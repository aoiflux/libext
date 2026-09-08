package libext

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// WalkDir behaviour.
//
// The walk reads every child's inode in order to decide whether to descend.
// These tests pin the two consequences of that: the callback receives the
// metadata that read produced, and it is not read a second time to get it.

// TestWalkDirEntriesCarryInodeMetadata: the entry handed to the callback is
// populated, because the inode behind it was read before the callback ran.
func TestWalkDirEntriesCarryInodeMetadata(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	seen := map[string]DirEntry{}
	err := fs.WalkDir(RootInode, func(p string, e DirEntry) error {
		seen[p] = e
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	notes, ok := seen["/notes.txt"]
	if !ok {
		t.Fatal("/notes.txt was not visited")
	}
	if notes.Size != 1234 {
		t.Errorf("Size = %d, want 1234", notes.Size)
	}
	if notes.UID != 1000 || notes.GID != 1001 {
		t.Errorf("UID/GID = %d/%d, want 1000/1001", notes.UID, notes.GID)
	}
	if notes.Generation != testGeneration {
		t.Errorf("Generation = %#x, want %#x", notes.Generation, testGeneration)
	}
	if notes.Times.Mtime.Unix() != 0x69e58838 {
		t.Errorf("Mtime = %v, want unix %#x", notes.Times.Mtime, 0x69e58838)
	}
	if notes.ParentInode != RootInode {
		t.Errorf("ParentInode = %d, want %d", notes.ParentInode, RootInode)
	}

	deep, ok := seen["/sub/deep.txt"]
	if !ok {
		t.Fatal("/sub/deep.txt was not visited")
	}
	if deep.ParentInode != nestedSub {
		t.Errorf("/sub/deep.txt ParentInode = %d, want %d", deep.ParentInode, nestedSub)
	}
}

// TestWalkDirReadsEachChildInodeOnce guards the reorder against being "fixed"
// by adding a second read.
//
// It compares against an equivalent hand-rolled traversal - one listing per
// directory, one inode read per entry - rather than pinning a number, so it
// keeps its meaning if the cost of a listing changes.
func TestWalkDirReadsEachChildInodeOnce(t *testing.T) {
	img := buildNestedDirFixture(t)

	walkReads := func() int64 {
		counter := &countingReaderAt{r: bytes.NewReader(img)}
		fs, err := OpenWithOptions(counter, Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		counter.count.Store(0)
		if err := fs.WalkDir(RootInode, func(string, DirEntry) error { return nil }); err != nil {
			t.Fatalf("WalkDir: %v", err)
		}
		return counter.count.Load()
	}()

	manualReads := func() int64 {
		counter := &countingReaderAt{r: bytes.NewReader(img)}
		fs, err := OpenWithOptions(counter, Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		counter.count.Store(0)
		var visit func(uint32)
		visit = func(dir uint32) {
			entries, err := fs.ListDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.Name == "." || e.Name == ".." {
					continue
				}
				child, err := fs.ReadInode(e.Inode)
				if err == nil && child.IsDirectory {
					visit(e.Inode)
				}
			}
		}
		visit(RootInode)
		return counter.count.Load()
	}()

	// The one permitted extra is WalkDir's up-front read of startInode, which it
	// makes to reject a non-directory before descending. A regression that read
	// each child twice would show up as one extra per entry, not one per walk.
	const startInodeCheck = 1
	if walkReads > manualReads+startInodeCheck {
		t.Errorf("WalkDir issued %d reads; one listing per directory plus one inode read "+
			"per entry (plus %d for the start-inode check) is %d, so it is reading "+
			"something twice", walkReads, startInodeCheck, manualReads+startInodeCheck)
	}
}

// TestWalkDirDerivesIsDirectoryFromInode is the regression test for the one
// behaviour change in this work, and simultaneously the demonstration that it
// is a fix.
//
// Without the FILETYPE feature a directory record carries no type at all, so
// the record-derived IsDirectory is false for everything - while the walk
// descends into those directories regardless. Taking it from the inode makes
// the flag agree with the traversal.
func TestWalkDirDerivesIsDirectoryFromInode(t *testing.T) {
	cfg := defaultSBConfig()
	cfg.incompat = 0 // no FILETYPE: the type byte is part of the name length
	img := buildTestImage(t, cfg)

	// Records must carry file type 0, or the 16-bit name length misparses.
	var root []byte
	root = append(root, dirent(RootInode, 12, 0, ".")...)
	root = append(root, dirent(RootInode, 12, 0, "..")...)
	root = append(root, dirent(nestedSub, uint16(testBlockSize-24), 0, "sub")...)
	writeTestBlock(img, nestedRootBlock, root)

	var sub []byte
	sub = append(sub, dirent(nestedSub, 12, 0, ".")...)
	sub = append(sub, dirent(RootInode, 12, 0, "..")...)
	sub = append(sub, dirent(nestedDeep, uint16(testBlockSize-24), 0, "deep.txt")...)
	writeTestBlock(img, nestedSubBlock, sub)

	writeTestInode(img, RootInode, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{nestedRootBlock}, 0, 0, 0))
	writeTestInode(img, nestedSub, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{nestedSubBlock}, 0, 0, 0))
	writeTestInode(img, nestedDeep, inodeTypeRegular|0o644, 99, 0, nil)

	fs := openFixture(t, img, Options{})

	// The record itself cannot say: this is the state the callback used to see.
	listed, err := fs.ListDir(RootInode)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	for _, e := range listed {
		if e.Name == "sub" && e.IsDirectory {
			t.Fatal("fixture is not exercising the no-FILETYPE case: the record reported a type")
		}
	}

	seen := map[string]bool{}
	err = fs.WalkDir(RootInode, func(p string, e DirEntry) error {
		seen[p] = e.IsDirectory
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	if isDir, ok := seen["/sub"]; !ok {
		t.Fatal("/sub was not visited")
	} else if !isDir {
		t.Error("/sub reported IsDirectory=false while the walk descended into it")
	}
	if isDir, ok := seen["/sub/deep.txt"]; !ok {
		t.Error("/sub/deep.txt was not visited, so the walk did not descend")
	} else if isDir {
		t.Error("/sub/deep.txt reported IsDirectory=true")
	}
}

// TestWalkDirStopsAtCycle: a record pointing back at an already-entered
// directory must be reported and then left alone, not followed.
func TestWalkDirStopsAtCycle(t *testing.T) {
	img := buildNestedDirFixture(t)

	// Replace sub's tail record with one pointing back at the root.
	var sub []byte
	sub = append(sub, dirent(nestedSub, 12, extDirentTypeDirectory, ".")...)
	sub = append(sub, dirent(RootInode, 12, extDirentTypeDirectory, "..")...)
	sub = append(sub, dirent(nestedDeep, 20, 1, "deep.txt")...)
	sub = append(sub, dirent(RootInode, uint16(testBlockSize-44), extDirentTypeDirectory, "loop")...)
	writeTestBlock(img, nestedSubBlock, sub)

	fs := openFixture(t, img, Options{})

	visits := map[string]int{}
	done := make(chan error, 1)
	go func() {
		done <- fs.WalkDir(RootInode, func(p string, e DirEntry) error {
			visits[p]++
			return nil
		})
	}()
	if err := <-done; err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	for p, n := range visits {
		if n != 1 {
			t.Errorf("%s visited %d times, want 1", p, n)
		}
	}
	if visits["/sub/loop"] != 1 {
		t.Error("the looping record was not reported to the callback")
	}
	if !hasWarning(fs.Warnings(), WarnDegradedRead) {
		t.Error("descending into an already-entered directory was refused without a warning")
	}
}

func TestWalkDirPropagatesCallbackError(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	sentinel := errors.New("stop here")
	calls := 0
	err := fs.WalkDir(RootInode, func(string, DirEntry) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("WalkDir error = %v, want the callback's own error", err)
	}
	if calls != 1 {
		t.Errorf("callback ran %d times after returning an error, want 1", calls)
	}
}

func TestWalkDirSkipsDotEntries(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	err := fs.WalkDir(RootInode, func(p string, e DirEntry) error {
		if e.Name == "." || e.Name == ".." {
			t.Errorf("walk reported a dot entry: %q at %q", e.Name, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func TestWalkDirRejectsNonDirectory(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	err := fs.WalkDir(nestedNotes, func(string, DirEntry) error { return nil })
	if !errors.Is(err, ErrNotDirectory) {
		t.Errorf("WalkDir on a file: error = %v, want ErrNotDirectory", err)
	}
}

// TestStampParentIsAppliedThroughParseError: a directory whose tail is damaged
// still returns the records read before the fault, and those records were read
// from this directory whether or not the rest decoded.
func TestStampParentIsAppliedThroughParseError(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	var dir []byte
	dir = append(dir, dirent(RootInode, 12, extDirentTypeDirectory, ".")...)
	dir = append(dir, dirent(RootInode, 12, extDirentTypeDirectory, "..")...)
	dir = append(dir, dirent(nestedNotes, 20, 1, "notes.txt")...)
	// A record claiming a length that overruns the block.
	bad := make([]byte, 12)
	binary.LittleEndian.PutUint32(bad[0:], 44)
	binary.LittleEndian.PutUint16(bad[4:], 8192)
	bad[6] = 4
	bad[7] = 1
	copy(bad[8:], "oops")
	dir = append(dir, bad...)
	writeTestBlock(img, nestedRootBlock, dir)

	writeTestInode(img, RootInode, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{nestedRootBlock}, 0, 0, 0))

	fs := openFixture(t, img, Options{})

	entries, err := fs.ListDir(RootInode)
	if err == nil {
		t.Log("directory parsed without error; the stamp is still the thing under test")
	}
	if len(entries) == 0 {
		t.Fatal("no entries survived the damaged record")
	}
	for _, e := range entries {
		if e.ParentInode != RootInode {
			t.Errorf("entry %q returned alongside a parse error has ParentInode %d, want %d",
				e.Name, e.ParentInode, RootInode)
		}
	}
}

// ---------------------------------------------------------------------------
// WalkDirWithInode
// ---------------------------------------------------------------------------

// TestWalkDirWithInodeHandsOverTheInodeItRead: what the callback receives is the
// same inode ReadInode gives, so a caller can stop reading it again.
func TestWalkDirWithInodeHandsOverTheInodeItRead(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	seen := map[string]Inode{}
	err := fs.WalkDirWithInode(RootInode, func(p string, e DirEntry, inode Inode) error {
		if inode.Number != e.Inode {
			t.Errorf("%s: inode.Number = %d, entry.Inode = %d", p, inode.Number, e.Inode)
		}
		seen[p] = inode
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDirWithInode: %v", err)
	}

	notes, ok := seen["/notes.txt"]
	if !ok {
		t.Fatalf("the walk did not report /notes.txt; it saw %v", seen)
	}

	// Compared against a direct read rather than against hand-written constants,
	// so the test says "the same inode" rather than restating the fixture.
	want, err := fs.ReadInode(nestedNotes)
	if err != nil {
		t.Fatalf("ReadInode: %v", err)
	}
	if notes.Size != want.Size || notes.Generation != want.Generation ||
		notes.LinksCount != want.LinksCount || !notes.Mtime.Equal(want.Mtime) {
		t.Errorf("walk gave %+v, ReadInode gave %+v", notes, want)
	}

	// The point of this shape: fields DirEntry does not carry at all.
	if notes.LinksCount != 2 {
		t.Errorf("LinksCount = %d, want 2; a hard-linked file is what makes this "+
			"field worth handing over", notes.LinksCount)
	}
}

// TestWalkDirWithInodeReportsUnreadableInodeAsZero pins the contract for a
// record naming an inode that cannot be read: the name is still reported,
// because the record is evidence, and Number == 0 is how the callback tells.
func TestWalkDirWithInodeReportsUnreadableInodeAsZero(t *testing.T) {
	img := buildNestedDirFixture(t)

	// Repoint notes.txt at an inode number beyond the filesystem. The record
	// stays valid; the inode behind it cannot be read.
	const beyondTheFilesystem = 60000
	root := make([]byte, 0, testBlockSize)
	root = append(root, dirent(RootInode, 12, extDirentTypeDirectory, ".")...)
	root = append(root, dirent(RootInode, 12, extDirentTypeDirectory, "..")...)
	root = append(root, dirent(beyondTheFilesystem, 20, 1, "notes.txt")...)
	root = append(root, dirent(nestedSub, uint16(testBlockSize-44), extDirentTypeDirectory, "sub")...)
	writeTestBlock(img, nestedRootBlock, root)

	fs := openFixture(t, img, Options{})

	var found bool
	err := fs.WalkDirWithInode(RootInode, func(p string, e DirEntry, inode Inode) error {
		if p != "/notes.txt" {
			return nil
		}
		found = true
		if inode.Number != 0 {
			t.Errorf("inode.Number = %d for an unreadable inode, want 0", inode.Number)
		}
		if inode.Size != 0 || inode.Mode != 0 {
			t.Errorf("the zero Inode contract is broken: got %+v", inode)
		}
		if e.Inode != beyondTheFilesystem {
			t.Errorf("entry.Inode = %d, want the record's own value %d", e.Inode, beyondTheFilesystem)
		}
		if e.Name != "notes.txt" {
			t.Errorf("entry.Name = %q, want notes.txt", e.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDirWithInode: %v", err)
	}
	if !found {
		t.Error("a record naming an unreadable inode was dropped from the walk")
	}
}

// TestWalkDirWithInodeVisitsTheSameEntries: the two callback shapes differ in
// what they hand over and in nothing else.
func TestWalkDirWithInodeVisitsTheSameEntries(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	var plain, withInode []string
	if err := fs.WalkDir(RootInode, func(p string, e DirEntry) error {
		plain = append(plain, p)
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if err := fs.WalkDirWithInode(RootInode, func(p string, e DirEntry, _ Inode) error {
		withInode = append(withInode, p)
		return nil
	}); err != nil {
		t.Fatalf("WalkDirWithInode: %v", err)
	}

	if len(plain) == 0 {
		t.Fatal("the fixture produced no entries")
	}
	if len(plain) != len(withInode) {
		t.Fatalf("WalkDir saw %v, WalkDirWithInode saw %v", plain, withInode)
	}
	for i := range plain {
		if plain[i] != withInode[i] {
			t.Errorf("entry %d: WalkDir gave %q, WalkDirWithInode gave %q", i, plain[i], withInode[i])
		}
	}
}

// ---------------------------------------------------------------------------
// LookupPath
// ---------------------------------------------------------------------------

// TestLookupPathRootIsADirectory. The root literal set FileType 2 - directory -
// while leaving IsDirectory false, so the one entry every caller starts from
// gave opposite answers depending on which field was read.
func TestLookupPathRootIsADirectory(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	root, err := fs.LookupPath("/")
	if err != nil {
		t.Fatalf("LookupPath(/): %v", err)
	}
	if !root.IsDirectory {
		t.Error("LookupPath(\"/\") reports the root as not a directory")
	}
	if root.FileType != extDirentTypeDirectory {
		t.Errorf("FileType = %d, want %d", root.FileType, extDirentTypeDirectory)
	}
	if root.Inode != RootInode || root.ParentInode != RootInode {
		t.Errorf("root Inode/ParentInode = %d/%d, want %d/%d",
			root.Inode, root.ParentInode, RootInode, RootInode)
	}

	// The same question asked of a subdirectory, so the root is not answering
	// differently from every other directory entry.
	sub, err := fs.LookupPath("/sub")
	if err != nil {
		t.Fatalf("LookupPath(/sub): %v", err)
	}
	if !sub.IsDirectory {
		t.Error("LookupPath reports /sub as not a directory")
	}
}

// TestWalkDirWithInodeAvoidsTheSecondRead is the claim that makes this shape
// worth having: a callback needing the whole inode used to read it again, and
// now does not. Stated as a relationship rather than a pinned count, because
// the absolute number is a property of the fixture and the ratio is the
// property of the code.
func TestWalkDirWithInodeAvoidsTheSecondRead(t *testing.T) {
	img := buildNestedDirFixture(t)

	count := func(run func(*FS) error) int64 {
		t.Helper()
		counter := &countingReaderAt{r: bytes.NewReader(img)}
		fs, err := OpenWithOptions(counter, Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		counter.count.Store(0)
		if err := run(fs); err != nil {
			t.Fatalf("walk: %v", err)
		}
		return counter.count.Load()
	}

	// The shape a caller needing the whole inode had before: the walk reads it
	// to decide whether to descend, and the callback reads it again.
	twice := count(func(fs *FS) error {
		return fs.WalkDir(RootInode, func(_ string, e DirEntry) error {
			_, err := fs.ReadInode(e.Inode)
			return err
		})
	})

	// The same information, one read.
	once := count(func(fs *FS) error {
		return fs.WalkDirWithInode(RootInode, func(_ string, _ DirEntry, inode Inode) error {
			if inode.Number == 0 {
				t.Error("an inode the walk read came through as zero")
			}
			return nil
		})
	})

	if once >= twice {
		t.Errorf("WalkDirWithInode cost %d reads and walk-then-read cost %d; "+
			"handing the inode over is meant to remove the second read", once, twice)
	}
}
