package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise a genuine process restart by re-executing the test
// binary with an environment switch. That matters for Go plugins: a true
// restart re-opens artifacts from disk rather than hitting the in-process
// plugin registry.

const (
	envRestartCase  = "HOST_RESTART_CASE"
	envRestartDir   = "HOST_RESTART_DIR"
	envRestartArt   = "HOST_RESTART_ART"
	envRestartState = "HOST_RESTART_STATE"
)

// restartSubprocess runs one phase ("load" or "restart") in a fresh test
// process. The phase code lives in TestRestartSubprocessDriver.
func restartSubprocess(t *testing.T, phase string) string {
	t.Helper()
	args := []string{"-test.run=^TestRestartSubprocessDriver$", "-test.v"}
	if testing.Short() {
		args = append(args, "-test.short")
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), envRestartCase+"="+phase)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess %s failed: %v\n%s", phase, err, b)
	}
	return string(b)
}

// Driver used only inside re-executed test processes.
func TestRestartSubprocessDriver(t *testing.T) {
	phase := os.Getenv(envRestartCase)
	if phase == "" {
		return
	}
	dir := os.Getenv(envRestartDir)
	art := os.Getenv(envRestartArt)
	state := os.Getenv(envRestartState)
	caps := []string{"log"}

	switch phase {
	case "load":
		h, err := New(Config{Capabilities: caps, StatePath: state})
		if err != nil {
			t.Fatalf("load new: %v", err)
		}
		if _, err := h.Load(art); err != nil {
			t.Fatalf("load artifact: %v", err)
		}
		// Second module to prove the whole set, not one module, is restored.
		art2 := filepath.Join(dir, "m2.so")
		copyFile(t, buildPlugin(t, "g1restart2"), art2)
		if _, err := h.Load(art2); err != nil {
			t.Fatalf("load second: %v", err)
		}
	case "restart":
		h, err := New(Config{Capabilities: caps, StatePath: state})
		if err != nil {
			t.Fatalf("RESTART_ERROR: %v", err)
		}
		mods := h.Modules()
		if len(mods) != 2 {
			t.Fatalf("RESTART_ERROR: restored %d modules, want 2", len(mods))
		}
		out, err := h.Call("g1restart", 1, "Echo", "x")
		if err != nil || out != "g1restart:x" {
			t.Fatalf("RESTART_ERROR call g1restart: %v %v", out, err)
		}
		out, err = h.Call("g1restart2", 1, "Echo", "y")
		if err != nil || out != "g1restart2:y" {
			t.Fatalf("RESTART_ERROR call g1restart2: %v %v", out, err)
		}
		t.Log("RESTART_OK")
	default:
		t.Fatalf("unknown phase %q", phase)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Requirement: after restart the host fully reuses the last committed set.
func TestRestartReusesCommittedSet(t *testing.T) {
	dir := t.TempDir()
	art := filepath.Join(dir, "m1.so")
	state := filepath.Join(dir, "state.json")
	copyFile(t, buildPlugin(t, "g1restart"), art)

	t.Setenv(envRestartDir, dir)
	t.Setenv(envRestartArt, art)
	t.Setenv(envRestartState, state)
	restartSubprocess(t, "load")
	out := restartSubprocess(t, "restart")
	if !strings.Contains(out, "RESTART_OK") {
		t.Fatalf("restart phase did not confirm: %s", out)
	}
}

// A tampered artifact fails the entire replay: the new process refuses to
// start rather than mixing changed and unchanged modules.
func TestRestartRejectsTamperedArtifact(t *testing.T) {
	dir := t.TempDir()
	art := filepath.Join(dir, "m1.so")
	state := filepath.Join(dir, "state.json")
	copyFile(t, buildPlugin(t, "g1restart"), art)

	t.Setenv(envRestartDir, dir)
	t.Setenv(envRestartArt, art)
	t.Setenv(envRestartState, state)
	restartSubprocess(t, "load")

	b, err := os.ReadFile(art)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(art, append(b, 0x00), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestartSubprocessDriver$", "-test.v")
	cmd.Env = append(os.Environ(), envRestartCase+"=restart", envRestartDir+"="+dir,
		envRestartArt+"="+art, envRestartState+"="+state)
	outB, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("restart with tampered artifact succeeded: %s", outB)
	}
	if !strings.Contains(string(outB), "state_replay_failed") {
		t.Fatalf("want replay rejection, got: %s", outB)
	}
}

// A missing artifact fails the whole replay too.
func TestRestartRejectsMissingArtifact(t *testing.T) {
	dir := t.TempDir()
	art := filepath.Join(dir, "m1.so")
	state := filepath.Join(dir, "state.json")
	copyFile(t, buildPlugin(t, "g1restart3"), art)

	t.Setenv(envRestartDir, dir)
	t.Setenv(envRestartArt, art)
	t.Setenv(envRestartState, state)
	restartSubprocess(t, "load")
	if err := os.Remove(art); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestartSubprocessDriver$", "-test.v")
	cmd.Env = append(os.Environ(), envRestartCase+"=restart", envRestartDir+"="+dir,
		envRestartArt+"="+art, envRestartState+"="+state)
	outB, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("restart with missing artifact succeeded: %s", outB)
	}
	if !strings.Contains(string(outB), "state_replay_failed") {
		t.Fatalf("want replay rejection, got: %s", outB)
	}
}

// Failed commits leave the previous persisted set byte-for-byte intact and
// never leak temp files.
func TestStateFileIsAtomic(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	h, err := New(Config{Capabilities: []string{"log"}, StatePath: state})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(buildPlugin(t, "g1ok")); err != nil {
		t.Fatal(err)
	}
	for _, e := range mustReadDir(t, dir) {
		if strings.HasPrefix(e.Name(), ".host-state-") {
			t.Fatalf("temp state file leaked: %s", e.Name())
		}
	}
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(buildPlugin(t, "g1gen9")); err == nil {
		t.Fatal("expected gen9 rejection")
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("persisted set changed after a failed load")
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return es
}
