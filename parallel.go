package libext

import (
	"context"
	"runtime"
	"sync"
)

// Bounded parallelism.
//
// The scans this package performs over a whole inode table are read-bound: each
// unit of work reads a few blocks and decodes them, and units do not depend on
// each other. That makes them worth overlapping, but only under three
// constraints that the design below enforces rather than merely intends.
//
//   - Bounded. Goroutine count is fixed at pool construction, never one per
//     work item. A filesystem with 500,000 block groups must not create 500,000
//     goroutines.
//   - Supervised. Every goroutine is joined through a WaitGroup before the pool
//     returns. No worker outlives the call that started it.
//   - Deterministic. Results are indexed by their position in the input and
//     reassembled in that order, so output does not depend on scheduling. A
//     parallel run and a sequential run produce byte-identical results.
//
// Parallelism is off unless asked for: Options.Parallelism defaults to 1, which
// runs the work inline with no goroutines and no channels at all. That is the
// degradation path, and it is the same code path rather than a second
// implementation that could drift.

const (
	// serialParallelism disables the pool entirely: work runs inline on the
	// calling goroutine. This is the zero value's behaviour.
	serialParallelism = 1

	// maxAutoParallelism caps automatic sizing. These scans are I/O bound, so
	// past a handful of workers the queue depth stops helping and only adds
	// contention on the reader.
	maxAutoParallelism = 8
)

// effectiveParallelism resolves the configured worker count to a concrete one.
//
// Anything other than an explicit request above 1, or ParallelismAuto, means
// sequential — including the zero value, so an unconfigured FS never starts a
// goroutine.
func (fs *FS) effectiveParallelism() int {
	switch n := fs.opts.Parallelism; {
	case n == ParallelismAuto:
		return min(max(runtime.NumCPU(), serialParallelism), maxAutoParallelism)
	case n > serialParallelism:
		return n
	default:
		return serialParallelism
	}
}

// parallelMap applies work to each index in [0, n) and returns the results in
// index order, regardless of the order they were computed in.
//
// work must be safe to call from several goroutines at once and must not depend
// on being called in any particular order. Its error stops the run: the context
// passed to the remaining workers is cancelled, and the first error by index is
// returned so the outcome does not depend on which worker failed first.
//
// With workers <= 1 this runs entirely inline — no goroutines, no channels —
// which is what makes the sequential path free of concurrency machinery rather
// than merely equivalent to it.
func parallelMap[T any](ctx context.Context, workers, n int, work func(ctx context.Context, i int) (T, error)) ([]T, error) {
	results := make([]T, n)

	if workers <= serialParallelism || n <= 1 {
		for i := range n {
			if err := ctx.Err(); err != nil {
				return results, err
			}
			v, err := work(ctx, i)
			if err != nil {
				return results, err
			}
			results[i] = v
		}
		return results, nil
	}

	if workers > n {
		workers = n
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		firstErr error
		firstIdx = n // index of the failing item, so the earliest one wins
	)
	recordErr := func(i int, err error) {
		mu.Lock()
		defer mu.Unlock()
		if i < firstIdx {
			firstErr, firstIdx = err, i
		}
		cancel()
	}

	// A single index channel feeds every worker, which is what bounds the
	// goroutine count independently of n.
	indices := make(chan int)
	var wg sync.WaitGroup

	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range indices {
				if ctx.Err() != nil {
					return
				}
				v, err := work(ctx, i)
				if err != nil {
					recordErr(i, err)
					return
				}
				// Each worker writes a distinct index, so the slice needs no
				// lock: no two goroutines touch the same element.
				results[i] = v
			}
		}()
	}

	for i := range n {
		select {
		case indices <- i:
		case <-ctx.Done():
			// A worker failed; stop feeding and let them drain.
			close(indices)
			wg.Wait()
			return results, firstError(firstErr, ctx.Err())
		}
	}
	close(indices)
	wg.Wait()

	return results, firstError(firstErr, nil)
}

// firstError prefers a work error over a context error: the context is usually
// cancelled *because* of the work error, so reporting the cancellation instead
// would hide the cause.
func firstError(workErr, ctxErr error) error {
	if workErr != nil {
		return workErr
	}
	return ctxErr
}
