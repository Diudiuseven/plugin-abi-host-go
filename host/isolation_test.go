package host

import (
	"errors"
	"testing"
)

// A panic while resolving the module is a pre-load rejection; the host
// process survives and keeps its current set.
func TestLoadTimePanicRejected(t *testing.T) {
	h := testHost(t, "log")
	if _, err := h.Load(buildPlugin(t, "g1ok")); err != nil {
		t.Fatal(err)
	}
	_, err := h.Load(buildPlugin(t, "g1loadpanic"))
	if err == nil || codeOf(t, err) != ErrModulePanic {
		t.Fatalf("want module_panic rejection, got %v", err)
	}
	// host and the healthy module are unaffected.
	if _, err := h.Call("g1", 1, "Echo", "alive"); err != nil {
		t.Fatalf("healthy module affected by load panic: %v", err)
	}
}

// Panic inside an entry is converted to an error, charged as a failure,
// and after threshold failures the module is quarantined and remains
// queryable with reason and a recovery hint. Other modules stay healthy.
func TestCallPanicQuarantinesAndIsolates(t *testing.T) {
	h := testHost(t, "log")
	if _, err := h.Load(buildPlugin(t, "g1ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(buildPlugin(t, "g1callpanic")); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		if _, err := h.Call("panicmod", 1, "Boom", "x"); err == nil {
			t.Fatalf("panic %d did not surface as error", i)
		}
	}

	st, err := h.Module("panicmod", 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusQuarantined {
		t.Fatalf("want quarantined, got %s", st.Status)
	}
	if st.Failures != 3 || st.LastError == "" || st.RecoveryHint == "" {
		t.Fatalf("quarantine report incomplete: %+v", st)
	}

	// Quarantined calls are refused fast.
	if _, err := h.Call("panicmod", 1, "Boom", "x"); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("want ErrQuarantined, got %v", err)
	}
	// Other entries of the same module are also refused, but the host and
	// unrelated modules are fully unaffected.
	if _, err := h.Call("panicmod", 1, "Echo", "x"); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("healthy entry of quarantined module should be refused: %v", err)
	}
	if _, err := h.Call("g1", 1, "Echo", "fine"); err != nil {
		t.Fatalf("other module affected by quarantine: %v", err)
	}

	// Recovery returns the module to service and resets counters.
	if err := h.Recover("panicmod", 1); err != nil {
		t.Fatal(err)
	}
	st, _ = h.Module("panicmod", 1)
	if st.Status != StatusHealthy || st.Failures != 0 {
		t.Fatalf("recover did not reset state: %+v", st)
	}
	out, err := h.Call("panicmod", 1, "Echo", "back")
	if err != nil || out != "ok:back" {
		t.Fatalf("module did not resume: %v %v", out, err)
	}
}

// Returned errors (not just panics) count toward the threshold.
func TestCallErrorQuarantines(t *testing.T) {
	h := testHost(t)
	if _, err := h.Load(buildPlugin(t, "g1callerr")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := h.Call("errmod", 2, "Fail", map[string]any{}); err == nil {
			t.Fatal("entry error was swallowed")
		}
	}
	st, _ := h.Module("errmod", 2)
	if st.Status != StatusQuarantined {
		t.Fatalf("entry errors did not quarantine: %+v", st)
	}
}

// Caller-side mistakes (unknown entry, wrong argument generation type) are
// never charged to the module.
func TestCallerMistakesDoNotCount(t *testing.T) {
	h := testHost(t, "log", "metrics")
	if _, err := h.Load(buildPlugin(t, "g2ok")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := h.Call("g2", 2, "Nope", map[string]any{}); !errors.Is(err, ErrUnknownEntry) {
			t.Fatalf("want unknown entry, got %v", err)
		}
		if _, err := h.Call("g2", 2, "Process", 123); err == nil {
			t.Fatal("wrong arg type accepted")
		}
	}
	st, _ := h.Module("g2", 2)
	if st.Status != StatusHealthy || st.Failures != 0 {
		t.Fatalf("caller mistakes charged to module: %+v", st)
	}
}

// A panic during the module package's init runs inside plugin.Open and
// must also be contained as a pre-load rejection.
func TestInitPanicRejected(t *testing.T) {
	h := testHost(t)
	_, err := h.Load(buildPlugin(t, "g1initpanic"))
	if err == nil || codeOf(t, err) != ErrModulePanic {
		t.Fatalf("want module_panic for init panic, got %v", err)
	}
}
