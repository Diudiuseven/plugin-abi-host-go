package host_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
	"github.com/Diudiuseven/plugin-abi-host-go/host"
)

var (
	binDir     string
	projectDir string
	echoPath   string
	panicPath  string
	gen2Path   string
)

func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	projectDir = filepath.Join(filepath.Dir(thisFile), "..")
	binDir = filepath.Join(filepath.Dir(thisFile), "testdata", "bin")
	_ = os.MkdirAll(binDir, 0o755)

	plugins := map[string]string{
		"echo":     "./host/testdata/plugins/echo",
		"panicmod": "./host/testdata/plugins/panicmod",
		"gen2":     "./host/testdata/plugins/gen2",
	}
	for name, pkg := range plugins {
		out := filepath.Join(binDir, name)
		cmd := exec.Command("go", "build", "-o", out, pkg)
		cmd.Dir = projectDir
		if b, err := cmd.CombinedOutput(); err != nil {
			os.Stderr.Write(b)
			panic("building test plugin " + name + ": " + err.Error())
		}
	}
	echoPath = filepath.Join(binDir, "echo")
	panicPath = filepath.Join(binDir, "panicmod")
	gen2Path = filepath.Join(binDir, "gen2")

	os.Exit(m.Run())
}

func echoManifest(path string) pluginhost.Manifest {
	return pluginhost.Manifest{
		ModuleID:     "echo",
		Version:      "1.0.0",
		Generation:   pluginhost.Gen1,
		ArtifactPath: path,
		Entries: []pluginhost.EntrySpec{
			{Name: "echo", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString},
			{Name: "add", Input: []pluginhost.Kind{pluginhost.KindInt, pluginhost.KindInt}, Output: pluginhost.KindInt},
		},
	}
}

func panicManifest(path string) pluginhost.Manifest {
	return pluginhost.Manifest{
		ModuleID:     "panicmod",
		Version:      "1.0.0",
		Generation:   pluginhost.Gen1,
		ArtifactPath: path,
		Entries: []pluginhost.EntrySpec{
			{Name: "boom", Output: pluginhost.KindString},
			{Name: "crash", Output: pluginhost.KindString},
			{Name: "ok", Output: pluginhost.KindString},
		},
	}
}

