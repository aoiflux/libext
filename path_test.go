package libext

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

// Nested-tree fixture and the naming tests built on it.
//
// Every fixture the rest of the suite uses is one level deep, which is enough
// for listing but not for naming: in a one-level tree the root's ".." record
// points at the root, so a record's Inode and its ParentInode are both 2 and the
// two possible readings of ParentInode cannot be told apart. Nothing here works
// without a real subdirectory.

const (
	// Inode numbers in the nested fixture. 40 and 41 match buildDirFixture so
	// the two fixtures describe the same objects.
	nestedNotes = 40 // /notes.txt, also linked as /sub/alias.txt
	nestedGone  = 41 // unlinked: present in the inode table, named by nothing
	nestedSub   = 43 // /sub
	nestedDeep  = 44 // /sub/deep.txt

	nestedRootBlock = 200
	nestedSubBlock  = 201

	// testGeneration is written into notes.txt so a test can tell a real
	// generation from a zero one.
	testGeneration = 0xC0FFEE
)

// buildNestedDirFixture lays out root(2) -> sub(43) -> deep.txt(44), with
// notes.txt(40) also linked into sub as alias.txt.
//
// The hard link is what makes the "shallowest path wins" rule observable, and
// inode 41 is deliberately linked from nowhere so that "not reachable" has a
// subject.
func buildNestedDirFixture(t testing.TB) []byte {
	t.Helper()

	img := buildTestImage(t, defaultSBConfig())

	// Root directory: ".", "..", notes.txt, sub.
	var root []byte
	root = append(root, dirent(RootInode, 12, extDirentTypeDirectory, ".")...)
	root = append(root, dirent(RootInode, 12, extDirentTypeDirectory, "..")...)
	root = append(root, dirent(nestedNotes, 20, 1, "notes.txt")...)
	root = append(root, dirent(nestedSub, uint16(testBlockSize-44), extDirentTypeDirectory, "sub")...)
	writeTestBlock(img, nestedRootBlock, root)

	// sub: ".", "..", deep.txt, alias.txt (the second link to notes.txt).
	var sub []byte
	sub = append(sub, dirent(nestedSub, 12, extDirentTypeDirectory, ".")...)
	sub = append(sub, dirent(RootInode, 12, extDirentTypeDirectory, "..")...)
	sub = append(sub, dirent(nestedDeep, 20, 1, "deep.txt")...)
	sub = append(sub, dirent(nestedNotes, uint16(testBlockSize-44), 1, "alias.txt")...)
	writeTestBlock(img, nestedSubBlock, sub)

	writeTestInode(img, RootInode, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{nestedRootBlock}, 0, 0, 0))
	writeTestInode(img, nestedSub, inodeTypeDir|0o755, testBlockSize, 0,
		classicRoot([]uint32{nestedSubBlock}, 0, 0, 0))

	// notes.txt, with a full timestamp set and a non-zero generation.
	notes := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 1234)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 2)
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], 0x69e58838)
		binary.LittleEndian.PutUint32(raw[inodeOffAtime:], 0x69e58839)
		binary.LittleEndian.PutUint32(raw[inodeOffCtime:], 0x69e5883A)
		binary.LittleEndian.PutUint16(raw[inodeOffUIDLo:], 1000)
		binary.LittleEndian.PutUint16(raw[inodeOffGIDLo:], 1001)
		binary.LittleEndian.PutUint32(raw[inodeOffGeneration:], testGeneration)
	})
	copy(img[testInodeTableBlock*testBlockSize+(nestedNotes-1)*testInodeSize:], notes)

	// deep.txt.
	writeTestInode(img, nestedDeep, inodeTypeRegular|0o644, 99, 0, nil)

	// An unlinked inode: real in the table, named by no record anywhere.
	gone := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 4096)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 0)
		binary.LittleEndian.PutUint32(raw[inodeOffDtime:], 0x69e58900)
	})
	copy(img[testInodeTableBlock*testBlockSize+(nestedGone-1)*testInodeSize:], gone)

	return img
}

// ---------------------------------------------------------------------------
// ParentInode semantics
// ---------------------------------------------------------------------------

