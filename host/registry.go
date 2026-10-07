package host

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

// Config configures a Host.
type Config struct {
	// Capabilities is the set of host capabilities modules may require.
	Capabilities []string
	// FailureThreshold quarantines a module after this many failures.
	// Zero defaults to 3.
	FailureThreshold int
	// StatePath, when set, persists the committed module set so a restart
	// either fully reuses the last committed set or starts empty.
	StatePath string
}

// Host is the capability host. Reads (query/call) operate on an immutable
// snapshot and never block loads; loads are serialized and only become
// visible as one fully committed set.
type Host struct {
	mu        sync.Mutex // serializes load/recover-commit and snapshot swap
	snapMu    sync.RWMutex
	snapshot  map[string]*module // immutable after publication; key identity(id, gen)
	caps      map[string]bool
	threshold int
	statePath string
}

// New creates a host. When cfg.StatePath is set, the last committed module
// set is fully replayed before New returns; replay is all-or-nothing.
func New(cfg Config) (*Host, error) {
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	caps := make(map[string]bool, len(cfg.Capabilities))
	for _, c := range cfg.Capabilities {
		caps[c] = true
	}
	h := &Host{
		snapshot:  map[string]*module{},
		caps:      caps,
		threshold: threshold,
		statePath: cfg.StatePath,
	}
	if cfg.StatePath != "" {
		if err := h.restore(); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Load validates and installs one module, committing a new complete set
// atomically. All validation happens before the new set becomes visible;
// on any error the previously committed set (and the persisted state)
// stays fully in effect.
//
// Loading the same identity (ID + generation) from an identical artifact
// is idempotent and returns the existing instance. The same identity from
// a different artifact is rejected so no duplicate or silently replaced
// instance can exist.
func (h *Host) Load(path string) (*Manifest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	checksum, err := sha256File(path)
	if err != nil {
		return nil, &LoadError{Code: ErrMalformedManifest, Detail: "cannot read artifact: " + err.Error()}
	}

	art, err := loadArtifact(path, h.caps)
	if err != nil {
		return nil, err
	}
	key := identity(art.manifest.ID, art.manifest.Generation)
	if existing := h.snapshot[key]; existing != nil {
		if existing.path == path && checksum == checksumOf(existing) {
			return existing.manifest, nil // identical reload: no duplicate instance
		}
		return nil, &LoadError{Code: ErrDuplicateExisting, ModuleID: art.manifest.ID,
			Detail: fmt.Sprintf("generation %d is already loaded from a different artifact", art.manifest.Generation)}
	}

	mod := &module{
		manifest:  art.manifest,
		path:      path,
		entries:   art.entries,
		threshold: h.threshold,
		status:    StatusHealthy,
	}
	mod.checksum = checksum

	// Build the candidate set without touching the published one.
	next := make(map[string]*module, len(h.snapshot)+1)
	for k, v := range h.snapshot {
		next[k] = v
	}
	next[key] = mod

	// Persist before publication. A failed persistence aborts the commit,
	// so no half-loaded state is ever observable or restartable.
	if err := h.persist(snapshotEntries(next)); err != nil {
		return nil, err
	}
	h.publish(next)
	return art.manifest, nil
}

// Call invokes a declared entry of module (id, generation). The module
// receives generation-correct argument semantics.
func (h *Host) Call(id string, generation int, entry string, arg any) (any, error) {
	mod := h.lookup(identity(id, generation))
	if mod == nil {
		return nil, fmt.Errorf("%w: %s generation %d", ErrNoSuchModule, id, generation)
	}
	return mod.call(entry, arg)
}

// Recover moves a quarantined module back into service and clears its
// failure counters.
func (h *Host) Recover(id string, generation int) error {
	mod := h.lookup(identity(id, generation))
	if mod == nil {
		return fmt.Errorf("%w: %s generation %d", ErrNoSuchModule, id, generation)
	}
	mod.recover()
	return nil
}

// Modules reports the committed set as a point-in-time snapshot. The
// returned slice is a private copy and is safe to use concurrently.
func (h *Host) Modules() []StatusReport {
	h.snapMu.RLock()
	mods := make([]*module, 0, len(h.snapshot))
	for _, m := range h.snapshot {
		mods = append(mods, m)
	}
	h.snapMu.RUnlock()
	sort.Slice(mods, func(i, j int) bool {
		return identity(mods[i].manifest.ID, mods[i].manifest.Generation) <
			identity(mods[j].manifest.ID, mods[j].manifest.Generation)
	})
	out := make([]StatusReport, len(mods))
	for i, m := range mods {
		out[i] = m.report()
	}
	return out
}

// Module reports one module's status, including isolation state, the last
// failure reason and the recovery method.
func (h *Host) Module(id string, generation int) (StatusReport, error) {
	mod := h.lookup(identity(id, generation))
	if mod == nil {
		return StatusReport{}, fmt.Errorf("%w: %s generation %d", ErrNoSuchModule, id, generation)
	}
	return mod.report(), nil
}

func (h *Host) lookup(key string) *module {
	h.snapMu.RLock()
	defer h.snapMu.RUnlock()
	return h.snapshot[key]
}

// publish swaps the committed set. The argument map is treated as
// immutable afterwards, which is what gives readers a consistent full set.
func (h *Host) publish(next map[string]*module) {
	h.snapMu.Lock()
	h.snapshot = next
	h.snapMu.Unlock()
}

func identity(id string, gen int) string {
	return fmt.Sprintf("%s#gen=%d", id, gen)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
