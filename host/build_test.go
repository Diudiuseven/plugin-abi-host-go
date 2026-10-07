package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// pluginsModule is the directory containing the per-fixture plugin modules.
func pluginsModule(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "plugins")
}

// Go identifies a loaded plugin in a process by its package path, so a
// given compiled artifact can only be opened once per process. Tests share
// one built .so per fixture (built lazily, exactly once) — which also
// mirrors real deployment where one artifact file is reused.
var (
	buildDirOnce sync.Once
	buildDir     string
	raceOn       bool
	builtPlugins sync.Map // fixture name -> *sync.Once
)

func sharedBuildDir(t *testing.T) string {
	t.Helper()
	buildDirOnce.Do(func() {
		d, err := os.MkdirTemp("", "host-plugins-")
		if err != nil {
			t.Fatalf("build dir: %v", err)
		}
		buildDir = d
		// Race-enabled hosts require race-enabled plugins (the runtime
		// package identity must match).
		raceOn = raceDetectorEnabled()
	})
	return buildDir
}

// buildPlugin returns the path to the fixture's shared compiled artifact.
func buildPlugin(t *testing.T, name string) string {
	t.Helper()
	dir := sharedBuildDir(t)
	suffix := ""
	if raceOn {
		suffix = "-race"
	}
	out := filepath.Join(dir, name+suffix+".so")
	onceI, _ := builtPlugins.LoadOrStore(name, &sync.Once{})
	once := onceI.(*sync.Once)
	once.Do(func() {
		cmd := exec.Command("go", "build", "-buildmode=plugin", "-buildvcs=false",
			"-o", out, ".")
		cmd.Dir = filepath.Join(pluginsModule(t), name)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build plugin %s: %v\n%s", name, err, b)
		}
	})
	return out
}

func TestPluginsModuleExists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(pluginsModule(t), "g1ok", "go.mod")); err != nil {
		t.Fatal(err)
	}
}
