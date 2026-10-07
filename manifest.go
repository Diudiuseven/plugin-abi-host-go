package pluginhost

import (
	"fmt"
	"sort"
)

// validKinds is the closed set of value kinds accepted on the wire.
var validKinds = map[Kind]bool{
	KindString: true,
	KindInt:    true,
	KindBool:   true,
}

// ValidateManifest performs the purely structural validation of a manifest.
// It is deterministic: the same input always yields the same errors in the
// same order. Host-side policy checks (generation range, capabilities) live
// in the host package so that validation does not depend on host config.
func ValidateManifest(m Manifest) LoadErrors {
	var errs LoadErrors
	reject := func(kind LoadErrorKind, entry, detail string) {
		errs = append(errs, &LoadError{Kind: kind, Module: m.ModuleID, Entry: entry, Detail: detail})
	}

	if m.ModuleID == "" {
		reject(ErrManifestInvalid, "", "module_id is empty")
	}
	if m.ArtifactPath == "" {
		reject(ErrManifestInvalid, "", "artifact_path is empty")
	}
	if m.Generation <= 0 {
		reject(ErrManifestInvalid, "", "generation must be positive")
	}
	if m.MinHostGeneration != 0 && m.MaxHostGeneration != 0 && m.MinHostGeneration > m.MaxHostGeneration {
		reject(ErrGenerationRange, "",
			fmt.Sprintf("min_host_generation %d > max_host_generation %d", m.MinHostGeneration, m.MaxHostGeneration))
	}
	if m.MinHostGeneration != 0 && m.MinHostGeneration < 0 {
		reject(ErrManifestInvalid, "", "min_host_generation must be positive or zero")
	}
	if m.MaxHostGeneration != 0 && m.MaxHostGeneration < 0 {
		reject(ErrManifestInvalid, "", "max_host_generation must be positive or zero")
	}
	if len(m.Entries) == 0 {
		reject(ErrManifestInvalid, "", "entries list is empty")
	}

	seen := map[string]int{}
	for i, e := range m.Entries {
		if e.Name == "" {
			reject(ErrManifestInvalid, fmt.Sprintf("entries[%d]", i), "entry name is empty")
			continue
		}
		if prev, dup := seen[e.Name]; dup {
			reject(ErrEntryDuplicate, e.Name,
				fmt.Sprintf("entry also declared at entries[%d]", prev))
		} else {
			seen[e.Name] = i
		}
		if !validKinds[e.Output] {
			reject(ErrEntryTypeMismatch, e.Name,
				fmt.Sprintf("unsupported output kind %q", e.Output))
		}
		for j, k := range e.Input {
			if !validKinds[k] {
				reject(ErrEntryTypeMismatch, e.Name,
					fmt.Sprintf("unsupported input kind %q at parameter %d", k, j))
			}
		}
	}

	// Deterministic order: structural errors are collected in manifest order,
	// but the public contract is "stable for equal input". Manifest order is
	// part of the input, so we sort by (entry, kind) for full reproducibility.
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Entry != errs[j].Entry {
			return errs[i].Entry < errs[j].Entry
		}
		if errs[i].Kind != errs[j].Kind {
			return errs[i].Kind < errs[j].Kind
		}
		return errs[i].Detail < errs[j].Detail
	})
	return errs
}

// FindEntry returns the declared spec of an entry point.
func (m Manifest) FindEntry(name string) (EntrySpec, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return EntrySpec{}, false
}
