package host

import (
	"errors"
	"sync"
	"testing"
)

// syntheticModule builds an in-memory module without opening a plugin, so
// the host's synchronization (snapshot publication and per-module failure
// accounting) can be exercised under the race detector independently of
// the go-test/plugin build-identity limitation.
func syntheticModule(id string, gen int, failing bool, threshold int) *module {
	m := &module{
		manifest:  &Manifest{Schema: 1, ID: id, Generation: gen, Entries: []EntryDecl{{Name: "Op", Kind: KindCall}}},
		path:      "synthetic://" + id,
		threshold: threshold,
		status:    StatusHealthy,
	}
	m.entries = map[string]entryFn{
		"Op": func(arg any) (any, error) {
			if failing {
				return nil, errors.New("synthetic failure")
			}
			return "ok", nil
		},
	}
	return m
}

func TestRaceSnapshotSwapsAndCalls(t *testing.T) {
	h, err := New(Config{FailureThreshold: 1000})
	if err != nil {
		t.Fatal(err)
	}
	h.publish(map[string]*module{
		identity("a", 1): syntheticModule("a", 1, false, 1000),
		identity("b", 2): syntheticModule("b", 2, true, 1000),
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers continuously commit fresh complete snapshot maps.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			next := map[string]*module{
				identity("a", 1): syntheticModule("a", 1, false, 1000),
				identity("b", 2): syntheticModule("b", 2, true, 1000),
			}
			if i%2 == 0 {
				next[identity("c", 1)] = syntheticModule("c", 1, false, 1000)
			}
			h.publish(next)
			select {
			case <-stop:
				return
			default:
			}
		}
	}()

	// Readers, callers and status queries hit the structures concurrently.
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = h.Modules()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = h.Call("b", 2, "Op", "x")
				_, _ = h.Call("a", 1, "Op", "x")
			}
		}()
	}

	// Exact failure counting against one module under concurrent calls.
	m := syntheticModule("d", 1, true, 10000)
	h2, _ := New(Config{})
	h2.publish(map[string]*module{identity("d", 1): m})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_, _ = h2.Call("d", 1, "Op", "x")
			}
		}()
	}

	close(stop)
	wg.Wait()
	if st, _ := h2.Module("d", 1); st.Failures != 100 {
		t.Fatalf("exact failure count lost: %d", st.Failures)
	}
}
