package host

import (
	"errors"
	"testing"
)

func testHost(t *testing.T, caps ...string) *Host {
	t.Helper()
	h, err := New(Config{Capabilities: caps, FailureThreshold: 3})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	return h
}

func TestLoadAndCallGen1(t *testing.T) {
	h := testHost(t, "log")
	so := buildPlugin(t, "g1ok")
	m, err := h.Load(so)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.ID != "g1" || m.Generation != 1 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	out, err := h.Call("g1", 1, "Echo", "hello")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != "g1:hello" {
		t.Fatalf("unexpected output %v", out)
	}
}

func TestLoadAndCallGen2(t *testing.T) {
	h := testHost(t, "log", "metrics")
	so := buildPlugin(t, "g2ok")
	if _, err := h.Load(so); err != nil {
		t.Fatalf("load: %v", err)
	}
	out, err := h.Call("g2", 2, "Process", map[string]any{"x": "abc"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	m := out.(map[string]any)
	if m["echo"] != "abc" || m["gen"] != 2 {
		t.Fatalf("unexpected gen2 output %#v", m)
	}
}

// Two generations with the SAME module id coexist in one process and each
// keeps its own generation semantics; neither symbol table pollutes the
// other.
func TestMultipleGenerationsCoexist(t *testing.T) {
	h := testHost(t, "log", "metrics")
	g1 := buildPlugin(t, "g1ok")
	g2 := buildPlugin(t, "g2ok")
	if _, err := h.Load(g1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(g2); err != nil {
		t.Fatal(err)
	}
	mods := h.Modules()
	if len(mods) != 2 {
		t.Fatalf("want 2 committed modules, got %d", len(mods))
	}
	// gen1 semantics: string in/out.
	if _, err := h.Call("g1", 1, "Echo", "z"); err != nil {
		t.Fatalf("g1: %v", err)
	}
	// gen2 semantics: map in/out. A string argument must be refused by the
	// gen2 adapter even though the same host serves a gen1 string entry.
	if _, err := h.Call("g2", 2, "Process", "not-a-map"); err == nil {
		t.Fatal("gen2 entry accepted a string argument")
	}
	// g1's Echo symbol is not an entry of g2 (no symbol cross-pollution).
	if _, err := h.Call("g2", 2, "Echo", "z"); !errors.Is(err, ErrUnknownEntry) {
		t.Fatalf("want ErrUnknownEntry, got %v", err)
	}
}

// Same id across two generations must not share package-global state:
// each compiled module keeps its own "calls" counter.
func TestGenerationsDoNotShareGlobalState(t *testing.T) {
	h := testHost(t)
	if _, err := h.Load(buildPlugin(t, "g1state")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(buildPlugin(t, "g2state")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := h.Call("stateful", 1, "Count", "x"); err != nil {
			t.Fatal(err)
		}
	}
	// gen2 never called: its private counter must still report 1 on first
	// call, unaffected by the three gen1 calls.
	out, err := h.Call("stateful", 2, "Count", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["calls"] != 1 {
		t.Fatalf("global state leaked across generations: %#v", out)
	}
	// gen1 counter is independently at 4.
	if v, _ := h.Call("stateful", 1, "Count", "x"); v != "g1:4" {
		t.Fatalf("gen1 counter wrong: %v", v)
	}
}
