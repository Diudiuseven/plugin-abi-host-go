package pluginhost

import (
	"errors"
	"fmt"
	"strings"
)

// LoadErrorKind enumerates the deterministic load-time rejection reasons.
type LoadErrorKind string

const (
	ErrManifestInvalid   LoadErrorKind = "manifest_invalid"
	ErrGenerationRange   LoadErrorKind = "generation_range_unsatisfied"
	ErrUnsupportedGen    LoadErrorKind = "unsupported_generation"
	ErrEntryMissing      LoadErrorKind = "entry_missing"
	ErrEntryDuplicate    LoadErrorKind = "entry_duplicate"
	ErrEntryTypeMismatch LoadErrorKind = "entry_type_mismatch"
	ErrCapabilityMissing LoadErrorKind = "capability_missing"
	ErrBannerMismatch    LoadErrorKind = "banner_mismatch"
	ErrDuplicateInstance LoadErrorKind = "duplicate_instance"
	ErrArtifactFailure   LoadErrorKind = "artifact_failure"
)

// LoadError identifies a rejected module and, when relevant, the concrete
// entry point that caused the rejection. Every rejection can therefore be
// traced to a specific module (Module) and, when applicable, entry (Entry).
type LoadError struct {
	Kind   LoadErrorKind `json:"kind"`
	Module string        `json:"module"`
	Entry  string        `json:"entry,omitempty"`
	Detail string        `json:"detail,omitempty"`
}

func (e *LoadError) Error() string {
	loc := e.Module
	if e.Entry != "" {
		loc = e.Module + "#" + e.Entry
	}
	if e.Detail != "" {
		return fmt.Sprintf("module %q rejected: %s: %s", loc, e.Kind, e.Detail)
	}
	return fmt.Sprintf("module %q rejected: %s", loc, e.Kind)
}

// LoadErrors bundles all rejection reasons produced while validating one
// load transaction. The transaction is all-or-nothing: if the bundle is
// non-empty, nothing from the transaction is committed.
type LoadErrors []*LoadError

func (es LoadErrors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return "load transaction rejected: " + strings.Join(parts, "; ")
}

// AsLoadErrors extracts a LoadErrors bundle from err when present.
func AsLoadErrors(err error) (LoadErrors, bool) {
	var le LoadErrors
	if errors.As(err, &le) {
		return le, true
	}
	return nil, false
}

// IsolatedError is returned when a call targets a module currently in the
// isolated state.
type IsolatedError struct {
	Module   string
	Reason   string
	Recovery string
}

func (e *IsolatedError) Error() string {
	return fmt.Sprintf("module %q is isolated (last failure: %s); recover with Host.Recover", e.Module, e.Reason)
}

// CallError describes a failed module invocation.
type CallError struct {
	Module string `json:"module"`
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

func (e *CallError) Error() string {
	return fmt.Sprintf("call to %q#%s failed: %s", e.Module, e.Entry, e.Reason)
}
