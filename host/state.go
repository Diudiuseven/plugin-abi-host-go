package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// persistedState is the on-disk representation of one fully committed
// module set. It is written to a temp file and renamed into place so a
// crash during write can only leave the previous complete file or the new
// complete file: never a mixture of two sets.
type persistedState struct {
	Entries []persistedEntry `json:"entries"`
}

type persistedEntry struct {
	Path       string `json:"path"`
	ID         string `json:"id"`
	Generation int    `json:"generation"`
	// Checksum is sha256 of the artifact at commit time. On replay a
	// changed artifact rejects the whole replay, preventing silent
	// semantic drift between the committed set and the replayed one.
	Checksum string `json:"checksum"`
}

func snapshotEntries(snap map[string]*module) []persistedEntry {
	out := make([]persistedEntry, 0, len(snap))
	for _, m := range snap {
		out = append(out, persistedEntry{
			Path: m.path, ID: m.manifest.ID,
			Generation: m.manifest.Generation, Checksum: m.checksum,
		})
	}
	return out
}

func checksumOf(m *module) string { return m.checksum }

// persist writes the committed set atomically (temp file + rename, fsync).
// When no StatePath is configured the host is ephemeral and persistence is
// a no-op.
func (h *Host) persist(entries []persistedEntry) error {
	if h.statePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(persistedState{Entries: entries}, "", "  ")
	if err != nil {
		return &LoadError{Code: ErrStateReplay, Detail: "encode state: " + err.Error()}
	}
	dir := filepath.Dir(h.statePath)
	tmp, err := os.CreateTemp(dir, ".host-state-*")
	if err != nil {
		return &LoadError{Code: ErrStateReplay, Detail: "create temp state: " + err.Error()}
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return &LoadError{Code: ErrStateReplay, Detail: "write state: " + err.Error()}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return &LoadError{Code: ErrStateReplay, Detail: "sync state: " + err.Error()}
	}
	if err := tmp.Close(); err != nil {
		return &LoadError{Code: ErrStateReplay, Detail: "close state: " + err.Error()}
	}
	if err := os.Rename(tmpName, h.statePath); err != nil {
		return &LoadError{Code: ErrStateReplay, Detail: "rename state: " + err.Error()}
	}
	cleanup = false
	return nil
}

// restore fully replays the last committed set. Replay is all-or-nothing:
// if any entry cannot be revalidated (missing artifact, changed checksum,
// or the artifact no longer satisfies its manifest), the host returns an
// error and stays empty rather than mixing a partial old set with a new
// one. A missing state file simply means an empty first start.
func (h *Host) restore() error {
	data, err := os.ReadFile(h.statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &LoadError{Code: ErrStateReplay, Detail: "read state: " + err.Error()}
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return &LoadError{Code: ErrStateReplay, Detail: "decode state: " + err.Error()}
	}

	next := make(map[string]*module, len(st.Entries))
	// Re-validate in a deterministic order so identical state always
	// produces the same verdict (replay reproducibility).
	for i := range st.Entries {
		e := st.Entries[i]
		sum, err := sha256File(e.Path)
		if err != nil {
			return &LoadError{Code: ErrStateReplay, ModuleID: e.ID,
				Detail: fmt.Sprintf("replay artifact unreadable: %v", err)}
		}
		if sum != e.Checksum {
			return &LoadError{Code: ErrStateReplay, ModuleID: e.ID,
				Detail: "artifact changed since the set was committed"}
		}
		art, err := loadArtifact(e.Path, h.caps)
		if err != nil {
			return &LoadError{Code: ErrStateReplay, ModuleID: e.ID,
				Detail: "replay validation failed: " + err.Error()}
		}
		if art.manifest.ID != e.ID || art.manifest.Generation != e.Generation {
			return &LoadError{Code: ErrStateReplay, ModuleID: e.ID,
				Detail: "artifact identity no longer matches committed state"}
		}
		key := identity(e.ID, e.Generation)
		if _, dup := next[key]; dup {
			return &LoadError{Code: ErrDuplicateInSet, ModuleID: e.ID,
				Detail: "duplicate identity inside persisted set"}
		}
		next[key] = &module{
			manifest: art.manifest, path: e.Path, checksum: e.Checksum,
			entries: art.entries, threshold: h.threshold, status: StatusHealthy,
		}
	}
	h.publish(next)
	return nil
}