func gen2Manifest(path string) pluginhost.Manifest {
	return pluginhost.Manifest{
		ModuleID:     "gen2echo",
		Version:      "2.0.0",
		Generation:   pluginhost.Gen2,
		ArtifactPath: path,
		Capabilities: []string{"logging"},
		Entries: []pluginhost.EntrySpec{
			{Name: "echo", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString},
		},
	}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func loadOrFail(t *testing.T, h *host.Host, manifests []pluginhost.Manifest) *host.Snapshot {
	t.Helper()
	c, cancel := ctx()
	defer cancel()
	snap, err := h.LoadSet(c, manifests)
	if err != nil {
		t.Fatalf("LoadSet failed: %v", err)
	}
	return snap
}

func rejectKinds(t *testing.T, err error) map[pluginhost.LoadErrorKind]int {
	t.Helper()
	var le pluginhost.LoadErrors
	if !errors.As(err, &le) {
		t.Fatalf("expected LoadErrors, got %v", err)
	}
	kinds := map[pluginhost.LoadErrorKind]int{}
	for _, e := range le {
		kinds[e.Kind]++
	}
	return kinds
}

// ---- generation semantics ----

func TestGenerationMismatchRejected(t *testing.T) {
	h := host.New(host.WithSupport(host.Support{
		HostGeneration: pluginhost.Gen2,
		MinGeneration:  pluginhost.Gen2,
		MaxGeneration:  pluginhost.Gen2,
		Capabilities:   map[string]bool{"logging": true},
		Policy:         host.PolicyReject,
	}))
	defer h.Close()

	m := echoManifest(echoPath) // Gen1 module against Gen2-exact host
	_, err := h.LoadSet(context.Background(), []pluginhost.Manifest{m})
	kinds := rejectKinds(t, err)
	if kinds[pluginhost.ErrUnsupportedGen] == 0 {
		t.Fatalf("expected unsupported_generation rejection, got: %v", err)
	}
}

func TestModuleHostRangeRejected(t *testing.T) {
	h := host.New()
	defer h.Close()

	// Module requires host >= Gen3; default host is Gen2.
	m := echoManifest(echoPath)
	m.MinHostGeneration = pluginhost.Generation(3)
	_, err := h.LoadSet(context.Background(), []pluginhost.Manifest{m})
	kinds := rejectKinds(t, err)
	if kinds[pluginhost.ErrGenerationRange] == 0 {
		t.Fatalf("expected generation_range rejection, got: %v", err)
	}
}

func TestMixedGenerationsCoexist(t *testing.T) {
	h := host.New()
	defer h.Close()

	snap := loadOrFail(t, h, []pluginhost.Manifest{echoManifest(echoPath), gen2Manifest(gen2Path)})
	if len(snap.Modules) != 2 {
		t.Fatalf("expected 2 modules across generations, got %d", len(snap.Modules))
	}
	c, cancel := ctx()
	defer cancel()

	// Gen1 semantics: lenient arity (missing arg -> zero value).
	v1, err := h.Call(c, "echo", "add", 21)
	if err != nil {
		t.Fatalf("gen1 add with missing arg: %v", err)
	}
	if v1.(int) != 21 {
		t.Fatalf("gen1 lenient add = %v, want 21 (second arg defaults to 0)", v1)
	}

	// Gen1 normal call.
	v2, err := h.Call(c, "echo", "add", 20, 22)
	if err != nil || v2.(int) != 42 {
		t.Fatalf("gen1 add = (%v,%v)", v2, err)
	}

	// Gen2 semantics: strict arity must reject.
	_, err = h.Call(c, "gen2echo", "echo")
	var ce *pluginhost.CallError
	if !errors.As(err, &ce) || ce.Reason == "" {
		t.Fatalf("expected Gen2 arity rejection, got %v", err)
	}

	// Gen2 normal call has its own semantic marker.
	v3, err := h.Call(c, "gen2echo", "echo", "hi")
	if err != nil || v3 != "gen2:hi" {
		t.Fatalf("gen2 echo = (%v,%v)", v3, err)
	}
}

// ---- entry validation ----

func TestEntryMissingInManifestRejected(t *testing.T) {
	h := host.New()
	defer h.Close()

	m := echoManifest(echoPath)
	m.Entries = append(m.Entries, pluginhost.EntrySpec{Name: "ghost", Output: pluginhost.KindString})
	_, err := h.LoadSet(context.Background(), []pluginhost.Manifest{m})
	kinds := rejectKinds(t, err)
	if kinds[pluginhost.ErrEntryMissing] == 0 {
		t.Fatalf("expected entry_missing, got: %v", err)
	}
}

func TestEntrySignatureMismatchRejected(t *testing.T) {
	h := host.New()
	defer h.Close()

	// Manifest claims echo returns int; artifact really returns string.
	m := echoManifest(echoPath)
	m.Entries[0].Output = pluginhost.KindInt
	_, err := h.LoadSet(context.Background(), []pluginhost.Manifest{m})
	kinds := rejectKinds(t, err)
	if kinds[pluginhost.ErrEntryTypeMismatch] == 0 {
		t.Fatalf("expected entry_type_mismatch, got: %v", err)
	}
}

func TestDuplicateEntryInManifestRejected(t *testing.T) {
	m := echoManifest(echoPath)
	m.Entries = append(m.Entries, pluginhost.EntrySpec{
		Name: "echo", Input: []pluginhost.Kind{pluginhost.KindString}, Output: pluginhost.KindString,
	})
	errs := pluginhost.ValidateManifest(m)
	found := false
	for _, e := range errs {
		if e.Kind == pluginhost.ErrEntryDuplicate && e.Module == "echo" && e.Entry == "echo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate entry error located at echo#echo, got %v", errs)
	}
}

func TestDuplicateInstanceRejectedAndIdempotentSet(t *testing.T) {
	h := host.New()
	defer h.Close()
	loadOrFail(t, h, []pluginhost.Manifest{echoManifest(echoPath)})

	// Re-loading the same identity+generation must not create a duplicate.
	_, err := h.LoadSet(context.Background(), []pluginhost.Manifest{echoManifest(echoPath)})
	kinds := rejectKinds(t, err)
	if kinds[pluginhost.ErrDuplicateInstance] == 0 {
		t.Fatalf("expected duplicate_instance rejection, got: %v", err)
	}
}

func TestCapabilityMissingRejected(t *testing.T) {
	h := host.New(host.WithSupport(host.Support{
		HostGeneration: pluginhost.Gen2,
		MinGeneration:  pluginhost.Gen1,
		MaxGeneration:  pluginhost.Gen2,
		Capabilities:   map[string]bool{},
		Policy:         host.PolicyAllowLegacy,
	}))
	defer h.Close()

	_, err := h.LoadSet(context.Background(), []pluginhost.Manifest{gen2Manifest(gen2Path)})
	kinds := rejectKinds(t, err)
	if kinds[pluginhost.ErrCapabilityMissing] == 0 {
		t.Fatalf("expected capability_missing, got: %v", err)
	}
}

