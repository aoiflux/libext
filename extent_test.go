package libext

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// fixture helpers
// ---------------------------------------------------------------------------

// writeTestBlock places raw content at a block number in the fixture image.
func writeTestBlock(img []byte, block uint64, data []byte) {
	copy(img[block*testBlockSize:], data)
}

// classicRoot builds the 60-byte ext2-style pointer array.
func classicRoot(direct []uint32, indirect, double, triple uint32) []byte {
	root := make([]byte, 60)
	for i, p := range direct {
		binary.LittleEndian.PutUint32(root[i*4:], p)
	}
	binary.LittleEndian.PutUint32(root[12*4:], indirect)
	binary.LittleEndian.PutUint32(root[13*4:], double)
	binary.LittleEndian.PutUint32(root[14*4:], triple)
	return root
}

// pointerBlock renders a block of 32-bit block pointers.
func pointerBlock(ptrs ...uint32) []byte {
	b := make([]byte, testBlockSize)
	for i, p := range ptrs {
		binary.LittleEndian.PutUint32(b[i*4:], p)
	}
	return b
}

func openFixture(t testing.TB, img []byte, opts Options) *FS {
	t.Helper()
	fs, err := OpenWithOptions(bytes.NewReader(img), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return fs
}

// assertExtents compares against the full expected map, so a change in
// normalization cannot pass unnoticed.
func assertExtents(t testing.TB, got, want []Extent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d extents, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extent %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// extent-mapped inodes
// ---------------------------------------------------------------------------

func TestExtentsExtentMappedWithHoles(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// Two written runs separated by a hole, inside a file whose tail is a hole.
	root := inodeRoot(
		extentHeader(2, 4, 0),
		extentLeafEntry(0, 2, 100),
		extentLeafEntry(4, 2, 200),
	)
	writeTestInode(img, 20, 0x81A4, 8*testBlockSize, inodeFlagExtents, root)

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(20)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	assertExtents(t, got, []Extent{
		{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 2},
		{LogicalBlock: 2, Blocks: 2, Flags: ExtentSparse},
		{LogicalBlock: 4, PhysicalBlock: 200, Blocks: 2},
		{LogicalBlock: 6, Blocks: 2, Flags: ExtentSparse},
	})
}

func TestExtentsOmitSparse(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := inodeRoot(
		extentHeader(2, 4, 0),
		extentLeafEntry(0, 2, 100),
		extentLeafEntry(4, 2, 200),
	)
	writeTestInode(img, 20, 0x81A4, 8*testBlockSize, inodeFlagExtents, root)

	fs := openFixture(t, img, Options{})

	got, err := fs.ExtentsWithOptions(20, ExtentOptions{OmitSparse: true})
	if err != nil {
		t.Fatalf("ExtentsWithOptions: %v", err)
	}
	assertExtents(t, got, []Extent{
		{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 2},
		{LogicalBlock: 4, PhysicalBlock: 200, Blocks: 2},
	})
}

func TestExtentsNoCoalesceKeepsOnDiskShape(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// Two records that are physically adjacent: mkfs splits long runs at 32768
	// blocks, and a caller measuring real fragmentation needs to see the split.
	root := inodeRoot(
		extentHeader(2, 4, 0),
		extentLeafEntry(0, 2, 100),
		extentLeafEntry(2, 2, 102),
	)
	writeTestInode(img, 20, 0x81A4, 4*testBlockSize, inodeFlagExtents, root)

	fs := openFixture(t, img, Options{})

	merged, err := fs.Extents(20)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	assertExtents(t, merged, []Extent{{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 4}})

	split, err := fs.ExtentsWithOptions(20, ExtentOptions{NoCoalesce: true})
	if err != nil {
		t.Fatalf("ExtentsWithOptions: %v", err)
	}
	assertExtents(t, split, []Extent{
		{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 2},
		{LogicalBlock: 2, PhysicalBlock: 102, Blocks: 2},
	})
}

func TestExtentsUnwrittenKeepsPhysicalLocation(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// Preallocated run: reads as zeros, but the blocks are allocated and may
	// still hold whatever occupied them before, so the location must survive.
	root := inodeRoot(
		extentHeader(1, 4, 0),
		extentLeafEntry(0, 0x8000|2, 300),
	)
	writeTestInode(img, 21, 0x81A4, 2*testBlockSize, inodeFlagExtents, root)

	// Put recognisable bytes where the preallocated run points.
	writeTestBlock(img, 300, bytes.Repeat([]byte{0xAA}, testBlockSize))

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(21)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	assertExtents(t, got, []Extent{
		{LogicalBlock: 0, PhysicalBlock: 300, Blocks: 2, Flags: ExtentUnwritten},
	})
	if !got[0].Unwritten() {
		t.Error("Unwritten() = false")
	}

	// The file interface must still read zeros: the location is exposed for
	// analysis, not served as file content.
	data, err := fs.ReadFile(21)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, make([]byte, 2*testBlockSize)) {
		t.Error("preallocated run did not read as zeros")
	}
}

// ---------------------------------------------------------------------------
// classic block maps produce the same shape
// ---------------------------------------------------------------------------

func TestExtentsClassicDirectCoalesces(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 101, 102}, 0, 0, 0)
	writeTestInode(img, 22, 0x81A4, 3*testBlockSize, 0, root)

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(22)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	assertExtents(t, got, []Extent{{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 3}})
}

