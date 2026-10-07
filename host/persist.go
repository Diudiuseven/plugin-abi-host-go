package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
)

// Store persists the last successfully committed manifest set so that a
// restart can atomically reuse it. A store never holds two sets at once:
// either the previous good set is intact on disk, or the new good set is
// fully on disk.
type Store interface {
	Save(ctx context.Context, setID uint64, manifests []pluginhost.Manifest) error
	Load(ctx context.Context) (uint64, []pluginhost.Manifest, error)
}

type persistedSet struct {
	SetID     uint64                `json:"set_id"`
	Manifests []pluginhost.Manifest `json:"manifests"`
}

// ErrNoSavedSet reports that no committed set exists yet.
var ErrNoSavedSet = errors.New("no persisted module set")

// FileStore stores the committed set as a single JSON file replaced
// atomically (write temp file + fsync + rename).
type FileStore struct {
	dir      string
	fileName string
}

// NewFileStore creates a file-backed store under dir.
func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir, fileName: "moduleset.json"}
}

func (s *FileStore) path() string { return filepath.Join(s.dir, s.fileName) }

func (s *FileStore) Save(_ context.Context, setID uint64, manifests []pluginhost.Manifest) error {
	if manifests == nil {
		manifests = []pluginhost.Manifest{}
	}
	payload, err := json.MarshalIndent(persistedSet{SetID: setID, Manifests: manifests}, "", "  ")
	if err != nil {
		return fmt.Errorf("persisting module set: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".moduleset-*")
	if err != nil {
		return fmt.Errorf("creating temp state file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("writing state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("fsync state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing state file: %w", err)
	}
	// rename is atomic on a single filesystem: readers see old or new file.
	if err := os.Rename(tmpName, s.path()); err != nil {
		cleanup()
		return fmt.Errorf("committing state file: %w", err)
	}
	return s.syncDir()
}

func (s *FileStore) syncDir() error {
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *FileStore) Load(_ context.Context) (uint64, []pluginhost.Manifest, error) {
	payload, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, ErrNoSavedSet
	}
	if err != nil {
		return 0, nil, fmt.Errorf("reading state file: %w", err)
	}
	var ps persistedSet
	if err := json.Unmarshal(payload, &ps); err != nil {
		return 0, nil, fmt.Errorf("state file corrupt (will not mix sets): %w", err)
	}
	return ps.SetID, ps.Manifests, nil
}