// TestListDirStampsParentInode pins the definition of ParentInode: the
// directory the record was read from, not the parent of the inode it names.
//
// The ".." record is the only place the two readings differ, which is why the
// dot entries are kept here.
func TestListDirStampsParentInode(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	entries, err := fs.ListDirEx(nestedSub, DirOptions{IncludeDotEntries: true})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}

	byName := map[string]DirEntry{}
	for _, e := range entries {
		byName[e.Name] = e
		if e.ParentInode != nestedSub {
			t.Errorf("entry %q has ParentInode %d, want %d (the directory it was read from)",
				e.Name, e.ParentInode, nestedSub)
		}
	}

	// The load-bearing case: ".." names the parent, but lives in sub.
	dotdot, ok := byName[".."]
	if !ok {
		t.Fatal(`".." record missing from listing`)
	}
	if dotdot.Inode != RootInode {
		t.Errorf(`".." Inode = %d, want %d (the parent it points at)`, dotdot.Inode, RootInode)
	}
	if dotdot.ParentInode != nestedSub {
		t.Errorf(`".." ParentInode = %d, want %d (the directory holding the record)`,
			dotdot.ParentInode, nestedSub)
	}

	// "." is where the two readings coincide.
	dot, ok := byName["."]
	if !ok {
		t.Fatal(`"." record missing from listing`)
	}
	if dot.Inode != nestedSub || dot.ParentInode != nestedSub {
		t.Errorf(`"." = {Inode:%d ParentInode:%d}, want both %d`, dot.Inode, dot.ParentInode, nestedSub)
	}
}

func TestListDirExFillsGeneration(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	find := func(entries []DirEntry, name string) DirEntry {
		t.Helper()
		for _, e := range entries {
			if e.Name == name {
				return e
			}
		}
		t.Fatalf("entry %q not found", name)
		return DirEntry{}
	}

	withMeta, err := fs.ListDirEx(RootInode, DirOptions{WithInodeMetadata: true})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}
	if got := find(withMeta, "notes.txt").Generation; got != testGeneration {
		t.Errorf("Generation = %#x, want %#x", got, testGeneration)
	}

	// Bare ListDir reads no inodes, so it cannot know the generation.
	bare, err := fs.ListDir(RootInode)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if got := find(bare, "notes.txt").Generation; got != 0 {
		t.Errorf("ListDir filled Generation = %#x; it reads no inodes and must leave it zero", got)
	}
}

func TestReadDirFillsGeneration(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	root, err := fs.GetRootDirectory()
	if err != nil {
		t.Fatalf("GetRootDirectory: %v", err)
	}
	entries, err := root.ReadDir()
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name != "notes.txt" {
			continue
		}
		if e.Generation != testGeneration {
			t.Errorf("Generation = %#x, want %#x", e.Generation, testGeneration)
		}
		if e.ParentInode != RootInode {
			t.Errorf("ParentInode = %d, want %d", e.ParentInode, RootInode)
		}
		return
	}
	t.Fatal("notes.txt not found")
}