func TestExtentsClassicHoleInMiddle(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 0, 102}, 0, 0, 0)
	writeTestInode(img, 23, 0x81A4, 3*testBlockSize, 0, root)

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(23)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	assertExtents(t, got, []Extent{
		{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 1},
		{LogicalBlock: 1, Blocks: 1, Flags: ExtentSparse},
		{LogicalBlock: 2, PhysicalBlock: 102, Blocks: 1},
	})
}

func TestExtentsClassicIndirectCoalescesAcrossBoundary(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	direct := make([]uint32, 12)
	for i := range direct {
		direct[i] = uint32(100 + i) // 100..111
	}
	root := classicRoot(direct, 50, 0, 0)
	writeTestBlock(img, 50, pointerBlock(112, 113))
	writeTestInode(img, 24, 0x81A4, 14*testBlockSize, 0, root)

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(24)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	// The run continues across the direct/indirect boundary and must present as
	// a single extent, exactly as an ext4 extent would.
	assertExtents(t, got, []Extent{{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 14}})
}

func TestMetadataBlocksReportsIndirectBlock(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	direct := make([]uint32, 12)
	for i := range direct {
		direct[i] = uint32(100 + i)
	}
	root := classicRoot(direct, 50, 0, 0)
	writeTestBlock(img, 50, pointerBlock(112, 113))
	writeTestInode(img, 24, 0x81A4, 14*testBlockSize, 0, root)

	fs := openFixture(t, img, Options{})

	meta, err := fs.MetadataBlocks(24)
	if err != nil {
		t.Fatalf("MetadataBlocks: %v", err)
	}
	if len(meta) != 1 || meta[0] != 50 {
		t.Errorf("MetadataBlocks = %v, want [50]", meta)
	}
}

func TestMetadataBlocksReportsExtentIndexBlock(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	leaf := append(extentHeader(1, 84, 0), extentLeafEntry(0, 2, 100)...)
	writeTestBlock(img, 60, leaf)

	root := inodeRoot(extentHeader(1, 4, 1), extentIndexEntry(0, 60))
	writeTestInode(img, 25, 0x81A4, 2*testBlockSize, inodeFlagExtents, root)

	fs := openFixture(t, img, Options{})

	meta, err := fs.MetadataBlocks(25)
	if err != nil {
		t.Fatalf("MetadataBlocks: %v", err)
	}
	if len(meta) != 1 || meta[0] != 60 {
		t.Errorf("MetadataBlocks = %v, want [60]", meta)
	}
}

// ---------------------------------------------------------------------------
// byte ranges
// ---------------------------------------------------------------------------

func TestDataRunsClampToFileSize(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 101, 102}, 0, 0, 0)
	// Two and a half blocks: the last run must stop at the file's real end.
	writeTestInode(img, 26, 0x81A4, 2*testBlockSize+500, 0, root)

	fs := openFixture(t, img, Options{})

	runs, err := fs.DataRuns(26)
	if err != nil {
		t.Fatalf("DataRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1: %+v", len(runs), runs)
	}
	want := ByteRange{
		FileOffset: 0,
		DiskOffset: 100 * testBlockSize,
		Length:     2*testBlockSize + 500,
	}
	if runs[0] != want {
		t.Errorf("run = %+v, want %+v", runs[0], want)
	}

	var total int64
	for _, r := range runs {
		total += r.Length
	}
	if total != int64(2*testBlockSize+500) {
		t.Errorf("run lengths total %d, want the file size %d", total, 2*testBlockSize+500)
	}
}

