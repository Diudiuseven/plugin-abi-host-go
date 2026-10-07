package host

import (
	"context"
	"errors"
	"fmt"

	pluginhost "github.com/Diudiuseven/plugin-abi-host-go"
)

// Host is the public facade of the capability host.
type Host struct {
	registry *Registry
	store    Store
	support  Support
}

// Option configures a Host.
type Option func(*config)

type config struct {
	support Support
	store   Store
}

// WithSupport sets the generation/capability support matrix.
func WithSupport(s Support) Option {
	return func(c *config) { c.support = s }
}

// WithPersistence enables durable commit of the last good set.
func WithPersistence(store Store) Option {
	return func(c *config) { c.store = store }
}

// New creates a new empty host.
func New(opts ...Option) *Host {
	cfg := config{support: DefaultSupport()}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Host{
		registry: NewRegistry(cfg.support),
		store:    cfg.store,
		support:  cfg.support,
	}
}

// Open creates a host and, when a store with a committed set is configured,
// restores that whole set before returning. Restoration is itself an
// all-or-nothing transaction: if the persisted set cannot be fully loaded,
// Open fails rather than serving a mixture. With no persisted set the host
// starts empty.
func Open(ctx context.Context, opts ...Option) (*Host, error) {
	h := New(opts...)
	if h.store == nil {
		return h, nil
	}
	_, manifests, err := h.store.Load(ctx)
	if errors.Is(err, ErrNoSavedSet) {
		return h, nil
	}
	if err != nil {
		return nil, fmt.Errorf("restoring module set: %w", err)
	}
	if _, lerrs := h.registry.LoadSet(ctx, manifests); len(lerrs) > 0 {
		return nil, fmt.Errorf("restoring module set: %w", lerrs)
	}
	return h, nil
}

// LoadSet validates and atomically commits a new complete module set. On
// validation or spawn failure the previously committed set remains fully in
// service and the returned error is a LoadErrors bundle. On success the new
// set is the only set visible, and when persistence is configured it is the
// only durable set after restart (persistence failure rolls the commit back
// to the previous in-memory set).
func (h *Host) LoadSet(ctx context.Context, manifests []pluginhost.Manifest) (*Snapshot, error) {
	stage, _, lerrs := h.registry.Stage(ctx, manifests)
	if len(lerrs) > 0 {
		return h.Current(), lerrs
	}

	if h.store != nil {
		ordered := make([]pluginhost.Manifest, 0, len(stage.next.Modules))
		for _, id := range stage.next.SetIDs() {
			ordered = append(ordered, stage.next.Modules[id].Manifest())
		}
		if err := h.store.Save(ctx, stage.next.SetID, ordered); err != nil {
			// The staged set was never published: abort it and keep serving
			// the previous set. Memory and disk therefore can never describe
			// different "current" sets.
			h.registry.Abort(stage)
			return h.Current(), fmt.Errorf("committing set durably: %w", err)
		}
	}

	prev := h.registry.Commit(stage)
	for _, mod := range prev.Modules {
		mod.Stop()
	}
	return stage.next, nil
}

// Current returns the committed snapshot.
func (h *Host) Current() *Snapshot { return h.registry.Current() }

// Call invokes an entry point of a module in the current set. The module
// reference is either a module ID (when it exists for one generation) or an
// "id@gen" qualified name. Failures are returned as *CallError / *IsolatedError
// and never propagate panics or crashes of the module process.
func (h *Host) Call(ctx context.Context, moduleRef, entry string, args ...any) (any, error) {
	mod, ok := h.registry.Find(moduleRef)
	if !ok {
		return nil, fmt.Errorf("module %q not found", moduleRef)
	}
	return mod.Call(ctx, entry, args)
}

// Query lists every module of the current set with its state.
func (h *Host) Query() []ModuleInfo { return h.registry.Query() }

// Recover attempts to take an isolated module back into service by spawning a
// fresh process and re-running the full handshake.
func (h *Host) Recover(ctx context.Context, moduleRef string) error {
	return h.registry.Recover(ctx, moduleRef)
}

// Close stops all modules of the current set.
func (h *Host) Close() error {
	snap := h.registry.Current()
	for _, mod := range snap.Modules {
		mod.Stop()
	}
	return nil
}