// ---- fault isolation ----

func TestPanicIsolationDoesNotAffectHostOrOthers(t *testing.T) {
	h := host.New()
	defer h.Close()
	loadOrFail(t, h, []pluginhost.Manifest{
		echoManifest(echoPath),
		panicManifest(panicPath),
	})
	c, cancel := ctx()
	defer cancel()

	// Three consecutive module failures isolate panicmod.
	for i := 0; i < host.FailureThreshold; i++ {
		if _, err := h.Call(c, "panicmod", "boom"); err == nil {
			t.Fatalf("boom #%d should error", i)
		}
		// The healthy module keeps serving throughout.
		v, err := h.Call(c, "echo", "echo", "still-alive")
		if err != nil || v != "still-alive" {
			t.Fatalf("healthy module impacted at failure #%d: %v %v", i, v, err)
		}
	}

	// Isolated module refuses calls with a queryable reason.
	_, err := h.Call(c, "panicmod", "ok")
	var iso *pluginhost.IsolatedError
	if !errors.As(err, &iso) {
		t.Fatalf("expected IsolatedError, got %v", err)
	}
	if iso.Module != "panicmod" || iso.Reason == "" {
		t.Fatalf("isolated error missing module/reason: %+v", iso)
	}

	var info host.ModuleInfo
	for _, mi := range h.Query() {
		if mi.Manifest.ModuleID == "panicmod" {
			info = mi
		}
	}
	if info.State != pluginhost.StateIsolated {
		t.Fatalf("state = %s, want isolated", info.State)
	}
	if info.Failure.Count != host.FailureThreshold || info.Failure.LastAt == "" || info.Failure.Recovery == "" {
		t.Fatalf("failure info incomplete: %+v", info.Failure)
	}

	// Recovery restores service via a fresh process handshake.
	if err := h.Recover(c, "panicmod"); err != nil {
		t.Fatalf("recover: %v", err)
	}
	v, err := h.Call(c, "panicmod", "ok")
	if err != nil || v != "fine" {
		t.Fatalf("after recover: %v %v", v, err)
	}
}

func TestCrashCountedAndRecovered(t *testing.T) {
	h := host.New()
	defer h.Close()
	loadOrFail(t, h, []pluginhost.Manifest{panicManifest(panicPath)})
	c, cancel := ctx()
	defer cancel()

	for i := 0; i < host.FailureThreshold; i++ {
		if _, err := h.Call(c, "panicmod", "crash"); err == nil {
			t.Fatalf("crash #%d should error", i)
		}
	}
	if _, err := h.Call(c, "panicmod", "ok"); err == nil {
		t.Fatal("module should be isolated after crashes")
	}
	if err := h.Recover(c, "panicmod"); err != nil {
		t.Fatalf("recover after crashes: %v", err)
	}
	v, err := h.Call(c, "panicmod", "ok")
	if err != nil || v != "fine" {
		t.Fatalf("post-recover call: %v %v", v, err)
	}
}

func TestSuccessfulCallResetsFailureCount(t *testing.T) {
	h := host.New()
	defer h.Close()
	loadOrFail(t, h, []pluginhost.Manifest{panicManifest(panicPath)})
	c, cancel := ctx()
	defer cancel()

	for i := 0; i < host.FailureThreshold-1; i++ {
		_, _ = h.Call(c, "panicmod", "boom")
	}
	// A success resets the consecutive counter; the module never isolates.
	if _, err := h.Call(c, "panicmod", "ok"); err != nil {
		t.Fatalf("ok call: %v", err)
	}
	for i := 0; i < host.FailureThreshold-1; i++ {
		_, _ = h.Call(c, "panicmod", "boom")
	}
	for _, mi := range h.Query() {
		if mi.Manifest.ModuleID == "panicmod" && mi.State != pluginhost.StateHealthy {
			t.Fatalf("module isolated despite success resetting counter: %+v", mi)
		}
	}
}

// ---- concurrency ----

