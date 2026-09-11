package ui

import "sync"

// defaultFanOutWorkers bounds FanOut's worker pool when the caller
// doesn't. Probes are I/O-shaped (git subprocesses, host API calls),
// so a modest bound keeps a large fleet from stampeding while still
// collapsing the wall clock.
const defaultFanOutWorkers = 8

// SlotGroup is the optional slot-hosting half of the group reporter
// contract — implemented by the interactive group (RunGroup's inner
// Reporter), whose render model can hold several concurrently
// animating child rows. Reporters without a live render (raw, plain,
// capture) don't implement it; FanOut degrades for them.
type SlotGroup interface {
	Reporter

	// Slot starts a concurrent child slot without blocking: a
	// running-indicator row for title at a fixed position. The
	// returned Reporter is slot-scoped — its emissions replace the
	// running row in place — and done clears the indicator; call it
	// exactly once, after the resolving emissions.
	Slot(title string) (Reporter, func())
}

// FanOut runs work(i) for every item concurrently under grp, each
// with its own child running-indicator row, and swaps each row for
// its result in place as it resolves: resolve(i, r) emits the item's
// terminal rows through r as soon as its work completes, while
// slower siblings keep spinning. Row order follows item order
// regardless of completion order. Blocks until all items resolve —
// the enclosing group's own spinner therefore outlives every child.
//
// The pool is bounded by workers (defaultFanOutWorkers when <= 0).
// resolve calls are serialized, so they may touch shared state
// without further locking.
//
// When grp cannot host slots (raw, plain, capture reporters), the
// work still fans out concurrently, but the rows emit after the
// gather completes, in item order — the stable order those
// reporters' consumers (piped output, tests) expect.
func FanOut(grp Reporter, n, workers int, label func(int) string, work func(int), resolve func(int, Reporter)) {
	if n <= 0 {
		return
	}
	if workers <= 0 {
		workers = defaultFanOutWorkers
	}

	sg, ok := grp.(SlotGroup)
	if !ok {
		RunBounded(n, workers, work)
		for i := range n {
			resolve(i, grp)
		}
		return
	}

	slots := make([]Reporter, n)
	dones := make([]func(), n)
	for i := range n {
		slots[i], dones[i] = sg.Slot(label(i))
	}

	var mu sync.Mutex
	RunBounded(n, workers, func(i int) {
		work(i)
		mu.Lock()
		defer mu.Unlock()
		resolve(i, slots[i])
		dones[i]()
	})
}

// RunBounded fans fn out across n items on a pool of at most workers
// goroutines and blocks until all complete. The concurrency seam
// FanOut and the non-interactive gather paths share, so the two
// can't drift on pooling behavior.
func RunBounded(n, workers int, fn func(int)) {
	if n <= 0 {
		return
	}
	if workers <= 0 {
		workers = defaultFanOutWorkers
	}
	if workers > n {
		workers = n
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}()
	}
	wg.Wait()
}