func TestDataRunsIncludeBaseOffset(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100}, 0, 0, 0)
	writeTestInode(img, 27, 0x81A4, testBlockSize, 0, root)

	const partitionStart = 1 << 20
	fs := openFixture(t, img, Options{BaseOffset: partitionStart})

	runs, err := fs.DataRuns(27)
	if err != nil {
		t.Fatalf("DataRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if want := int64(partitionStart + 100*testBlockSize); runs[0].DiskOffset != want {
		t.Errorf("DiskOffset = %d, want %d", runs[0].DiskOffset, want)
	}

	// Extents stay volume-relative; only the byte form is image-absolute.
	exts, err := fs.Extents(27)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	if exts[0].PhysicalBlock != 100 {
		t.Errorf("PhysicalBlock = %d, want 100 (volume-relative)", exts[0].PhysicalBlock)
	}
}

func TestDataRunsMarkSparseWithoutLocation(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 0, 102}, 0, 0, 0)
	writeTestInode(img, 28, 0x81A4, 3*testBlockSize, 0, root)

	fs := openFixture(t, img, Options{BaseOffset: 4096})

	runs, err := fs.DataRuns(28)
	if err != nil {
		t.Fatalf("DataRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3: %+v", len(runs), runs)
	}
	if !runs[1].Sparse {
		t.Error("middle run is not marked sparse")
	}
	if runs[1].DiskOffset != 0 {
		t.Errorf("sparse run has DiskOffset %d, want 0: a hole has no location", runs[1].DiskOffset)
	}
}

// ---------------------------------------------------------------------------
// read path
// ---------------------------------------------------------------------------

func TestReadAtZeroesHolesInCallerBuffer(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 0, 102}, 0, 0, 0)
	writeTestInode(img, 29, 0x81A4, 3*testBlockSize, 0, root)
	writeTestBlock(img, 100, bytes.Repeat([]byte{0x11}, testBlockSize))
	writeTestBlock(img, 102, bytes.Repeat([]byte{0x22}, testBlockSize))

	fs := openFixture(t, img, Options{})

	f, err := fs.Open(29)
	if err != nil {
		t.Fatalf("Open inode: %v", err)
	}

	// The buffer belongs to the caller and is not zeroed. A hole must read as
	// zeros, not as whatever the caller left behind.
	buf := bytes.Repeat([]byte{0xFF}, testBlockSize)
	n, err := f.ReadAt(buf, testBlockSize)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != testBlockSize {
		t.Fatalf("read %d bytes, want %d", n, testBlockSize)
	}
	if !bytes.Equal(buf, make([]byte, testBlockSize)) {
		t.Errorf("hole read back non-zero bytes: %x...", buf[:16])
	}
}

func TestReadFileAcrossHoles(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 0, 102}, 0, 0, 0)
	writeTestInode(img, 30, 0x81A4, 3*testBlockSize, 0, root)
	writeTestBlock(img, 100, bytes.Repeat([]byte{0x11}, testBlockSize))
	writeTestBlock(img, 102, bytes.Repeat([]byte{0x22}, testBlockSize))

	fs := openFixture(t, img, Options{})

	data, err := fs.ReadFile(30)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	want := make([]byte, 0, 3*testBlockSize)
	want = append(want, bytes.Repeat([]byte{0x11}, testBlockSize)...)
	want = append(want, make([]byte, testBlockSize)...)
	want = append(want, bytes.Repeat([]byte{0x22}, testBlockSize)...)

	if !bytes.Equal(data, want) {
		t.Error("file content across a hole does not match")
	}
}

// countingReaderAt records how many reads a code path issues.
type countingReaderAt struct {
	r     io.ReaderAt
	count atomic.Int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.count.Add(1)
	return c.r.ReadAt(p, off)
}

func (c *countingReaderAt) Size() int64 {
	return c.r.(*bytes.Reader).Size()
}