// TestEnhancedListDirStampsParentInode covers the one listing path that does
// not route through ListDir. Without its own stamp it silently reports zero.
func TestEnhancedListDirStampsParentInode(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	entries, err := fs.EnhancedListDir(nestedSub)
	if err != nil {
		t.Fatalf("EnhancedListDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	for _, e := range entries {
		if e.ParentInode != nestedSub {
			t.Errorf("entry %q has ParentInode %d, want %d", e.Name, e.ParentInode, nestedSub)
		}
	}
}

// ---------------------------------------------------------------------------
// PathIndex
// ---------------------------------------------------------------------------

func TestPathIndexNamesReachableInodes(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ix, err := fs.BuildPathIndex()
	if err != nil {
		t.Fatalf("BuildPathIndex: %v", err)
	}

	want := map[uint32]string{
		RootInode:  "/",
		nestedSub:  "/sub",
		nestedDeep: "/sub/deep.txt",
	}
	for inode, wantPath := range want {
		got, err := ix.PathFor(inode)
		if err != nil {
			t.Errorf("PathFor(%d): %v", inode, err)
			continue
		}
		if got != wantPath {
			t.Errorf("PathFor(%d) = %q, want %q", inode, got, wantPath)
		}
	}
	if ix.Truncated() {
		t.Error("index reports truncated on an intact tree")
	}
}

// TestPathIndexFirstPathWinsBreadthFirst pins the tie-break for a hard link.
//
// notes.txt is linked at /notes.txt and at /sub/alias.txt. Breadth-first order
// is what makes the shallower one the answer, and makes it the same answer on
// every run.
func TestPathIndexFirstPathWinsBreadthFirst(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	for i := range 20 {
		ix, err := fs.BuildPathIndex()
		if err != nil {
			t.Fatalf("BuildPathIndex: %v", err)
		}
		got, err := ix.PathFor(nestedNotes)
		if err != nil {
			t.Fatalf("PathFor: %v", err)
		}
		if got != "/notes.txt" {
			t.Fatalf("run %d: PathFor(%d) = %q, want the shallower %q",
				i, nestedNotes, got, "/notes.txt")
		}
	}
}

func TestPathIndexPathsForReturnsEveryLink(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ix, err := fs.BuildPathIndex()
	if err != nil {
		t.Fatalf("BuildPathIndex: %v", err)
	}

	got := ix.PathsFor(nestedNotes)
	want := []string{"/notes.txt", "/sub/alias.txt"}
	if len(got) != len(want) {
		t.Fatalf("PathsFor = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PathsFor[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if paths := ix.PathsFor(nestedGone); paths != nil {
		t.Errorf("PathsFor(unreachable) = %q, want nil", paths)
	}
}

func TestPathIndexParentOf(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ix, err := fs.BuildPathIndex()
	if err != nil {
		t.Fatalf("BuildPathIndex: %v", err)
	}

	cases := []struct {
		inode, want uint32
	}{
		{nestedDeep, nestedSub},
		{nestedSub, RootInode},
		{nestedNotes, RootInode},
		{RootInode, RootInode}, // the root is its own parent
	}
	for _, c := range cases {
		got, ok := ix.ParentOf(c.inode)
		if !ok {
			t.Errorf("ParentOf(%d): not found", c.inode)
			continue
		}
		if got != c.want {
			t.Errorf("ParentOf(%d) = %d, want %d", c.inode, got, c.want)
		}
	}

	if _, ok := ix.ParentOf(nestedGone); ok {
		t.Errorf("ParentOf(%d) reported a parent for an unreachable inode", nestedGone)
	}
}

func TestPathIndexUnreachableInodeIsNotFound(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ix, err := fs.BuildPathIndex()
	if err != nil {
		t.Fatalf("BuildPathIndex: %v", err)
	}
	if _, err := ix.PathFor(nestedGone); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("PathFor(unreachable) error = %v, want ErrPathNotFound", err)
	}
}

// TestBuildPathIndexMatchesWalkDir is the test that keeps the two traversals
// honest. The index and the walk are separate implementations of "what does the
// tree contain", and having both is only safe while they agree.
func TestBuildPathIndexMatchesWalkDir(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ix, err := fs.BuildPathIndex()
	if err != nil {
		t.Fatalf("BuildPathIndex: %v", err)
	}

	err = fs.WalkDir(RootInode, func(p string, e DirEntry) error {
		paths := ix.PathsFor(e.Inode)
		for _, indexed := range paths {
			if indexed == p {
				return nil
			}
		}
		t.Errorf("walk reached %q (inode %d) but the index has %q", p, e.Inode, paths)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

// ---------------------------------------------------------------------------
// (*FS).PathFor
// ---------------------------------------------------------------------------

func TestPathForNamesDirectoriesAndFiles(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	cases := []struct {
		inode uint32
		want  string
	}{
		{RootInode, "/"},
		{nestedSub, "/sub"},           // upward ".." walk
		{nestedDeep, "/sub/deep.txt"}, // downward walk
		{nestedNotes, "/notes.txt"},
	}
	for _, c := range cases {
		got, err := fs.PathFor(c.inode)
		if err != nil {
			t.Errorf("PathFor(%d): %v", c.inode, err)
			continue
		}
		if got != c.want {
			t.Errorf("PathFor(%d) = %q, want %q", c.inode, got, c.want)
		}
	}
}

// TestPathForWalksUpwardForDirectories proves the two code paths exist and
// differ in cost. It asserts a relationship rather than a pinned read count, so
// it does not break on an unrelated change to how directories are read.
func TestPathForWalksUpwardForDirectories(t *testing.T) {
	img := buildNestedDirFixture(t)

	measure := func(inode uint32) int64 {
		t.Helper()
		counter := &countingReaderAt{r: bytes.NewReader(img)}
		fs, err := OpenWithOptions(counter, Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		counter.count.Store(0)
		if _, err := fs.PathFor(inode); err != nil {
			t.Fatalf("PathFor(%d): %v", inode, err)
		}
		return counter.count.Load()
	}

	dirReads := measure(nestedSub)   // ".." upward: two listings
	fileReads := measure(nestedDeep) // no back-pointer: the tree is walked
	if dirReads >= fileReads {
		t.Errorf("naming a directory took %d reads and naming a file took %d; "+
			"the directory case is supposed to be the cheap upward walk", dirReads, fileReads)
	}
}

func TestPathForUnreachableInodeIsNotFound(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	if _, err := fs.PathFor(nestedGone); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("PathFor(unlinked) error = %v, want ErrPathNotFound", err)
	}
}

func TestPathForRejectsInvalidInode(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	for _, num := range []uint32{0, fs.Superblock().InodesCount + 1} {
		if _, err := fs.PathFor(num); !errors.Is(err, ErrInvalidInode) {
			t.Errorf("PathFor(%d) error = %v, want ErrInvalidInode", num, err)
		}
	}
}

// TestPathForSurvivesDirectoryCycle: a ".." chain that never reaches the root
// must produce an error rather than spinning to the depth cap or forever.
func TestPathForSurvivesDirectoryCycle(t *testing.T) {
	img := buildNestedDirFixture(t)

	// Point sub's ".." at sub itself. The upward walk then has nowhere to go.
	var sub []byte
	sub = append(sub, dirent(nestedSub, 12, extDirentTypeDirectory, ".")...)
	sub = append(sub, dirent(nestedSub, 12, extDirentTypeDirectory, "..")...)
	sub = append(sub, dirent(nestedDeep, 20, 1, "deep.txt")...)
	sub = append(sub, dirent(nestedNotes, uint16(testBlockSize-44), 1, "alias.txt")...)
	writeTestBlock(img, nestedSubBlock, sub)

	fs := openFixture(t, img, Options{})

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = fs.PathFor(nestedSub)
	}()
	<-done

	if err == nil {
		t.Fatal("a self-referential \"..\" chain returned a path instead of an error")
	}
	if !errors.Is(err, ErrUnsupportedLayout) {
		t.Errorf("error = %v, want ErrUnsupportedLayout", err)
	}
}

// TestBuildPathIndexHonoursCancellation checks the pacing counter is tested
// before it advances; otherwise the first check would fall at entry 1024 and a
// small fixture would never observe a cancelled context at all.
func TestBuildPathIndexHonoursCancellation(t *testing.T) {
	fs := openFixture(t, buildNestedDirFixture(t), Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fs.BuildPathIndexContext(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("BuildPathIndexContext error = %v, want context.Canceled", err)
	}
}

// TestPathIndexDirsAgreeWithPaths pins an internal invariant that a damaged
// tree can otherwise break: for a directory reached twice, the path recorded in
// dirs must be the same one recorded in paths.
//
// They are read by different callers - the deleted scan iterates dirs, PathFor
// answers from paths - so a disagreement would have the two report different
// names for the same directory.
func TestPathIndexDirsAgreeWithPaths(t *testing.T) {
	img := buildNestedDirFixture(t)

	// Link sub a second time, as /again, so it is reached twice.
	var root []byte
	root = append(root, dirent(RootInode, 12, extDirentTypeDirectory, ".")...)
	root = append(root, dirent(RootInode, 12, extDirentTypeDirectory, "..")...)
	root = append(root, dirent(nestedSub, 12, extDirentTypeDirectory, "sub")...)
	root = append(root, dirent(nestedSub, uint16(testBlockSize-36), extDirentTypeDirectory, "again")...)
	writeTestBlock(img, nestedRootBlock, root)

	fs := openFixture(t, img, Options{})

	ix, err := fs.BuildPathIndex()
	if err != nil {
		t.Fatalf("BuildPathIndex: %v", err)
	}

	for inode, dirPath := range ix.dirs {
		p, ok := ix.paths[inode]
		if !ok {
			t.Errorf("inode %d is in dirs (%q) but absent from paths", inode, dirPath)
			continue
		}
		if p != dirPath {
			t.Errorf("inode %d: dirs says %q, paths says %q", inode, dirPath, p)
		}
	}

	if got := ix.PathsFor(nestedSub); len(got) != 2 {
		t.Errorf("PathsFor(sub) = %q, want both links recorded", got)
	}
	if !ix.Truncated() {
		t.Error("a directory reached twice did not mark the index truncated")
	}
}
