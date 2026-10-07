package host

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
)

// Snapshot is one committed, immutable view of the whole module set. Readers
// (Call, Query, Current) always observe either the previous full set or the
// new full set — never a mix at set or entry level.
type Snapshot struct {
	SetID   uint64
	Modules map[string]*Module // key: module ID
}

// SetIDs returns the ordered module IDs of a snapshot.
func (s *Snapshot) SetIDs() []string {
	ids := make([]string, 0, len(s.Modules))
	for id := range s.Modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Registry holds the currently committed module set. All state changes go
// through commit; reads are lock-free against an atomic pointer.
type Registry struct {
	support Support

	current atomic.Pointer[Snapshot]

	// loadMu serializes load transactions so that concurrent LoadSet calls
	// cannot interleave staging artifacts. Queries and calls do not take it.
	loadMu  sync.Mutex
	nextSet uint64
}

// NewRegistry creates an empty registry.
func NewRegistry(support Support) *Registry {
	r := &Registry{support: support}
	r.current.Store(&Snapshot{SetID: 0, Modules: map[string]*Module{}})
	return r
}

// Current returns the latest committed snapshot (never nil).
func (r *Registry) Current() *Snapshot { return r.current.Load() }

// ModuleInfo is a serializable module status record.
type ModuleInfo struct {
	SetID    uint64
	Key      string
	Manifest pluginhost.Manifest
	State    pluginhost.ModuleState
	Failure  pluginhost.FailureInfo
}

// Query returns a point-in-time listing of modules and their states.
func (r *Registry) Query() []ModuleInfo {
	snap := r.Current()
	infos := make([]ModuleInfo, 0, len(snap.Modules))
	for _, mod := range snap.Modules {
		state, failure := mod.State()
		infos = append(infos, ModuleInfo{
			SetID:    snap.SetID,
			Key:      Key(mod.Manifest().ModuleID, mod.Manifest().Generation),
			Manifest: mod.Manifest(),
			State:    state,
			Failure:  failure,
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	return infos
}

// Find resolves a module ID (or "id@gen" qualified name) within the current
// snapshot.
func (r *Registry) Find(ref string) (*Module, bool) {
	snap := r.Current()
	if mod, ok := snap.Modules[ref]; ok {
		return mod, true
	}
	var matches []*Module
	for _, mod := range snap.Modules {
		m := mod.Manifest()
		if m.ModuleID == ref {
			matches = append(matches, mod)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return nil, false
}

// Validate performs the full deterministic pre-load validation of a
// transaction without touching any process.
func (r *Registry) Validate(manifests []pluginhost.Manifest) pluginhost.LoadErrors {
	var errs pluginhost.LoadErrors

	seenIDs := map[string]bool{}
	seenKeys := map[string]bool{}
	for i, m := range manifests {
		label := m.ModuleID
		if label == "" {
			label = fmt.Sprintf("manifests[%d]", i)
		}
		errs = append(errs, pluginhost.ValidateManifest(m)...)
		if le := r.support.CheckGeneration(m); le != nil {
			errs = append(errs, le)
		}
		if le := r.support.CheckCapabilities(m); le != nil {
			errs = append(errs, le)
		}
		if seenIDs[m.ModuleID] {
			errs = append(errs, &pluginhost.LoadError{
				Kind:   pluginhost.ErrDuplicateInstance,
				Module: label,
				Detail: "module_id appears more than once in the same load set",
			})
		}
		seenIDs[m.ModuleID] = true

		key := Key(m.ModuleID, m.Generation)
		if seenKeys[key] {
			errs = append(errs, &pluginhost.LoadError{
				Kind:   pluginhost.ErrDuplicateInstance,
				Module: label,
				Detail: fmt.Sprintf("same identity and generation %s appears twice", key),
			})
		}
		seenKeys[key] = true
	}

	// Same ID + same generation already live must not create a duplicate
	// instance. Artifacts that differ are treated as a conflicting duplicate,
	// not as a silent replacement.
	snap := r.Current()
	for _, m := range manifests {
		if m.ModuleID == "" {
			continue
		}
		if existing, ok := snap.Modules[m.ModuleID]; ok {
			em := existing.Manifest()
			if em.Generation == m.Generation {
				errs = append(errs, &pluginhost.LoadError{
					Kind:   pluginhost.ErrDuplicateInstance,
					Module: m.ModuleID,
					Detail: fmt.Sprintf("%s is already loaded; unload it before loading the same identity/generation again",
						Key(m.ModuleID, m.Generation)),
				})
			}
		}
	}

	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Module != errs[j].Module {
			return errs[i].Module < errs[j].Module
		}
		if errs[i].Entry != errs[j].Entry {
			return errs[i].Entry < errs[j].Entry
		}
		return errs[i].Kind < errs[j].Kind
	})
	return errs
}

// stageResult is a staged but not yet committed load transaction.
type stageResult struct {
	next    *Snapshot
	started []*Module
}

// LoadSet validates and atomically commits a new complete module set.
// Semantics:
//   - validation failure: the previous set remains fully in service;
//   - process spawn/handshake failure: every staged process is torn down and
//     the previous set remains fully in service (no half-loaded set);
//   - success: the staged set replaces the previous set in one atomic pointer
//     swap, after which the previous set's processes are stopped.
func (r *Registry) LoadSet(ctx context.Context, manifests []pluginhost.Manifest) (*Snapshot, pluginhost.LoadErrors) {
	stage, snap, errs := r.Stage(ctx, manifests)
	if len(errs) > 0 {
		return snap, errs
	}
	prev := r.Commit(stage)
	for _, mod := range prev.Modules {
		mod.Stop()
	}
	return stage.next, nil
}

// Stage validates manifests and spawns every module without changing the
// visible set. It lets the host persist before Commit, so durable and visible
// state advance together.
func (r *Registry) Stage(ctx context.Context, manifests []pluginhost.Manifest) (*stageResult, *Snapshot, pluginhost.LoadErrors) {
	r.loadMu.Lock()
	defer r.loadMu.Unlock()

	if errs := r.Validate(manifests); len(errs) > 0 {
		return nil, r.Current(), errs
	}

	staged := make(map[string]*Module, len(manifests))
	started := make([]*Module, 0, len(manifests))
	for _, m := range manifests {
		mod, lerr := Start(ctx, r.support.HostGeneration, m)
		if lerr != nil {
			for _, sm := range started {
				sm.Stop()
			}
			return nil, r.Current(), pluginhost.LoadErrors{lerr}
		}
		started = append(started, mod)
		staged[m.ModuleID] = mod
	}

	r.nextSet++
	return &stageResult{
		next:    &Snapshot{SetID: r.nextSet, Modules: staged},
		started: started,
	}, r.Current(), nil
}

// Commit publishes a staged set atomically and returns the previous set
// (whose processes the caller owns and must retire).
func (r *Registry) Commit(stage *stageResult) *Snapshot {
	return r.current.Swap(stage.next)
}

// Abort tears down a staged set without changing the visible set.
func (r *Registry) Abort(stage *stageResult) {
	if stage == nil {
		return
	}
	for _, mod := range stage.started {
		mod.Stop()
	}
}

// Recover forwards recovery to one module of the current set.
func (r *Registry) Recover(ctx context.Context, ref string) error {
	mod, ok := r.Find(ref)
	if !ok {
		return fmt.Errorf("module %q not found in current set", ref)
	}
	return mod.Recover(ctx)
}
