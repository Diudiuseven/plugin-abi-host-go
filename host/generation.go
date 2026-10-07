package host

import (
	"fmt"
	"sort"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
)

// Policy decides how the host reacts to modules built for a generation that
// does not exactly match the host generation.
type Policy int

const (
	// PolicyReject rejects any module whose own generation is not exactly the
	// host generation, and rejects generation-range mismatches as well.
	PolicyReject Policy = iota
	// PolicyAllowLegacy admits modules built for older generations that this
	// host still retains native support for. Modules built for a newer
	// generation than the host are always rejected.
	PolicyAllowLegacy
)

// Support describes the generations and capabilities a host can provide.
// Multiple module generations may coexist inside one process: each module is
// pinned to its own generation and served with its own semantics, so this
// matrix never changes the meaning of a call for an already loaded module.
type Support struct {
	// HostGeneration is the generation implemented by this host build.
	HostGeneration pluginhost.Generation
	// MinGeneration is the oldest module generation the host retains support
	// for. Under PolicyReject only HostGeneration is accepted regardless of
	// this range.
	MinGeneration pluginhost.Generation
	// MaxGeneration is the newest module generation understood by the host.
	// Modules newer than this cannot be interpreted and are always rejected.
	MaxGeneration pluginhost.Generation
	// Capabilities are optional interface capabilities this host exposes.
	Capabilities map[string]bool
	// Policy selects the rejection/compatibility strategy.
	Policy Policy
}

// DefaultSupport returns the support matrix of this SDK build: native support
// for Gen1 and Gen2, current generation Gen2, legacy allowed.
func DefaultSupport() Support {
	return Support{
		HostGeneration: pluginhost.Gen2,
		MinGeneration:  pluginhost.Gen1,
		MaxGeneration:  pluginhost.Gen2,
		Capabilities: map[string]bool{
			"logging": true,
		},
		Policy: PolicyAllowLegacy,
	}
}

// CheckGeneration validates a manifest's generation declarations against the
// support matrix and the module's own declared compatible host range.
func (s Support) CheckGeneration(m pluginhost.Manifest) *pluginhost.LoadError {
	mk := func(kind pluginhost.LoadErrorKind, detail string) *pluginhost.LoadError {
		return &pluginhost.LoadError{Kind: kind, Module: m.ModuleID, Detail: detail}
	}

	// The module states which host generations it supports; the running host
	// must fall inside that declared range.
	if m.MinHostGeneration != 0 && s.HostGeneration < m.MinHostGeneration {
		return mk(pluginhost.ErrGenerationRange,
			fmt.Sprintf("module requires host generation >= %d, host is %d",
				m.MinHostGeneration, s.HostGeneration))
	}
	if m.MaxHostGeneration != 0 && s.HostGeneration > m.MaxHostGeneration {
		return mk(pluginhost.ErrGenerationRange,
			fmt.Sprintf("module requires host generation <= %d, host is %d",
				m.MaxHostGeneration, s.HostGeneration))
	}

	switch s.Policy {
	case PolicyReject:
		if m.Generation != s.HostGeneration {
			return mk(pluginhost.ErrUnsupportedGen,
				fmt.Sprintf("module generation %d does not match host generation %d (policy: reject)",
					m.Generation, s.HostGeneration))
		}
	case PolicyAllowLegacy:
		if m.Generation > s.MaxGeneration {
			return mk(pluginhost.ErrUnsupportedGen,
				fmt.Sprintf("module generation %d is newer than host understands (max %d)",
					m.Generation, s.MaxGeneration))
		}
		if m.Generation < s.MinGeneration {
			return mk(pluginhost.ErrUnsupportedGen,
				fmt.Sprintf("module generation %d is older than host still supports (min %d)",
					m.Generation, s.MinGeneration))
		}
	default:
		return mk(pluginhost.ErrUnsupportedGen, "unknown host generation policy")
	}
	return nil
}

// CheckCapabilities validates every capability required by the manifest.
func (s Support) CheckCapabilities(m pluginhost.Manifest) *pluginhost.LoadError {
	missing := make([]string, 0)
	for _, c := range m.Capabilities {
		if !s.Capabilities[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return &pluginhost.LoadError{
		Kind:   pluginhost.ErrCapabilityMissing,
		Module: m.ModuleID,
		Detail: fmt.Sprintf("host lacks required capabilities: %v", missing),
	}
}
