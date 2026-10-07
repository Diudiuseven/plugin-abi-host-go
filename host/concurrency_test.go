package host

import (
	"errors"
	"sync"
	"testing"
)

// Repeated loading of the same identity (id + generation) from the same
// artifact is idempotent and never creates a second instance.
func TestDuplicateLoadIsIdempotent(t *testing.T) {
	h := testHost(t, "log", "metrics")
	so := buildPlugin(t, "g1ok")
	for i := 0; i < 3; i++ {
		if _, err := h.Load(so); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}
	mods := h.Modules()
	if len(mods) != 1 {
		t.Fatalf("duplicate instances created: %d", len(mods))
	}
}

// Concurrent loads, queries and calls must never expose a mixed set. While
// loads are in flight every observed snapshot is either an old complete set
// or a new complete set, and every module reported in a snapshot works.
func TestConcurrentLoadQueryCall(t *testing.T) {
	h := testHost(t, "log", "metrics")
	g1 := buildPlugin(t, "g1ok")
	g2 := buildPlugin(t, "g2ok")

	var wg sync.WaitGroup
	// Concurrent repeated loads of two different generations.
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = h.Load(g1) }()
		go func() { defer wg.Done(); _, _ = h.Load(g2) }()
	}
	// Concurrent readers validate every observed snapshot is internally
	// consistent: reported entries are exactly callable at that instant.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				for _, r := range h.Modules() {
					// Snapshot consistency: every module reported in a
					// snapshot must answer its declared entry with the
					// generation-correct semantics at that instant. Loads
					// only add modules here, so the only allowed error is
					// the module having vanished between read and call.
					var arg any = "x"
					if r.Generation == 2 {
						arg = map[string]any{"x": "v"}
					}
					if _, err := h.Call(r.ID, r.Generation, r.Entries[0], arg); err != nil {
						if !errors.Is(err, ErrNoSuchModule) {
							t.Errorf("inconsistent snapshot for %s gen%d: %v", r.ID, r.Generation, err)
						}
					}
				}
			}
		}()
	}
	wg.Wait()

	mods := h.Modules()
	if len(mods) != 2 {
		t.Fatalf("want exactly 2 committed modules, got %d", len(mods))
	}
	if _, err := h.Call("g1", 1, "Echo", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call("g2", 2, "Process", map[string]any{"x": "done"}); err != nil {
		t.Fatal(err)
	}
}

// Concurrent calls against one module must keep failure accounting exact.
func TestConcurrentCallsExactFailureCount(t *testing.T) {
	h2, err := New(Config{FailureThreshold: 50})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h2.Load(buildPlugin(t, "g1callerr")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = h2.Call("errmod", 2, "Fail", map[string]any{}) }()
	}
	wg.Wait()
	st, _ := h2.Module("errmod", 2)
	if st.Failures != 50 {
		t.Fatalf("lost failures under concurrency: %d", st.Failures)
	}
}

// The same identity (id + generation) from a different artifact is a
// conflict: the host keeps the committed instance and rejects the new one.
func TestSameIdentityDifferentArtifactRejected(t *testing.T) {
	h := testHost(t, "log")
	if _, err := h.Load(buildPlugin(t, "g1ok")); err != nil {
		t.Fatal(err)
	}
	_, err := h.Load(buildPlugin(t, "g1dupconflict"))
	if err == nil {
		t.Fatal("conflicting artifact replaced the committed module")
	}
	var le *LoadError
	if !errors.As(err, &le) || le.Code != ErrDuplicateExisting {
		t.Fatalf("want duplicate_existing rejection, got %v", err)
	}
	// The original semantics are still intact.
	out, err := h.Call("g1", 1, "Echo", "x")
	if err != nil || out != "g1:x" {
		t.Fatalf("committed module was disturbed: %v %v", out, err)
	}
	if len(h.Modules()) != 1 {
		t.Fatalf("a second instance slipped in: %d", len(h.Modules()))
	}
}
