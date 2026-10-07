package host

import (
	"encoding/json"
	"fmt"
	"plugin"
	"reflect"
)

// SupportedGenerations bounds the interface generations this host speaks.
const (
	MinGeneration = 1
	MaxGeneration = 2
)

// Entry symbol contract kinds.
const (
	KindCall = "call"
)

// metadataSymbol is the version-neutral entry every module must export.
// It returns the module manifest as JSON, which decouples the manifest
// schema from the Go plugin ABI.
const metadataSymbol = "ABIMetadata"

// entryFn is the uniform in-host representation of an invocable entry.
// Generation adapters convert the caller's generic argument into exactly
// the argument semantics the module's generation promises, so each
// generation of module is always observed through its own semantics.
type entryFn func(arg any) (any, error)

// hostCallError marks an error caused by the host-side caller (for
// example an argument whose type the generation does not accept). Such
// errors are never charged as module failures.
type hostCallError struct{ msg string }

func (e *hostCallError) Error() string { return e.msg }

// loadedArtifact holds the fully verified symbols of one compiled module.
type loadedArtifact struct {
	manifest *Manifest
	entries  map[string]entryFn
}

// loadArtifact opens a compiled module and verifies it fully against its
// manifest before returning: manifest well-formedness, generation range,
// required capabilities, and every declared entry symbol present with the
// generation-correct Go type. Any panic raised by the module during
// resolution is converted into a LoadError; a half-resolved artifact is
// never returned.
func loadArtifact(path string, capabilities map[string]bool) (art *loadedArtifact, err error) {
	var id string // best-effort for precise errors
	defer func() {
		if r := recover(); r != nil {
			err = &LoadError{Code: ErrModulePanic, ModuleID: id,
				Detail: fmt.Sprintf("panic while loading: %v", r)}
		}
	}()

	p, openErr := plugin.Open(path)
	if openErr != nil {
		return nil, &LoadError{Code: ErrMalformedManifest, Detail: "cannot open artifact: " + openErr.Error()}
	}

	sym, err := p.Lookup(metadataSymbol)
	if err != nil {
		return nil, &LoadError{Code: ErrMissingEntrySymbol, Entry: metadataSymbol,
			Detail: "module does not export the metadata entry"}
	}
	metaFn, ok := sym.(func() string)
	if !ok {
		return nil, &LoadError{Code: ErrEntryTypeMismatch, Entry: metadataSymbol,
			Detail: fmt.Sprintf("want func() string, got %s", reflect.TypeOf(sym))}
	}

	var m Manifest
	raw := metaFn()
	if jerr := json.Unmarshal([]byte(raw), &m); jerr != nil {
		return nil, &LoadError{Code: ErrMalformedManifest, Detail: "manifest is not valid JSON: " + jerr.Error()}
	}
	id = m.ID
	if verr := m.validate(); verr != nil {
		return nil, verr
	}

	// Generation gating happens before any entry is usable.
	if m.Generation < MinGeneration {
		return nil, &LoadError{Code: ErrGenerationTooOld, ModuleID: m.ID,
			Detail: fmt.Sprintf("module generation %d older than supported minimum %d", m.Generation, MinGeneration)}
	}
	if m.Generation > MaxGeneration {
		return nil, &LoadError{Code: ErrGenerationTooNew, ModuleID: m.ID,
			Detail: fmt.Sprintf("module generation %d newer than supported maximum %d", m.Generation, MaxGeneration)}
	}

	// Required host capabilities must exist before commitment.
	for _, capName := range m.Requires {
		if !capabilities[capName] {
			return nil, &LoadError{Code: ErrMissingCapability, ModuleID: m.ID,
				Detail: "required capability not provided by host: " + capName}
		}
	}

	entries := make(map[string]entryFn, len(m.Entries))
	for _, d := range m.Entries {
		f, rerr := resolveEntry(p, m.ID, d, m.Generation)
		if rerr != nil {
			return nil, rerr
		}
		entries[d.Name] = f
	}
	return &loadedArtifact{manifest: &m, entries: entries}, nil
}

// resolveEntry binds one declared symbol and verifies its Go type against
// the contract of the module's generation.
func resolveEntry(p *plugin.Plugin, moduleID string, d EntryDecl, gen int) (entryFn, error) {
	sym, err := p.Lookup(d.Name)
	if err != nil {
		return nil, &LoadError{Code: ErrMissingEntrySymbol, ModuleID: moduleID, Entry: d.Name,
			Detail: "entry declared in manifest is not exported by artifact"}
	}

	switch gen {
	case 1:
		// Generation 1 contract: entry is func(string) string.
		f, ok := sym.(func(string) string)
		if !ok {
			return nil, &LoadError{Code: ErrEntryTypeMismatch, ModuleID: moduleID, Entry: d.Name,
				Detail: fmt.Sprintf("generation 1 wants func(string) string, got %s", reflect.TypeOf(sym))}
		}
		return func(arg any) (any, error) {
			s, ok := arg.(string)
			if !ok {
				return nil, &hostCallError{msg: fmt.Sprintf("generation 1 entry %q requires a string argument", d.Name)}
			}
			return f(s), nil
		}, nil
	case 2:
		// Generation 2 contract: entry is func(map[string]any) (map[string]any, error).
		f, ok := sym.(func(map[string]any) (map[string]any, error))
		if !ok {
			return nil, &LoadError{Code: ErrEntryTypeMismatch, ModuleID: moduleID, Entry: d.Name,
				Detail: fmt.Sprintf("generation 2 wants func(map[string]any)(map[string]any, error), got %s", reflect.TypeOf(sym))}
		}
		return func(arg any) (any, error) {
			m, ok := arg.(map[string]any)
			if !ok {
				return nil, &hostCallError{msg: fmt.Sprintf("generation 2 entry %q requires a map[string]any argument", d.Name)}
			}
			out, ierr := f(m)
			return out, ierr
		}, nil
	default:
		// Unreachable: the generation range is gated in loadArtifact.
		return nil, &LoadError{Code: ErrGenerationTooNew, ModuleID: moduleID, Entry: d.Name,
			Detail: fmt.Sprintf("no adapter for generation %d", gen)}
	}
}
