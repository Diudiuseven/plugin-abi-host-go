// Package host implements a local capability host that loads compiled
// third-party extension modules at runtime.
//
// The host binary and every module agree on a versioned interface
// contract. A module declares the interface generation it was compiled
// against; incompatible generations are rejected before the module
// becomes callable.
package host

import "fmt"

// LoadErrorCode identifies a deterministic, pre-load rejection reason.
type LoadErrorCode string

// Rejection codes are part of the stable API and are safe to match on.
const (
	ErrMalformedManifest  LoadErrorCode = "malformed_manifest"
	ErrDuplicateEntry     LoadErrorCode = "duplicate_entry"
	ErrMissingEntrySymbol LoadErrorCode = "missing_entry"
	ErrEntryTypeMismatch  LoadErrorCode = "entry_type_mismatch"
	ErrGenerationTooOld   LoadErrorCode = "generation_too_old"
	ErrGenerationTooNew   LoadErrorCode = "generation_too_new"
	ErrMissingCapability  LoadErrorCode = "missing_capability"
	ErrDuplicateInSet     LoadErrorCode = "duplicate_in_set"
	ErrDuplicateExisting  LoadErrorCode = "duplicate_existing"
	ErrModulePanic        LoadErrorCode = "module_panic"
	ErrStateReplay        LoadErrorCode = "state_replay_failed"
)

// LoadError reports why a module was rejected. Both the module identity
// and, when applicable, the offending entry name are always included so
// that the cause can be located precisely.
type LoadError struct {
	Code     LoadErrorCode
	ModuleID string
	Entry    string
	Detail   string
}

func (e *LoadError) Error() string {
	if e == nil {
		return ""
	}
	where := e.ModuleID
	if e.Entry != "" {
		where += "/" + e.Entry
	}
	msg := string(e.Code)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return fmt.Sprintf("host: module %s rejected (%s)", where, msg)
}
