package libext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// These tests exist to make the concurrency guarantees checkable rather than
// merely documented: identical results whatever the worker count, no goroutine
// when parallelism is off, bounded goroutines when it is on, a single-threaded
// callback, and cancellation that actually stops work.

func TestEffectiveParallelism(t *testing.T) {
	tests := []struct {
		name string
		set  int
		want int
	}{
		{"zero value is sequential", 0, serialParallelism},
		{"one is sequential", 1, serialParallelism},
		{"negative other than auto is sequential", -7, serialParallelism},
		{"explicit count is honoured", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &FS{opts: Options{Parallelism: tt.set}}
			if got := fs.effectiveParallelism(); got != tt.want {
				t.Errorf("effectiveParallelism() = %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("auto is bounded", func(t *testing.T) {
		fs := &FS{opts: Options{Parallelism: ParallelismAuto}}
		got := fs.effectiveParallelism()
		if got < serialParallelism || got > maxAutoParallelism {
			t.Errorf("auto sizing produced %d, outside [%d,%d]",
				got, serialParallelism, maxAutoParallelism)
		}
		if got > runtime.NumCPU() {
			t.Errorf("auto sizing produced %d workers on a %d CPU machine",
				got, runtime.NumCPU())
		}
	})
}

func TestParallelMapPreservesOrder(t *testing.T) {
	// The whole point of the pool is that scheduling must not reach the output.
	const n = 200

	for _, workers := range []int{1, 2, 8, n} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			got, err := parallelMap(context.Background(), workers, n,
				func(_ context.Context, i int) (int, error) {
					return i * i, nil
				})
			if err != nil {
				t.Fatalf("parallelMap: %v", err)
			}
			for i := range n {
				if got[i] != i*i {
					t.Fatalf("index %d = %d, want %d", i, got[i], i*i)
				}
			}
		})
	}
}

func TestParallelMapRunsInlineWhenSequential(t *testing.T) {
	// With parallelism off there must be no goroutine at all, not merely a pool
	// of one: that is the difference between "degrades gracefully" and "still
	// carries the machinery".
	before := runtime.NumGoroutine()

	var callerGoroutines sync.Map
	_, err := parallelMap(context.Background(), serialParallelism, 64,
		func(_ context.Context, i int) (int, error) {
			callerGoroutines.Store(goroutineFingerprint(), true)
			return i, nil
		})
	if err != nil {
		t.Fatalf("parallelMap: %v", err)
	}

	count := 0
	callerGoroutines.Range(func(_, _ any) bool { count++; return true })
	if count != 1 {
		t.Errorf("work ran on %d goroutines with parallelism off, want 1", count)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutine count rose from %d to %d with parallelism off", before, after)
	}
}

func TestParallelMapBoundsGoroutines(t *testing.T) {
	// A large item count must not translate into a large goroutine count.
	const (
		workers = 4
		items   = 5000
	)

	var concurrent, peak atomic.Int64
	_, err := parallelMap(context.Background(), workers, items,
		func(_ context.Context, i int) (int, error) {
			cur := concurrent.Add(1)
			defer concurrent.Add(-1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			return i, nil
		})
	if err != nil {
		t.Fatalf("parallelMap: %v", err)
	}
	if peak.Load() > workers {
		t.Errorf("peak concurrency %d exceeded the %d configured workers", peak.Load(), workers)
	}
}

func TestParallelMapReportsFirstErrorByIndex(t *testing.T) {
	// Several items fail; the reported error must be the earliest by index, so
	// the result does not depend on which worker happened to get there first.
	errAt := func(i int) error { return fmt.Errorf("failed at %d", i) }

	for _, workers := range []int{1, 8} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			_, err := parallelMap(context.Background(), workers, 100,
				func(_ context.Context, i int) (int, error) {
					if i == 10 || i == 50 || i == 90 {
						return 0, errAt(i)
					}
					return i, nil
				})
			if err == nil {
				t.Fatal("expected an error")
			}
			if err.Error() != errAt(10).Error() {
				t.Errorf("error = %v, want the earliest failing index (10)", err)
			}
		})
	}
}

func TestParallelMapHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var started atomic.Int64
	_, err := parallelMap(ctx, 4, 10000, func(ctx context.Context, i int) (int, error) {
		if started.Add(1) == 8 {
			cancel()
		}
		return i, nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if started.Load() >= 10000 {
		t.Error("cancellation did not stop the run early")
	}
}

func TestParallelMapJoinsEveryWorker(t *testing.T) {
	// No goroutine may outlive the call, in either the success or failure path.
	settle := func() int {
		runtime.GC()
		return runtime.NumGoroutine()
	}

	before := settle()

	_, _ = parallelMap(context.Background(), 8, 500,
		func(_ context.Context, i int) (int, error) { return i, nil })
	_, _ = parallelMap(context.Background(), 8, 500,
		func(_ context.Context, i int) (int, error) {
			if i == 3 {
				return 0, errors.New("boom")
			}
			return i, nil
		})

	if after := settle(); after > before+1 {
		t.Errorf("goroutines leaked: %d before, %d after", before, after)
	}
}

// goroutineFingerprint returns something that differs per goroutine without
// parsing stacks: the address of a stack-local variable.
func goroutineFingerprint() uintptr {
	var x byte
	return uintptr(len(fmt.Sprint(&x)))
}

// ---------------------------------------------------------------------------
// scan-level guarantees
// ---------------------------------------------------------------------------

func TestScanDeletedIdenticalAcrossParallelism(t *testing.T) {
	// The contract users rely on: turning parallelism up changes timing only.
	img := buildDeletedFixture(t)

	var reference []DeletedEntry
	for _, p := range []int{0, 1, 2, 4, ParallelismAuto} {
		fs, err := OpenWithOptions(bytes.NewReader(img), Options{Parallelism: p})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		entries, err := fs.DeletedEntries()
		if err != nil {
			t.Fatalf("DeletedEntries(parallelism=%d): %v", p, err)
		}

		if reference == nil {
			reference = entries
			continue
		}
		if len(entries) != len(reference) {
			t.Fatalf("parallelism=%d produced %d entries, sequential produced %d",
				p, len(entries), len(reference))
		}
		for i := range entries {
			if entries[i].Inode != reference[i].Inode || entries[i].Name != reference[i].Name {
				t.Errorf("parallelism=%d entry %d = inode %d %q, sequential had inode %d %q",
					p, i, entries[i].Inode, entries[i].Name,
					reference[i].Inode, reference[i].Name)
			}
		}
	}
}

func TestScanDeletedCallbackIsSingleThreaded(t *testing.T) {
	// The callback must never need a lock of its own, whatever the setting.
	img := buildDeletedFixture(t)

	fs, err := OpenWithOptions(bytes.NewReader(img), Options{Parallelism: 8})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var inCallback atomic.Int64
	err = fs.ScanDeleted(DeletedScanOptions{}, func(DeletedEntry) error {
		if n := inCallback.Add(1); n != 1 {
			t.Errorf("%d goroutines inside the callback at once", n)
		}
		inCallback.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatalf("ScanDeleted: %v", err)
	}
}

func TestScanDeletedContextCancellation(t *testing.T) {
	img := buildDeletedFixture(t)
	fs := openFixture(t, img, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the scan begins

	err := fs.ScanDeletedContext(ctx, DeletedScanOptions{}, func(DeletedEntry) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestScanDeletedRejectsNilContext(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})
	if err := fs.ScanDeletedContext(nil, DeletedScanOptions{}, func(DeletedEntry) error { return nil }); err == nil {
		t.Error("ScanDeletedContext accepted a nil context")
	}
}

// ---------------------------------------------------------------------------
// shared state
// ---------------------------------------------------------------------------

func TestBitmapCacheIsSharedAndReadOnce(t *testing.T) {
	counter := &countingReaderAt{r: bytes.NewReader(buildDeletedFixture(t))}
	fs, err := OpenWithOptions(counter, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	first, err := fs.BlockBitmap(0)
	if err != nil {
		t.Fatalf("BlockBitmap: %v", err)
	}

	counter.count.Store(0)
	for range 50 {
		again, err := fs.BlockBitmap(0)
		if err != nil {
			t.Fatalf("BlockBitmap: %v", err)
		}
		if &again[0] != &first[0] {
			t.Fatal("cache returned a different backing array")
		}
	}
	if n := counter.count.Load(); n != 0 {
		t.Errorf("cached bitmap still issued %d reads", n)
	}
}

func TestConcurrentBitmapAccessIsRaceFree(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = fs.BlockBitmap(0)
				_, _ = fs.InodeBitmap(0)
				_, _ = fs.InodeAllocated(2)
				_, _ = fs.BlockAllocated(5)
			}
		}()
	}
	wg.Wait()
}

func TestCloseIsSafeConcurrentlyWithReads(t *testing.T) {
	// Close used to write an unsynchronised bool that every read consulted.
	img := buildDeletedFixture(t)
	fs, err := OpenWithOptions(bytes.NewReader(img), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				// Either outcome is fine; neither may race or panic.
				_, _ = fs.ReadInode(RootInode)
				_ = fs.IsClosed()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = fs.Close()
	}()

	wg.Wait()

	if !fs.IsClosed() {
		t.Error("IsClosed() = false after Close()")
	}
	if err := fs.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil (idempotent)", err)
	}
}

func TestClosedFSRefusesReads(t *testing.T) {
	fs := openFixture(t, buildDeletedFixture(t), Options{})
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := fs.ReadInode(RootInode); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("read after close = %v, want io.ErrClosedPipe", err)
	}
}
