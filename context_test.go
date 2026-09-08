package libext

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// The ...Context contract, applied to every variant at once.
//
// These two tables are the reason to write them as tables: each new Context
// method is one row, and a variant that forgets a rule fails here rather than
// going unnoticed until someone tries to cancel it in anger.
//
// Each row brings its own fixture. A journal row run against an image with no
// journal would fail on "no journal inode found" before ever consulting the
// context, and pass the cancellation test vacuously.

type ctxCase struct {
	name string
	// fs builds a filesystem the call can actually do work on.
	fs func(t *testing.T) *FS
	// call runs the Context variant and returns only its error.
	call func(ctx context.Context, fs *FS) error
}

func ctxCases() []ctxCase {
	nested := func(t *testing.T) *FS {
		t.Helper()
		return openFixture(t, buildNestedDirFixture(t), Options{})
	}
	journal := func(t *testing.T) *FS {
		t.Helper()
		return openFixture(t, buildJournalFixture(t, []byte("payload"), 100), Options{})
	}

	return []ctxCase{
		{"WalkDirContext", nested, func(ctx context.Context, fs *FS) error {
			return fs.WalkDirContext(ctx, RootInode, func(string, DirEntry) error { return nil })
		}},
		{"WalkDirWithInodeContext", nested, func(ctx context.Context, fs *FS) error {
			return fs.WalkDirWithInodeContext(ctx, RootInode, func(string, DirEntry, Inode) error { return nil })
		}},
		{"BuildPathIndexContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.BuildPathIndexContext(ctx)
			return err
		}},
		{"PathForContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.PathForContext(ctx, nestedDeep)
			return err
		}},
		{"ScanDeletedContext", nested, func(ctx context.Context, fs *FS) error {
			return fs.ScanDeletedContext(ctx, DeletedScanOptions{}, func(DeletedEntry) error { return nil })
		}},
		{"DeletedEntriesContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.DeletedEntriesContext(ctx)
			return err
		}},
		{"DeletedEntriesWithOptionsContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.DeletedEntriesWithOptionsContext(ctx, DeletedScanOptions{})
			return err
		}},
		{"ScanDirSlackContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.ScanDirSlackContext(ctx, RootInode)
			return err
		}},
		{"OrphanInodesContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.OrphanInodesContext(ctx)
			return err
		}},
		{"ReportWithOptionsContext", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.ReportWithOptionsContext(ctx, "t", ReportOptions{})
			return err
		}},
		{"ReportWithOptionsContext/deep", nested, func(ctx context.Context, fs *FS) error {
			_, err := fs.ReportWithOptionsContext(ctx, "t", ReportOptions{DeepScan: true})
			return err
		}},
		{"WriteReportWithOptionsContext", nested, func(ctx context.Context, fs *FS) error {
			return fs.WriteReportWithOptionsContext(ctx, "t", ReportOptions{}, io.Discard)
		}},
		{"ListJournalTransactionsContext", journal, func(ctx context.Context, fs *FS) error {
			_, err := fs.ListJournalTransactionsContext(ctx)
			return err
		}},
		{"JournalBlockCopiesContext", journal, func(ctx context.Context, fs *FS) error {
			_, err := fs.JournalBlockCopiesContext(ctx, 100)
			return err
		}},
		{"JournalInodeVersionsContext", journal, func(ctx context.Context, fs *FS) error {
			_, err := fs.JournalInodeVersionsContext(ctx, RootInode)
			return err
		}},
		{"BuildJournalIndexContext", journal, func(ctx context.Context, fs *FS) error {
			_, err := fs.BuildJournalIndexContext(ctx)
			return err
		}},
	}
}

