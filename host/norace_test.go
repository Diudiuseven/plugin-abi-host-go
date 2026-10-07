//go:build !race

package host

// raceDetectorEnabled reports whether the current test binary was built
// with -race. Detection is split across build-tagged files because the
// runtime does not expose it and -race adds no process argument.
func raceDetectorEnabled() bool { return false }
