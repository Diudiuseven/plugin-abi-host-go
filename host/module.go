package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"sync"
	"time"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
)

// FailureThreshold is the number of consecutive module failures after which a
// module is automatically isolated.
const FailureThreshold = 3

// handshakeTimeout bounds the banner exchange with a freshly spawned module.
const handshakeTimeout = 10 * time.Second

// stderrCapture bounds how much module stderr the host retains for diagnosis.
const stderrCapture = 4096

// Module is a live module subprocess pinned to exactly one interface
// generation. Every call it serves is validated against that generation's
// semantics, so modules of different generations never share or distort each
// other's semantics.
type Module struct {
	mu sync.Mutex

	manifest       pluginhost.Manifest
	hostGeneration pluginhost.Generation

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	writeM sync.Mutex
	nextID uint64

	stderr *cappedBuffer
	waitCh chan error

	state     pluginhost.ModuleState
	failCount int
	failure   pluginhost.FailureInfo
}

type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len()+len(p) > stderrCapture {
		b.buf.Reset()
		if len(p) > stderrCapture {
			p = p[len(p)-stderrCapture:]
		}
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Key renders the unique instance key of a module: same ID may coexist across
// generations, same ID plus same generation may not.
func Key(id string, gen pluginhost.Generation) string {
	return fmt.Sprintf("%s@%d", id, gen)
}

// Start spawns the module process and performs the versioned handshake. Any
// incompatibility is reported before the module is considered loaded: a bad
// module can never crash the host later to reveal an ABI problem.
func Start(ctx context.Context, hostGen pluginhost.Generation, m pluginhost.Manifest) (*Module, *pluginhost.LoadError) {
	mod := &Module{
		manifest:       m,
		hostGeneration: hostGen,
		state:          pluginhost.StateHealthy,
		stderr:         &cappedBuffer{},
		waitCh:         make(chan error, 1),
	}
	if lerr := mod.spawn(ctx); lerr != nil {
		return nil, lerr
	}
	if lerr := mod.handshake(ctx); lerr != nil {
		mod.Stop()
		return nil, lerr
	}
	return mod, nil
}

func (mod *Module) spawn(_ context.Context) *pluginhost.LoadError {
	cmd := exec.Command(mod.manifest.ArtifactPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return mod.loadErr(pluginhost.ErrArtifactFailure, "", err.Error())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return mod.loadErr(pluginhost.ErrArtifactFailure, "", err.Error())
	}
	cmd.Stderr = mod.stderr
	if err := cmd.Start(); err != nil {
		return mod.loadErr(pluginhost.ErrArtifactFailure, "",
			fmt.Sprintf("cannot start artifact %q: %v", mod.manifest.ArtifactPath, err))
	}
	mod.cmd = cmd
	mod.stdin = stdin
	mod.reader = bufio.NewReaderSize(stdout, 1<<20)
	go func() { mod.waitCh <- cmd.Wait() }()
	return nil
}

func (mod *Module) loadErr(kind pluginhost.LoadErrorKind, entry, detail string) *pluginhost.LoadError {
	return &pluginhost.LoadError{Kind: kind, Module: mod.manifest.ModuleID, Entry: entry, Detail: detail}
}

// handshake exchanges Hello/Banner and cross-checks the running artifact
// against its manifest. Nothing short of an exact match is accepted.
func (mod *Module) handshake(parent context.Context) *pluginhost.LoadError {
	ctx, cancel := context.WithTimeout(parent, handshakeTimeout)
	defer cancel()

	hello := pluginhost.Hello{Protocol: pluginhost.ProtocolName, HostGeneration: mod.hostGeneration}
	if err := writeLine(mod.stdin, hello); err != nil {
		return mod.loadErr(pluginhost.ErrArtifactFailure, "", "sending hello: "+err.Error())
	}

	var banner pluginhost.Banner
	lineCh := make(chan struct {
		line []byte
		err  error
	}, 1)
	go func() {
		line, err := mod.reader.ReadBytes('\n')
		lineCh <- struct {
			line []byte
			err  error
		}{line, err}
	}()
	select {
	case <-ctx.Done():
		return mod.loadErr(pluginhost.ErrArtifactFailure, "", "handshake timed out")
	case got := <-lineCh:
		if got.err != nil {
			return mod.loadErr(pluginhost.ErrArtifactFailure, "",
				fmt.Sprintf("reading banner: %v (stderr: %s)", got.err, mod.stderr.String()))
		}
		if err := json.Unmarshal(bytes.TrimSpace(got.line), &banner); err != nil {
			return mod.loadErr(pluginhost.ErrBannerMismatch, "", "banner is not valid JSON: "+err.Error())
		}
	}

	return mod.checkBanner(banner)
}

func (mod *Module) checkBanner(b pluginhost.Banner) *pluginhost.LoadError {
	m := mod.manifest
	reject := func(kind pluginhost.LoadErrorKind, entry, detail string) *pluginhost.LoadError {
		return mod.loadErr(kind, entry, detail)
	}
	if b.Protocol != pluginhost.ProtocolName {
		return reject(pluginhost.ErrBannerMismatch, "", "protocol mismatch: "+b.Protocol)
	}
	if b.ProtocolVer != pluginhost.ProtocolVersion {
		return reject(pluginhost.ErrBannerMismatch, "",
			fmt.Sprintf("banner protocol version %d != host %d", b.ProtocolVer, pluginhost.ProtocolVersion))
	}
	if b.ModuleID != m.ModuleID {
		return reject(pluginhost.ErrBannerMismatch, "",
			fmt.Sprintf("banner module_id %q != manifest %q", b.ModuleID, m.ModuleID))
	}
	if b.Generation != m.Generation {
		return reject(pluginhost.ErrBannerMismatch, "",
			fmt.Sprintf("banner generation %d != manifest %d", b.Generation, m.Generation))
	}

	// Entries: same set, no duplicates, same signatures, same order-independent
	// content. The host only interacts with declared entries.
	seen := map[string]bool{}
	bannerByName := map[string]pluginhost.EntrySpec{}
	for _, e := range b.Entries {
		if e.Name == "" {
			return reject(pluginhost.ErrBannerMismatch, "", "banner contains an unnamed entry")
		}
		if seen[e.Name] {
			return reject(pluginhost.ErrEntryDuplicate, e.Name, "duplicate entry reported by banner")
		}
		seen[e.Name] = true
		bannerByName[e.Name] = e
	}
	for _, want := range m.Entries {
		got, ok := bannerByName[want.Name]
		if !ok {
			return reject(pluginhost.ErrEntryMissing, want.Name, "entry declared in manifest but not provided by module")
		}
		if !entriesEqual(got, want) {
			return reject(pluginhost.ErrEntryTypeMismatch, want.Name,
				fmt.Sprintf("module provides %s but manifest declares %s", formatEntry(got), formatEntry(want)))
		}
	}
	for name := range bannerByName {
		if _, ok := m.FindEntry(name); !ok {
			return reject(pluginhost.ErrEntryMissing, name, "entry provided by module but not declared in manifest")
		}
	}
	return nil
}

func entriesEqual(a, b pluginhost.EntrySpec) bool {
	if a.Name != b.Name || a.Output != b.Output || len(a.Input) != len(b.Input) {
		return false
	}
	for i := range a.Input {
		if a.Input[i] != b.Input[i] {
			return false
		}
	}
	return true
}

func formatEntry(e pluginhost.EntrySpec) string {
	return fmt.Sprintf("%s%v->%s", e.Name, e.Input, e.Output)
}

func writeLine(w io.Writer, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = w.Write(line)
	return err
}

// Call invokes a declared entry point. Module-originated failures (handler
// errors, panics surfaced over the wire, process crashes, protocol errors,
// Gen2 contract violations) count toward isolation; caller-side argument
// mistakes are rejected before dispatch and do not count.
func (mod *Module) Call(ctx context.Context, entry string, args []any) (any, error) {
	mod.mu.Lock()
	defer mod.mu.Unlock()

	if mod.state == pluginhost.StateIsolated {
		return nil, &pluginhost.IsolatedError{
			Module: mod.manifest.ModuleID, Reason: mod.failure.Reason, Recovery: mod.failure.Recovery,
		}
	}

	spec, ok := mod.manifest.FindEntry(entry)
	if !ok {
		return nil, &pluginhost.CallError{Module: mod.manifest.ModuleID, Entry: entry, Reason: "entry not declared"}
	}

	norm, argErr := normalizeArguments(mod.manifest.Generation, spec, args)
	if argErr != nil {
		return nil, &pluginhost.CallError{Module: mod.manifest.ModuleID, Entry: entry, Reason: argErr.Error()}
	}

	result, err := mod.roundTrip(ctx, entry, norm)
	if err != nil {
		mod.recordFailure(err.Error())
		return nil, &pluginhost.CallError{Module: mod.manifest.ModuleID, Entry: entry, Reason: err.Error()}
	}

	if mod.manifest.Generation >= pluginhost.Gen2 {
		if vErr := validateValue(spec.Output, result); vErr != nil {
			reason := "module returned value violating Gen2 contract: " + vErr.Error()
			mod.recordFailure(reason)
			return nil, &pluginhost.CallError{Module: mod.manifest.ModuleID, Entry: entry, Reason: reason}
		}
	}
	result = coerceResult(spec, result)

	mod.failCount = 0
	mod.failure = pluginhost.FailureInfo{}
	return result, nil
}

// roundTrip sends one request and reads exactly one matching response. Any
// deviation (process exit, malformed line, mismatched id, module-reported
// error) is a failure attributable to the module.
func (mod *Module) roundTrip(ctx context.Context, entry string, args []any) (any, error) {
	mod.writeM.Lock()
	mod.nextID++
	id := mod.nextID
	if err := writeLine(mod.stdin, pluginhost.Request{ID: id, Entry: entry, Args: args}); err != nil {
		mod.writeM.Unlock()
		return nil, fmt.Errorf("writing request: %w", err)
	}
	mod.writeM.Unlock()

	type lineResult struct {
		line []byte
		err  error
	}
	for {
		lineCh := make(chan lineResult, 1)
		go func() {
			line, err := mod.reader.ReadBytes('\n')
			lineCh <- lineResult{line: line, err: err}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case got := <-lineCh:
			if got.err != nil {
				if exitErr := mod.probeExit(); exitErr != "" {
					return nil, fmt.Errorf("%v; module process exited: %s", got.err, exitErr)
				}
				return nil, got.err
			}
			var generic map[string]json.RawMessage
			if err := json.Unmarshal(bytes.TrimSpace(got.line), &generic); err != nil {
				return nil, fmt.Errorf("malformed response: %w", err)
			}
			if t, _ := generic["type"]; string(t) == `"log"` {
				continue // best-effort diagnostic line
			}
			var resp pluginhost.Response
			if err := json.Unmarshal(bytes.TrimSpace(got.line), &resp); err != nil {
				return nil, fmt.Errorf("malformed response: %w", err)
			}
			if resp.ID != id {
				return nil, fmt.Errorf("response id %d does not match request %d", resp.ID, id)
			}
			if !resp.OK {
				return nil, errors.New("module error: " + resp.Error)
			}
			var val any
			if err := json.Unmarshal(resp.Result, &val); err != nil {
				return nil, fmt.Errorf("malformed result: %w", err)
			}
			return val, nil
		}
	}
}

func (mod *Module) probeExit() string {
	select {
	case err := <-mod.waitCh:
		if err == nil {
			return "exit status 0"
		}
		return err.Error() + " (stderr: " + mod.stderr.String() + ")"
	default:
		return ""
	}
}

func (mod *Module) recordFailure(reason string) {
	mod.failCount++
	mod.failure = pluginhost.FailureInfo{
		Reason:   reason,
		Count:    mod.failCount,
		LastAt:   time.Now().UTC().Format(time.RFC3339),
		Recovery: fmt.Sprintf("call Recover(%q) to restart and re-verify the module", mod.manifest.ModuleID),
	}
	if mod.failCount >= FailureThreshold {
		mod.state = pluginhost.StateIsolated
	}
}

// State returns the current lifecycle state and failure details.
func (mod *Module) State() (pluginhost.ModuleState, pluginhost.FailureInfo) {
	mod.mu.Lock()
	defer mod.mu.Unlock()
	f := mod.failure
	if mod.state == pluginhost.StateHealthy {
		f = pluginhost.FailureInfo{}
	}
	return mod.state, f
}

// Recover takes an isolated module back into service by terminating its
// process, spawning a fresh one and re-running the handshake.
func (mod *Module) Recover(ctx context.Context) error {
	mod.mu.Lock()
	defer mod.mu.Unlock()

	mod.stopProcess()
	mod.stderr = &cappedBuffer{}
	mod.waitCh = make(chan error, 1)
	if lerr := mod.spawn(ctx); lerr != nil {
		mod.failure.Recovery = "artifact cannot be restarted: " + lerr.Detail
		return errors.New(lerr.Error())
	}
	if lerr := mod.handshake(ctx); lerr != nil {
		mod.stopProcess()
		mod.failure.Recovery = "handshake still failing: " + lerr.Error()
		return errors.New(lerr.Error())
	}
	mod.state = pluginhost.StateHealthy
	mod.failCount = 0
	mod.failure = pluginhost.FailureInfo{}
	return nil
}

// Stop terminates the module process.
func (mod *Module) Stop() {
	mod.mu.Lock()
	defer mod.mu.Unlock()
	mod.stopProcess()
}

func (mod *Module) stopProcess() {
	if mod.cmd == nil || mod.cmd.Process == nil {
		return
	}
	_ = mod.stdin.Close()
	_ = mod.cmd.Process.Kill()
	select {
	case <-mod.waitCh:
	case <-time.After(time.Second):
	}
	mod.cmd = nil
}

// Manifest returns the manifest the module was loaded from.
func (mod *Module) Manifest() pluginhost.Manifest { return mod.manifest }

// normalizeArguments enforces the argument semantics of the module's own
// generation. Gen1 is lenient for backwards compatibility (missing arguments
// become zero values, extra arguments are dropped, mistyped present
// arguments are replaced by zero values). Gen2 requires exact arity and
// exact kinds and rejects the call before it reaches the module.
func normalizeArguments(gen pluginhost.Generation, spec pluginhost.EntrySpec, args []any) ([]any, error) {
	if gen >= pluginhost.Gen2 {
		if len(args) != len(spec.Input) {
			return nil, fmt.Errorf("Gen2 arity mismatch: entry expects %d args, got %d", len(spec.Input), len(args))
		}
		for i, k := range spec.Input {
			if err := validateValue(k, args[i]); err != nil {
				return nil, fmt.Errorf("Gen2 argument %d: %w", i, err)
			}
		}
		return append([]any(nil), args...), nil
	}

	// Gen1 legacy lenient behavior.
	out := make([]any, len(spec.Input))
	for i, k := range spec.Input {
		switch {
		case i < len(args) && valueMatches(k, args[i]):
			out[i] = args[i]
		case i < len(args):
			out[i] = zeroValue(k) // best-effort instead of rejecting
		default:
			out[i] = zeroValue(k) // missing argument
		}
	}
	return out, nil
}

func coerceResult(spec pluginhost.EntrySpec, v any) any {
	if spec.Output == pluginhost.KindInt {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return v
}

func zeroValue(k pluginhost.Kind) any {
	switch k {
	case pluginhost.KindString:
		return ""
	case pluginhost.KindInt:
		return 0
	case pluginhost.KindBool:
		return false
	}
	return nil
}

func validateValue(k pluginhost.Kind, v any) error {
	if !valueMatches(k, v) {
		return fmt.Errorf("expected %s, got %T", k, v)
	}
	return nil
}

func valueMatches(k pluginhost.Kind, v any) bool {
	if v == nil {
		return false
	}
	switch k {
	case pluginhost.KindString:
		return reflect.TypeOf(v).Kind() == reflect.String
	case pluginhost.KindInt:
		switch reflect.TypeOf(v).Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return true
		}
		return false
	case pluginhost.KindBool:
		return reflect.TypeOf(v).Kind() == reflect.Bool
	}
	return false
}
