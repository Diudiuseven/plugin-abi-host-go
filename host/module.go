package host

import (
	"errors"
	"fmt"
	"sync"
)

// ModuleStatus is the observable lifecycle state of a loaded module.
type ModuleStatus string

const (
	StatusHealthy     ModuleStatus = "healthy"
	StatusQuarantined ModuleStatus = "quarantined"
)

// ErrQuarantined is returned when an entry of an isolated module is called.
var ErrQuarantined = errors.New("host: module is quarantined")

// ErrUnknownEntry is returned when a called entry was not declared.
var ErrUnknownEntry = errors.New("host: entry not declared by module")

// ErrNoSuchModule is returned when the module identity is unknown.
var ErrNoSuchModule = errors.New("host: no such module")

// StatusReport is the queryable per-module state. Failure reasons and the
// recovery method are always included once a module has failed.
type StatusReport struct {
	ID           string       `json:"id"`
	Version      string       `json:"version,omitempty"`
	Generation   int          `json:"generation"`
	Entries      []string     `json:"entries"`
	Status       ModuleStatus `json:"status"`
	Failures     int          `json:"failures"`
	Threshold    int          `json:"threshold"`
	LastError    string       `json:"last_error,omitempty"`
	RecoveryHint string       `json:"recovery,omitempty"`
}

// module is one committed module instance. Its identity key is (ID,
// generation). Calls are isolated: a panic in an entry never escapes the
// host and repeated failures quarantine the instance.
type module struct {
	mu        sync.Mutex // guards status/failures/lastErr
	manifest  *Manifest
	path      string
	checksum  string
	entries   map[string]entryFn
	threshold int
	status    ModuleStatus
	failures  int
	lastErr   string
}

func (m *module) report() StatusReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.entries))
	for _, e := range m.manifest.Entries {
		names = append(names, e.Name)
	}
	r := StatusReport{
		ID:         m.manifest.ID,
		Version:    m.manifest.Version,
		Generation: m.manifest.Generation,
		Entries:    names,
		Status:     m.status,
		Failures:   m.failures,
		Threshold:  m.threshold,
		LastError:  m.lastErr,
	}
	if m.status == StatusQuarantined {
		r.RecoveryHint = "module isolated after reaching the failure threshold; call Host.Recover to return it to service"
	}
	return r
}

// call invokes a declared entry. The generation adapter (abi.go) supplies
// generation-correct semantics; this method owns isolation, failure
// accounting and quarantine.
func (m *module) call(name string, arg any) (out any, err error) {
	m.mu.Lock()
	quarantined := m.status == StatusQuarantined
	lastErr := m.lastErr
	m.mu.Unlock()
	if quarantined {
		return nil, fmt.Errorf("%w %s: %s", ErrQuarantined, m.manifest.ID, lastErr)
	}
	f, ok := m.entries[name]
	if !ok {
		// An unknown entry is a caller mistake, not a module failure.
		return nil, fmt.Errorf("%w: %s", ErrUnknownEntry, name)
	}

	defer func() {
		if r := recover(); r != nil {
			err = m.recordFailure(fmt.Errorf("panic in entry %s: %v", name, r))
		}
	}()
	out, err = f(arg)
	if err != nil {
		// Host-side argument adaptation errors are the caller's fault and
		// must not count against the module.
		var hc *hostCallError
		if errors.As(err, &hc) {
			return nil, err
		}
		return nil, m.recordFailure(err)
	}
	return out, nil
}

// recordFailure accounts for one module-originated failure and isolates the
// module once the threshold is reached.
func (m *module) recordFailure(err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
	m.lastErr = err.Error()
	if m.failures >= m.threshold && m.status != StatusQuarantined {
		m.status = StatusQuarantined
	}
	return fmt.Errorf("module %s: %w", m.manifest.ID, err)
}

// recover moves a quarantined module back to healthy state and clears the
// failure counters so it can serve traffic again.
func (m *module) recover() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = StatusHealthy
	m.failures = 0
	m.lastErr = ""
}
