package host

import (
	"errors"
	"testing"
)

func codeOf(t *testing.T, err error) LoadErrorCode {
	t.Helper()
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("want *LoadError, got %T: %v", err, err)
	}
	return le.Code
}

func rejectCase(t *testing.T, fixture string, caps ...string) (*Host, error) {
	t.Helper()
	h := testHost(t, caps...)
	so := buildPlugin(t, fixture)
	_, err := h.Load(so)
	return h, err
}

func TestRejectMissingEntry(t *testing.T) {
	_, err := rejectCase(t, "g1missing")
	if err == nil {
		t.Fatal("module with undeclared-symbol entry was accepted")
	}
	var le *LoadError
	errors.As(err, &le)
	if le.Code != ErrMissingEntrySymbol || le.Entry != "Ghost" || le.ModuleID != "g1missing" {
		t.Fatalf("unexpected error: %+v", le)
	}
}

func TestRejectWrongEntryType(t *testing.T) {
	_, err := rejectCase(t, "g1wrongtype")
	var le *LoadError
	errors.As(err, &le)
	if le.Code != ErrEntryTypeMismatch || le.Entry != "Echo" || le.ModuleID != "g1wrongtype" {
		t.Fatalf("unexpected error: %+v", le)
	}
}

func TestRejectDuplicateEntry(t *testing.T) {
	_, err := rejectCase(t, "g1dupentry")
	if codeOf(t, err) != ErrDuplicateEntry {
		t.Fatalf("want duplicate entry, got %v", err)
	}
}

func TestRejectGenerationRange(t *testing.T) {
	_, err := rejectCase(t, "g1gen0")
	if codeOf(t, err) != ErrGenerationTooOld {
		t.Fatalf("want too-old, got %v", err)
	}
	_, err = rejectCase(t, "g1gen9")
	if codeOf(t, err) != ErrGenerationTooNew {
		t.Fatalf("want too-new, got %v", err)
	}
}

func TestRejectMissingCapability(t *testing.T) {
	if _, err := rejectCase(t, "g1needscap"); codeOf(t, err) != ErrMissingCapability {
		t.Fatalf("want missing capability, got %v", err)
	}
	// Same artifact, host providing the capability: accepted. The verdict
	// is a pure function of host capabilities + artifact.
	h := testHost(t, "teleport")
	if _, err := h.Load(buildPlugin(t, "g1needscap")); err != nil {
		t.Fatalf("capability satisfied but rejected: %v", err)
	}
}

func TestRejectBadManifestAndNoMetadata(t *testing.T) {
	if _, err := rejectCase(t, "g1badjson"); codeOf(t, err) != ErrMalformedManifest {
		t.Fatalf("want malformed manifest, got %v", err)
	}
	if _, err := rejectCase(t, "g1nometa"); codeOf(t, err) != ErrMissingEntrySymbol {
		t.Fatalf("want missing metadata symbol, got %v", err)
	}
}

// A rejected load leaves the previously committed set entirely intact
// (no half-loaded state at set or entry level).
func TestRejectedLoadKeepsOldSet(t *testing.T) {
	h := testHost(t, "log")
	if _, err := h.Load(buildPlugin(t, "g1ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(buildPlugin(t, "g1gen9")); err == nil {
		t.Fatal("gen9 accepted")
	}
	mods := h.Modules()
	if len(mods) != 1 || mods[0].ID != "g1" {
		t.Fatalf("old set mutated after rejection: %+v", mods)
	}
	if _, err := h.Call("g1", 1, "Echo", "still-here"); err != nil {
		t.Fatalf("existing module broke after rejection: %v", err)
	}
}

// The same input always yields the same verdict.
func TestVerdictIsReproducible(t *testing.T) {
	so := buildPlugin(t, "g1gen9")
	for i := 0; i < 3; i++ {
		h := testHost(t)
		_, err := h.Load(so)
		if codeOf(t, err) != ErrGenerationTooNew {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}
