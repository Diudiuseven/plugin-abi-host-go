package host

// Manifest is the contract a module declares at load time. It is returned
// by the module through the version-neutral ABI symbol ABIMetadata.
type Manifest struct {
	// Schema is the manifest schema version. Only 1 is defined.
	Schema int `json:"schema"`
	// ID is the stable module identifier. Repeated loads of the same ID at
	// the same generation never produce duplicate instances.
	ID string `json:"id"`
	// Version is a free-form module version string for diagnostics.
	Version string `json:"version"`
	// Generation is the interface generation the module was compiled
	// against. The host verifies it before exposing any entry.
	Generation int `json:"generation"`
	// Requires lists host capabilities the module depends on. A missing
	// capability rejects the module before it becomes callable.
	Requires []string `json:"requires"`
	// Entries declares every host-visible entry symbol explicitly. The host
	// interacts with a module only through names listed here.
	Entries []EntryDecl `json:"entries"`
}

// EntryDecl is one explicitly declared module entry point.
type EntryDecl struct {
	// Name is both the manifest name and the exported plugin symbol.
	Name string `json:"name"`
	// Kind is the contract kind of the entry, e.g. "call".
	Kind string `json:"kind"`
}

// validate checks the manifest for structural errors and deterministically
// rejects duplicates. It does not inspect the compiled artifact, so the
// same manifest is always judged the same way.
func (m *Manifest) validate() error {
	if m == nil {
		return &LoadError{Code: ErrMalformedManifest, Detail: "no manifest returned"}
	}
	if m.Schema != 1 {
		return &LoadError{Code: ErrMalformedManifest, Detail: "unsupported manifest schema"}
	}
	if m.ID == "" {
		return &LoadError{Code: ErrMalformedManifest, Detail: "module id is empty"}
	}
	if len(m.Entries) == 0 {
		return &LoadError{Code: ErrMalformedManifest, ModuleID: m.ID, Detail: "module declares no entries"}
	}
	seen := make(map[string]bool, len(m.Entries))
	for _, e := range m.Entries {
		if e.Name == "" {
			return &LoadError{Code: ErrMalformedManifest, ModuleID: m.ID, Detail: "entry name is empty"}
		}
		if e.Kind != KindCall {
			return &LoadError{Code: ErrMalformedManifest, ModuleID: m.ID, Entry: e.Name,
				Detail: "unknown entry kind: " + e.Kind}
		}
		if seen[e.Name] {
			return &LoadError{Code: ErrDuplicateEntry, ModuleID: m.ID, Entry: e.Name,
				Detail: "entry declared more than once in manifest"}
		}
		seen[e.Name] = true
	}
	return nil
}