// TestContextVariantsRejectNilContext pins the first shared rule: a nil context
// is an error, not a panic and not a silent default to Background.
func TestContextVariantsRejectNilContext(t *testing.T) {
	for _, c := range ctxCases() {
		t.Run(c.name, func(t *testing.T) {
			fs := c.fs(t)
			//lint:ignore SA1012 passing nil is precisely what is under test here
			err := c.call(nil, fs) //nolint:staticcheck
			if err == nil {
				t.Fatal("a nil context was accepted")
			}
			if err.Error() != "context is nil" {
				t.Errorf("error = %q, want %q", err.Error(), "context is nil")
			}
		})
	}
}

// TestContextVariantsHonourCancellation pins the second: a cancelled context
// produces ctx.Err() verbatim, so errors.Is(err, context.Canceled) works.
//
// The cancellation happens before the call, which is the case that catches a
// paced check written as increment-then-test: on a fixture this small the first
// check has to fall on the first iteration or it never happens at all.
func TestContextVariantsHonourCancellation(t *testing.T) {
	for _, c := range ctxCases() {
		t.Run(c.name, func(t *testing.T) {
			fs := c.fs(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := c.call(ctx, fs)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
		})
	}
}

// TestScanDeletedContextCancelsDuringSlackIndexing is a regression test for a
// documented promise that did not hold: ScanDeletedContext says cancelling
// stops it at the next checkpoint, but the slack-indexing phase - the expensive
// one on a large image - consulted the context nowhere at all and returned nil.
func TestScanDeletedContextCancelsDuringSlackIndexing(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := fs.ScanDeletedContext(ctx, DeletedScanOptions{
		SkipInodeTable: true,
		SkipOrphanList: true,
	}, func(DeletedEntry) error { return nil })

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestScanDeletedContextCancelsDuringOrphanScan covers the other phase that was
// uncancellable.
func TestScanDeletedContextCancelsDuringOrphanScan(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := fs.ScanDeletedContext(ctx, DeletedScanOptions{
		SkipInodeTable: true,
		SkipDirSlack:   true,
	}, func(DeletedEntry) error { return nil })

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestIndexSlackIsDeterministic guards the map-iteration-order fix. The first
// slack name found for an inode is the one kept, so iterating the directory map
// directly let Go's randomised order decide the winner - and the same image
// produced different output run to run.
func TestIndexSlackIsDeterministic(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	first, err := fs.DeletedEntriesWithOptions(DeletedScanOptions{})
	if err != nil {
		t.Fatalf("DeletedEntriesWithOptions: %v", err)
	}

	for i := range 20 {
		again, err := fs.DeletedEntriesWithOptions(DeletedScanOptions{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if len(again) != len(first) {
			t.Fatalf("run %d returned %d entries, first run returned %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].Inode != first[j].Inode || again[j].Name != first[j].Name ||
				again[j].Path != first[j].Path || again[j].ParentInode != first[j].ParentInode {
				t.Fatalf("run %d entry %d = %+v, first run had %+v", i, j, again[j], first[j])
			}
		}
	}
}

// TestJournalBlocksResolvedOnce covers the memoisation. JournalBlockCopies used
// to resolve the journal's block list three times per call: once directly, once
// through ListJournalTransactions, and once more through the journal superblock
// read inside it.
func TestJournalBlocksResolvedOnce(t *testing.T) {
	img := buildJournalFixture(t, []byte("payload"), 100)

	counter := &countingReaderAt{r: bytes.NewReader(img)}
	fs, err := OpenWithOptions(counter, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Warm the cache, then measure a second identical call: with the list
	// memoised the journal inode is not read again at all.
	if _, err := fs.JournalBlockCopies(100); err != nil {
		t.Fatalf("JournalBlockCopies: %v", err)
	}
	counter.count.Store(0)
	if _, err := fs.JournalBlockCopies(100); err != nil {
		t.Fatalf("JournalBlockCopies: %v", err)
	}
	second := counter.count.Load()

	// Four journal blocks are re-read plus the copy itself; what must not
	// reappear is the inode read and extent resolution behind the block list.
	if second > 8 {
		t.Errorf("a second JournalBlockCopies issued %d reads; the journal block list "+
			"is supposed to be resolved once and shared", second)
	}
}
