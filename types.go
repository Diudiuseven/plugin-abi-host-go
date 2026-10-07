// Package pluginhost defines the versioned contract between a local
// capability host and third-party extension modules.
//
// Extensions are shipped as pre-compiled executable artifacts and loaded at
// runtime into separate processes. The host communicates with each module
// over its stdio streams using a versioned JSON-RPC protocol.
package pluginhost

import "encoding/json"

// Generation identifies an interface generation (代次). A generation is an
// opaque monotonically increasing integer in the wire protocol; this SDK
// defines the generations it understands.
type Generation int

const (
	// Gen1 is the first published interface generation.
	Gen1 Generation = 1
	// Gen2 is the second published interface generation.
	Gen2 Generation = 2
)

// Kind enumerates the value kinds that may cross the host/module boundary.
// The set is intentionally closed: anything outside it is rejected at load
// time so that failures can never surface mid-call as type surprises.
type Kind string

const (
	KindString Kind = "string"
	KindInt    Kind = "int"
	KindBool   Kind = "bool"
)

// EntrySpec declares a single explicit module entry point.
type EntrySpec struct {
	// Name is the unique entry point name inside a module.
	Name string `json:"name"`
	// Input is the ordered list of parameter kinds.
	Input []Kind `json:"input"`
	// Output is the entry point result kind.
	Output Kind `json:"output"`
}

// Manifest is the load-time declaration shipped next to a module artifact.
type Manifest struct {
	// ModuleID is the stable identifier of the extension module.
	ModuleID string `json:"module_id"`
	// Version is a human/tool readable module version (semver recommended).
	Version string `json:"version"`
	// Generation is the interface generation the module was compiled against.
	Generation Generation `json:"generation"`
	// MinHostGeneration / MaxHostGeneration declare the host generation range
	// the module is known to work with. Zero means unbounded on that side.
	MinHostGeneration Generation `json:"min_host_generation,omitempty"`
	MaxHostGeneration Generation `json:"max_host_generation,omitempty"`
	// Capabilities are optional host capabilities the module requires.
	Capabilities []string `json:"capabilities,omitempty"`
	// Entries is the explicit, exhaustive list of entry points.
	Entries []EntrySpec `json:"entries"`
	// ArtifactPath is the path (absolute or relative to the manifest) of the
	// compiled executable.
	ArtifactPath string `json:"artifact_path"`
}

// Banner is what a module process sends first on stdout.
type Banner struct {
	Protocol     string      `json:"protocol"`
	ProtocolVer  int         `json:"banner_version"`
	ModuleID     string      `json:"module_id"`
	Version      string      `json:"version"`
	Generation   Generation  `json:"generation"`
	Entries      []EntrySpec `json:"entries"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

// Hello is the host's first message on the module's stdin.
type Hello struct {
	Protocol       string     `json:"protocol"`
	HostGeneration Generation `json:"host_generation"`
}

// Request is a single host -> module call.
type Request struct {
	ID    uint64 `json:"id"`
	Entry string `json:"entry"`
	Args  []any  `json:"args"`
}

// Response is a single module -> host call result.
type Response struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ModuleState is the lifecycle state of a loaded module.
type ModuleState string

const (
	StateHealthy  ModuleState = "healthy"
	StateIsolated ModuleState = "isolated"
	StateRejected ModuleState = "rejected"
)

// FailureInfo describes why a module ended up in its current state.
type FailureInfo struct {
	Reason   string `json:"reason"`
	Count    int    `json:"count"`
	LastAt   string `json:"last_at,omitempty"`
	Recovery string `json:"recovery,omitempty"`
}