func TestReadPathResolvesBlockMapOnce(t *testing.T) {
	// The old path re-read the indirect block for every logical block it
	// resolved, which is quadratic across a file. Resolving the map once makes
	// the read count linear in the number of data blocks.
	const dataBlocks = 100

	img := buildTestImage(t, defaultSBConfig())

	direct := make([]uint32, 12)
	for i := range direct {
		direct[i] = uint32(100 + i)
	}
	indirect := make([]uint32, dataBlocks-12)
	for i := range indirect {
		indirect[i] = uint32(112 + i)
	}
	root := classicRoot(direct, 50, 0, 0)
	writeTestBlock(img, 50, pointerBlock(indirect...))
	writeTestInode(img, 31, 0x81A4, dataBlocks*testBlockSize, 0, root)

	counter := &countingReaderAt{r: bytes.NewReader(img)}
	fs, err := OpenWithOptions(counter, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	counter.count.Store(0)
	if _, err := fs.ReadFile(31); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// One inode read, one indirect block read, one read per data block.
	const budget = dataBlocks + 5
	if got := counter.count.Load(); got > budget {
		t.Errorf("read the image %d times for %d data blocks; want at most %d "+
			"(the block map is being resolved more than once)", got, dataBlocks, budget)
	}
}

// ---------------------------------------------------------------------------
// malformed maps
// ---------------------------------------------------------------------------

func TestExtentsRejectOverlappingRuns(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// Two records claiming the same logical blocks: which physical location a
	// logical block maps to is ambiguous, so the map cannot be trusted.
	root := inodeRoot(
		extentHeader(2, 4, 0),
		extentLeafEntry(0, 4, 100),
		extentLeafEntry(2, 4, 200),
	)
	writeTestInode(img, 32, 0x81A4, 6*testBlockSize, inodeFlagExtents, root)

	fs := openFixture(t, img, Options{})
	if _, err := fs.Extents(32); err == nil {
		t.Fatal("overlapping extents were accepted")
	}

	// Permissive clips the overlap and keeps going.
	permissive := openFixture(t, img, Options{Permissive: true})
	got, err := permissive.Extents(32)
	if err != nil {
		t.Fatalf("permissive Extents: %v", err)
	}
	assertExtents(t, got, []Extent{
		{LogicalBlock: 0, PhysicalBlock: 100, Blocks: 4},
		{LogicalBlock: 4, PhysicalBlock: 202, Blocks: 2},
	})
	if !hasWarning(permissive.Warnings(), WarnDegradedRead) {
		t.Error("no warning recorded for the clipped overlap")
	}
}

func TestExtentsRefusedUnderBigalloc(t *testing.T) {
	// Open refuses BIGALLOC outright; under Permissive the image opens, so the
	// extent API must refuse rather than report cluster-scaled offsets.
	cfg := defaultSBConfig()
	cfg.roCompat |= featureRoCompatBigalloc
	img := buildTestImage(t, cfg)

	root := classicRoot([]uint32{100}, 0, 0, 0)
	writeTestInode(img, 33, 0x81A4, testBlockSize, 0, root)

	fs := openFixture(t, img, Options{Permissive: true})
	if _, err := fs.Extents(33); err == nil {
		t.Fatal("Extents returned a map for a BIGALLOC filesystem")
	}
}

func TestExtentsEmptyForFastSymlink(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	// A fast symlink stores its target in the block map itself. Treating that
	// as a pointer array would report the target's bytes as block numbers.
	target := []byte("/etc/passwd")
	root := make([]byte, 60)
	copy(root, target)
	writeTestInode(img, 34, 0xA1FF, uint32(len(target)), 0, root)

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(34)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("fast symlink reported %d extents, want none: %+v", len(got), got)
	}
}

func TestExtentsEmptyForZeroLengthFile(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	writeTestInode(img, 35, 0x81A4, 0, 0, nil)

	fs := openFixture(t, img, Options{})

	got, err := fs.Extents(35)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty file reported %d extents, want none", len(got))
	}
}

func TestFileExtentsMatchFSExtents(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	root := classicRoot([]uint32{100, 0, 102}, 0, 0, 0)
	writeTestInode(img, 36, 0x81A4, 3*testBlockSize, 0, root)

	fs := openFixture(t, img, Options{})

	f, err := fs.Open(36)
	if err != nil {
		t.Fatalf("Open inode: %v", err)
	}

	viaFile, err := f.Extents()
	if err != nil {
		t.Fatalf("File.Extents: %v", err)
	}
	viaFS, err := fs.Extents(36)
	if err != nil {
		t.Fatalf("FS.Extents: %v", err)
	}
	assertExtents(t, viaFile, viaFS)

	if f.Inode().Number != 36 {
		t.Errorf("File.Inode().Number = %d, want 36", f.Inode().Number)
	}
}
