package libext

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

// JournalIndex.
//
// Two things are worth testing here and the rest follows from them. The first
// is that the index and the direct calls give the same answers - having two
// ways to read the journal is only safe while they agree, exactly as with
// PathIndex and WalkDir. The second is that the index actually walks once,
// since that is the entire reason it exists.

// buildInodeVersionFixture journals a copy of the inode table block carrying a
// larger, still-linked version of inode 3, and empties the live copy the way an
// unlink does.
func buildInodeVersionFixture(t testing.TB) []byte {
	t.Helper()

	priorBlock := make([]byte, testBlockSize)
	prior := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 9999)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 1)
		copy(raw[inodeOffBlockRaw:], classicRoot([]uint32{100, 101}, 0, 0, 0))
	})
	copy(priorBlock[2*testInodeSize:], prior)

	img := buildJournalFixture(t, priorBlock, testInodeTableBlock)
	writeTestInode(img, 3, inodeTypeRegular|0o644, 0, 0, nil)
	return img
}

// TestJournalIndexMatchesDirectQueries is the divergence guard. The index and
// the direct calls are two readings of the same journal, and a difference
// between them would mean one of the two is wrong about what was recovered.
func TestJournalIndexMatchesDirectQueries(t *testing.T) {
	fs := openFixture(t, buildInodeVersionFixture(t), Options{})

	ix, err := fs.BuildJournalIndex()
	if err != nil {
		t.Fatalf("BuildJournalIndex: %v", err)
	}

	for _, block := range []uint64{testInodeTableBlock, 100, 0} {
		want, err := fs.JournalBlockCopies(block)
		if err != nil {
			t.Fatalf("JournalBlockCopies(%d): %v", block, err)
		}
		got, err := ix.BlockCopies(block)
		if err != nil {
			t.Fatalf("index.BlockCopies(%d): %v", block, err)
		}
		if len(got) != len(want) {
			t.Fatalf("block %d: index gave %d copies, direct call gave %d", block, len(got), len(want))
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Errorf("block %d copy %d differs between the index and the direct call", block, i)
			}
		}
	}

	want, err := fs.JournalInodeVersions(3)
	if err != nil {
		t.Fatalf("JournalInodeVersions: %v", err)
	}
	got, err := ix.InodeVersions(3)
	if err != nil {
		t.Fatalf("index.InodeVersions: %v", err)
	}
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("index gave %d versions and the direct call %d; want 1 each", len(got), len(want))
	}
	if got[0].Size != want[0].Size || got[0].LinksCount != want[0].LinksCount {
		t.Errorf("recovered inode differs: index %+v, direct %+v", got[0], want[0])
	}
	if got[0].Size != 9999 {
		t.Errorf("recovered size = %d, want 9999; the fixture is not exercising recovery", got[0].Size)
	}
}

// TestJournalIndexWalksJournalOnce is E6's whole point: the direct calls pay for
// a journal walk each, and the index pays once however many questions follow.
func TestJournalIndexWalksJournalOnce(t *testing.T) {
	img := buildInodeVersionFixture(t)

	const queries = 8

	direct := &countingReaderAt{r: bytes.NewReader(img)}
	dfs, err := OpenWithOptions(direct, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	direct.count.Store(0)
	for i := 0; i < queries; i++ {
		if _, err := dfs.JournalInodeVersions(3); err != nil {
			t.Fatalf("JournalInodeVersions: %v", err)
		}
	}
	directReads := direct.count.Load()

	indexed := &countingReaderAt{r: bytes.NewReader(img)}
	ifs, err := OpenWithOptions(indexed, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	indexed.count.Store(0)
	ix, err := ifs.BuildJournalIndex()
	if err != nil {
		t.Fatalf("BuildJournalIndex: %v", err)
	}
	for i := 0; i < queries; i++ {
		if _, err := ix.InodeVersions(3); err != nil {
			t.Fatalf("index.InodeVersions: %v", err)
		}
	}
	indexedReads := indexed.count.Load()

	// The relationship, not a pinned number: the direct route repeats the walk
	// per query, so its cost grows with the query count while the index's does
	// not. On a real journal the gap is the difference between usable and not.
	if indexedReads >= directReads {
		t.Errorf("index cost %d reads for %d queries, direct route cost %d; "+
			"the index is meant to amortise the walk", indexedReads, queries, directReads)
	}
}

func TestJournalIndexHasBlockAndCopyCount(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, []byte("payload"), 100), Options{})

	ix, err := fs.BuildJournalIndex()
	if err != nil {
		t.Fatalf("BuildJournalIndex: %v", err)
	}

	if !ix.HasBlock(100) {
		t.Error("HasBlock(100) is false for the one block the fixture journals")
	}
	if got := ix.CopyCount(100); got != 1 {
		t.Errorf("CopyCount(100) = %d, want 1", got)
	}

	// A block the journal never carried is an ordinary answer, not an error.
	if ix.HasBlock(4242) {
		t.Error("HasBlock reports a copy of a block that was never journalled")
	}
	if got := ix.CopyCount(4242); got != 0 {
		t.Errorf("CopyCount of an unjournalled block = %d, want 0", got)
	}
	copies, err := ix.BlockCopies(4242)
	if err != nil {
		t.Errorf("BlockCopies of an unjournalled block errored: %v", err)
	}
	if copies != nil {
		t.Errorf("BlockCopies of an unjournalled block = %v, want nil", copies)
	}

	if ix.Len() != 1 {
		t.Errorf("Len = %d, want 1 distinct journalled block", ix.Len())
	}
}

// TestJournalIndexInodeVersionsRejectsInvalidInode pins that the number is
// validated at all, and - through the direct call - that it is validated before
// the journal walk rather than after it.
func TestJournalIndexInodeVersionsRejectsInvalidInode(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, nil, 100), Options{})

	ix, err := fs.BuildJournalIndex()
	if err != nil {
		t.Fatalf("BuildJournalIndex: %v", err)
	}
	if _, err := ix.InodeVersions(0); !errors.Is(err, ErrInvalidInode) {
		t.Errorf("InodeVersions(0) = %v, want ErrInvalidInode", err)
	}
	if _, err := fs.JournalInodeVersions(0); !errors.Is(err, ErrInvalidInode) {
		t.Errorf("JournalInodeVersions(0) = %v, want ErrInvalidInode", err)
	}
}

// TestJournalIndexTransactionsIsACopy: the outer slice is handed out, so a
// caller appending to it must not be able to disturb the index.
func TestJournalIndexTransactionsIsACopy(t *testing.T) {
	fs := openFixture(t, buildJournalFixture(t, []byte("payload"), 100), Options{})

	ix, err := fs.BuildJournalIndex()
	if err != nil {
		t.Fatalf("BuildJournalIndex: %v", err)
	}

	first := ix.Transactions()
	if len(first) == 0 {
		t.Fatal("the fixture produced no transactions")
	}
	first[0].Sequence = 0xDEAD

	second := ix.Transactions()
	if second[0].Sequence == 0xDEAD {
		t.Error("mutating the returned slice changed the index's own transactions")
	}
}

// TestJournalIndexWithoutJournalIsAnError keeps "nothing was journalled" and
// "there is no journal" distinguishable.
func TestJournalIndexWithoutJournalIsAnError(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	if _, err := fs.BuildJournalIndexContext(context.Background()); err == nil {
		t.Error("BuildJournalIndex succeeded on a filesystem with no journal")
	}
}
