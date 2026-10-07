package pluginhost

// Wire protocol constants shared by the host and the module SDK.
//
// The transport is line-delimited JSON over the module process's stdin and
// stdout ("JSON-RPC 1.0-ish", one JSON object per line):
//
//  1. the host writes one Hello line to stdin,
//  2. the module writes exactly one Banner line on stdout,
//  3. afterwards Request and Response messages strictly alternate, matched
//     by their numeric ID; the module may also emit a Log line.
const (
	// ProtocolName identifies the line-delimited JSON stdio protocol.
	ProtocolName = "pluginhost-jsonrpc"
	// ProtocolVersion is the transport/framing version. It is independent of
	// the interface Generation: framing rarely changes, semantics evolve by
	// generation.
	ProtocolVersion = 1

	// PingEntry is the built-in entry implemented by the module SDK itself.
	// It is not part of a manifest and is used for liveness checks.
	PingEntry = "__ping__"
)

// Log is an optional module -> host diagnostic line.
type Log struct {
	Type    string `json:"type"` // always "log"
	Level   string `json:"level"`
	Message string `json:"message"`
}
