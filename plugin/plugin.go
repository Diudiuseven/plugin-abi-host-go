// Package plugin is the SDK used by extension module authors. A module is a
// regular executable that declares a manifest, attaches handlers to its
// explicit entry points and calls Serve. The host spawns it as a subprocess
// and drives the stdio JSON protocol implemented here.
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
)

// HandlerFunc is the uniform in-process entry point signature.
type HandlerFunc func(ctx context.Context, args []any) (any, error)

// Module is a module-side entry point registry.
type Module struct {
	manifest pluginhost.Manifest
	handlers map[string]HandlerFunc
	in       io.Reader
	out      io.Writer
	writeMu  sync.Mutex
}

// New creates a module declaring the given manifest.
func New(m pluginhost.Manifest) *Module {
	return &Module{
		manifest: m,
		handlers: make(map[string]HandlerFunc),
		in:       os.Stdin,
		out:      os.Stdout,
	}
}

// Handle registers the handler for an explicitly declared entry point. It
// panics on programmer error (unknown or duplicate entry), which fails the
// program at startup rather than serving an ambiguous contract.
func (mod *Module) Handle(name string, fn HandlerFunc) {
	spec, ok := mod.manifest.FindEntry(name)
	if !ok {
		panic(fmt.Sprintf("plugin: Handle(%q) but no such entry is declared in the manifest", name))
	}
	if fn == nil {
		panic(fmt.Sprintf("plugin: Handle(%q) with nil handler", name))
	}
	if _, dup := mod.handlers[spec.Name]; dup {
		panic(fmt.Sprintf("plugin: duplicate handler registration for entry %q", name))
	}
	mod.handlers[spec.Name] = fn
}

// Logger emits a best-effort diagnostic line to the host.
type Logger struct{ mod *Module }

// Log sends one diagnostic line; it never blocks protocol handling.
func (mod *Module) Log(level, msg string) {
	mod.emit(pluginhost.Log{Type: "log", Level: level, Message: msg})
}

func (mod *Module) emit(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	line = append(line, '\n')
	mod.writeMu.Lock()
	defer mod.writeMu.Unlock()
	_, _ = mod.out.Write(line)
}

// Serve runs the stdio protocol until the stream ends. Startup-time contract
// problems (unknown protocol, missing handler) abort the process before any
// banner is emitted, so the host rejects the module at load time.
func (mod *Module) Serve() error {
	reader := bufio.NewReader(mod.in)

	// 1. Hello.
	var hello pluginhost.Hello
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("reading host hello: %w", err)
	}
	if err := json.Unmarshal(line, &hello); err != nil {
		return fmt.Errorf("invalid host hello: %w", err)
	}
	if hello.Protocol != pluginhost.ProtocolName {
		return fmt.Errorf("unsupported protocol %q", hello.Protocol)
	}

	// 2. Every manifest entry must have a handler; fail fast otherwise.
	for _, e := range mod.manifest.Entries {
		if _, ok := mod.handlers[e.Name]; !ok {
			fmt.Fprintf(os.Stderr, "plugin: entry declared in manifest but no handler registered: %s\n", e.Name)
			return fmt.Errorf("entry %q declared but no handler registered", e.Name)
		}
	}

	// 3. Banner lists exactly the declared entries with their signatures.
	banner := pluginhost.Banner{
		Protocol:     pluginhost.ProtocolName,
		ProtocolVer:  pluginhost.ProtocolVersion,
		ModuleID:     mod.manifest.ModuleID,
		Version:      mod.manifest.Version,
		Generation:   mod.manifest.Generation,
		Entries:      mod.manifest.Entries,
		Capabilities: mod.manifest.Capabilities,
	}
	if err := mod.emitLocked(banner); err != nil {
		return fmt.Errorf("writing banner: %w", err)
	}

	// 4. Request loop. Calls are dispatched concurrently; responses are
	// serialized by writeMu. Each call is panic-isolated.
	var wg sync.WaitGroup
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(bytesTrim(line)) == 0 {
			break
		}
		if err != nil {
			wg.Wait()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading request: %w", err)
		}
		var req pluginhost.Request
		if jerr := json.Unmarshal(bytesTrim(line), &req); jerr != nil {
			mod.respondError(0, "malformed request: "+jerr.Error())
			continue
		}
		wg.Add(1)
		go func(req pluginhost.Request) {
			defer wg.Done()
			mod.dispatch(req)
		}(req)
	}
	wg.Wait()
	return nil
}

func bytesTrim(b []byte) []byte {
	return trimSpace(b)
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}

func (mod *Module) emitLocked(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	mod.writeMu.Lock()
	defer mod.writeMu.Unlock()
	_, err = mod.out.Write(line)
	return err
}

func (mod *Module) respondError(id uint64, msg string) {
	mod.emit(pluginhost.Response{ID: id, OK: false, Error: msg})
}

func (mod *Module) dispatch(req pluginhost.Request) {
	if req.Entry == pluginhost.PingEntry {
		mod.emit(pluginhost.Response{ID: req.ID, OK: true, Result: json.RawMessage(`true`)})
		return
	}

	handler, ok := mod.handlers[req.Entry]
	if !ok {
		mod.respondError(req.ID, "entry not found: "+req.Entry)
		return
	}

	var result any
	var callErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				callErr = fmt.Errorf("panic: %v", r)
			}
		}()
		result, callErr = handler(context.Background(), req.Args)
	}()

	if callErr != nil {
		mod.respondError(req.ID, callErr.Error())
		return
	}

	raw, err := json.Marshal(result)
	if err != nil {
		mod.respondError(req.ID, "cannot encode handler result: "+err.Error())
		return
	}
	mod.emit(pluginhost.Response{ID: req.ID, OK: true, Result: raw})
}