func TestConcurrentCallsAreSafe(t *testing.T) {
	h := host.New()
	defer h.Close()
	loadOrFail(t, h, []pluginhost.Manifest{echoManifest(echoPath), gen2Manifest(gen2Path)})
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := h.Call(c, "echo", "echo", "x")
			if err != nil || v != "x" {
				t.Errorf("gen1 call %d: %v %v", i, v, err)
			}
			if i%2 == 0 {
				v, err := h.Call(c, "gen2echo", "echo", "y")
				if err != nil || v != "gen2:y" {
					t.Errorf("gen2 call %d: %v %v", i, v, err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentLoadsAreSerializedAndNeverMix(t *testing.T) {
	h := host.New()
	defer h.Close()
	c, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	gen2m := gen2Manifest(gen2Path)
	var wg sync.WaitGroup
	var success, rejected int64
	var mu sync.Mutex
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(first bool) {
			defer wg.Done()
			_, err := h.LoadSet(c, []pluginhost.Manifest{gen2m})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				rejected++
			} else {
				success++
			}
		}(i == 0)
	}
	wg.Wait()

	if success != 1 || rejected != 5 {
		t.Fatalf("expected exactly 1 commit and 5 duplicate rejections, got success=%d rejected=%d", success, rejected)
	}
	snap := h.Current()
	if len(snap.Modules) != 1 {
		t.Fatalf("snapshot mixed: %d modules", len(snap.Modules))
	}
}

func TestFailedLoadKeepsPreviousSetIntact(t *testing.T) {
	h := host.New()
	defer h.Close()
	loadOrFail(t, h, []pluginhost.Manifest{echoManifest(echoPath)})
	c, cancel := ctx()
	defer cancel()

	// Attempt a set containing an unstartable artifact; old set must survive.
	bad := echoManifest(echoPath)
	bad.ModuleID = "broken"
	bad.ArtifactPath = filepath.Join(binDir, "does-not-exist")
	_, err := h.LoadSet(c, []pluginhost.Manifest{bad})
	if err == nil {
		t.Fatal("expected load failure for missing artifact")
	}
	v, err := h.Call(c, "echo", "echo", "survived")
	if err != nil || v != "survived" {
		t.Fatalf("previous set not intact after failed load: %v %v", v, err)
	}
}

// ---- restart recovery ----

func TestRestartRestoresLastCommittedSet(t *testing.T) {
	dir := t.TempDir()
	store := host.NewFileStore(dir)

	c, cancel := ctx()
	defer cancel()

	h1, err := host.Open(c, host.WithPersistence(store))
	if err != nil {
		t.Fatalf("open empty: %v", err)
	}
	loadOrFail(t, h1, []pluginhost.Manifest{echoManifest(echoPath), gen2Manifest(gen2Path)})
	v, err := h1.Call(c, "echo", "echo", "v1")
	if err != nil || v != "v1" {
		t.Fatalf("call before restart: %v %v", v, err)
	}
	_ = h1.Close()

	// Simulate process restart with the same durable state.
	h2, err := host.Open(c, host.WithPersistence(store))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer h2.Close()
	snap := h2.Current()
	if len(snap.Modules) != 2 {
		t.Fatalf("expected full set of 2 after restart, got %d", len(snap.Modules))
	}
	v, err = h2.Call(c, "echo", "add", 40, 2)
	if err != nil || v.(int) != 42 {
		t.Fatalf("gen1 call after restart: %v %v", v, err)
	}
	v, err = h2.Call(c, "gen2echo", "echo", "z")
	if err != nil || v != "gen2:z" {
		t.Fatalf("gen2 call after restart: %v %v", v, err)
	}
}

func TestRejectedTransactionIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	store := host.NewFileStore(dir)
	c, cancel := ctx()
	defer cancel()

	h1, _ := host.Open(c, host.WithPersistence(store))
	loadOrFail(t, h1, []pluginhost.Manifest{echoManifest(echoPath)})
	_ = h1.Close()

	h2, _ := host.Open(c, host.WithPersistence(store))
	defer h2.Close()
	bad := gen2Manifest(gen2Path)
	bad.Capabilities = []string{"does-not-exist"}
	if _, err := h2.LoadSet(c, []pluginhost.Manifest{bad}); err == nil {
		t.Fatal("expected capability rejection")
	}
	_ = h2.Close()

	// Restart must reuse the last *committed* set, not the rejected one.
	h3, err := host.Open(c, host.WithPersistence(store))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer h3.Close()
	if len(h3.Current().Modules) != 1 {
		t.Fatalf("rejected transaction leaked into persistence: %v", h3.Query())
	}
}

// ---- determinism ----

func TestValidationDeterministic(t *testing.T) {
	m := echoManifest(echoPath)
	m.Entries = append(m.Entries,
		pluginhost.EntrySpec{Name: "echo", Output: pluginhost.KindString},
		pluginhost.EntrySpec{Name: "", Output: pluginhost.Kind("weird")},
	)
	first := pluginhost.ValidateManifest(m)
	for i := 0; i < 10; i++ {
		again := pluginhost.ValidateManifest(m)
		if len(first) != len(again) {
			t.Fatalf("error count unstable: %d vs %d", len(first), len(again))
		}
		for j := range first {
			if first[j].Error() != again[j].Error() {
				t.Fatalf("non-deterministic validation: %q vs %q", first[j], again[j])
			}
		}
	}
}
