package libext

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// Each test below builds an image that is readable but not well formed in one
// way, and checks both halves of the Strict contract: strict refuses it with
// the named check, and the default options read it as before.

// strictFixture is a 1 KiB-block image with the extents and filetype features
// and a root directory at block 100 listing the given entries after "." and
// "..". Data blocks for the tests start at 200.
func strictFixture(t *testing.T, entries ...[]byte) []byte {
	t.Helper()
	cfg := defaultSBConfig()
	cfg.incompat |= IncompatExtents
	img := buildTestImage(t, cfg)

	var dir []byte
	dir = append(dir, dirent(RootInode, 12, FileTypeDir, ".")...)
	dir = append(dir, dirent(RootInode, 12, FileTypeDir, "..")...)
	for _, e := range entries {
		dir = append(dir, e...)
	}
	// The last record reaches the end of the block.
	last := len(dir) - len(entries[len(entries)-1])
	binary.LittleEndian.PutUint16(dir[last+4:], uint16(testBlockSize-last))
	writeTestBlock(img, 100, dir)

	writeTestInode(img, RootInode, ModeDir|0o755, testBlockSize, InodeFlagExtents,
		inodeRoot(extentHeader(1, 4, 0), extentLeafEntry(0, 1, 100)))
	return img
}

// testInodeOffset is where a fixture inode's raw bytes start.
func testInodeOffset(num uint32) int {
	return testInodeTableBlock*testBlockSize + int(num-1)*testInodeSize
}

// extentFile writes a regular file of size bytes mapped by the given leaf
// entries.
func extentFile(img []byte, num uint32, size uint32, leaves ...[]byte) {
	parts := append([][]byte{extentHeader(uint16(len(leaves)), 4, 0)}, leaves...)
	writeTestInode(img, num, ModeRegular|0o644, size, InodeFlagExtents, inodeRoot(parts...))
}

func strictAndDefault(t *testing.T, img []byte) (strict, lax *FS) {
	t.Helper()
	return openFixture(t, img, Options{Strict: true}), openFixture(t, img, Options{})
}

func TestStrictRejectsHole(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	extentFile(img, 20, 3*testBlockSize, extentLeafEntry(0, 1, 200), extentLeafEntry(2, 1, 201))
	strict, lax := strictAndDefault(t, img)

	_, err := strict.ReadFile(20)
	assertStrict(t, err, StrictHole, false)
	if _, err := lax.ReadFile(20); err != nil {
		t.Errorf("default ReadFile: %v", err)
	}
}

func TestStrictRejectsUnwrittenExtent(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	extentFile(img, 20, testBlockSize, extentLeafEntry(0, ExtentInitMaxLen+1, 200))
	strict, lax := strictAndDefault(t, img)

	f, err := strict.Open(20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.ReadAt(make([]byte, 10), 0)
	assertStrict(t, err, StrictUnwritten, false)
	if _, err := lax.ReadFile(20); err != nil {
		t.Errorf("default ReadFile: %v", err)
	}
}

func TestStrictRejectsShortMap(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	extentFile(img, 20, 3*testBlockSize, extentLeafEntry(0, 1, 200))
	strict, lax := strictAndDefault(t, img)

	_, err := strict.ReadFile(20)
	assertStrict(t, err, StrictShortMap, false)
	if _, err := lax.ReadFile(20); err != nil {
		t.Errorf("default ReadFile: %v", err)
	}
}

func TestStrictRejectsExtentPastFilesystem(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	// The fixture has 1024 blocks; this run covers 1023 and 1024.
	extentFile(img, 20, 2*testBlockSize, extentLeafEntry(0, 2, 1023))
	strict, lax := strictAndDefault(t, img)

	_, err := strict.Extents(20)
	assertStrict(t, err, StrictExtentRange, false)
	if _, err := lax.Extents(20); err != nil {
		t.Errorf("default Extents: %v", err)
	}
}

func TestStrictRejectsDuplicateName(t *testing.T) {
	img := strictFixture(t,
		dirent(20, 12, FileTypeRegular, "same"),
		dirent(21, 12, FileTypeRegular, "same"))
	extentFile(img, 20, 0)
	extentFile(img, 21, 0)
	strict, lax := strictAndDefault(t, img)

	_, err := strict.ListDir(RootInode)
	assertStrict(t, err, StrictDuplicateName, false)
	if _, err := lax.ListDir(RootInode); err != nil {
		t.Errorf("default ListDir: %v", err)
	}
}

func TestStrictRejectsUnlinkedNamedInode(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	extentFile(img, 20, 0)
	binary.LittleEndian.PutUint16(img[testInodeOffset(20)+InodeOffLinksCount:], 0)
	strict, lax := strictAndDefault(t, img)

	err := strict.WalkDir(RootInode, func(string, DirEntry) error { return nil })
	assertStrict(t, err, StrictUnlinked, false)
	if err := lax.WalkDir(RootInode, func(string, DirEntry) error { return nil }); err != nil {
		t.Errorf("default WalkDir: %v", err)
	}
}

func TestStrictRejectsDeletedNamedInode(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	extentFile(img, 20, 0)
	binary.LittleEndian.PutUint32(img[testInodeOffset(20)+InodeOffDtime:], 1700000000)
	strict, lax := strictAndDefault(t, img)

	err := strict.WalkDir(RootInode, func(string, DirEntry) error { return nil })
	assertStrict(t, err, StrictDeleted, false)
	if err := lax.WalkDir(RootInode, func(string, DirEntry) error { return nil }); err != nil {
		t.Errorf("default WalkDir: %v", err)
	}
}

func TestStrictRejectsBlockMappedInodeOnExtentsVolume(t *testing.T) {
	img := strictFixture(t, dirent(20, 12, FileTypeRegular, "f"))
	writeTestInode(img, 20, ModeRegular|0o644, testBlockSize, 0, classicRoot([]uint32{200}, 0, 0, 0))
	strict, lax := strictAndDefault(t, img)

	_, err := strict.ReadFile(20)
	assertStrict(t, err, StrictBlockMapped, false)
	err = strict.WalkDir(RootInode, func(string, DirEntry) error { return nil })
	assertStrict(t, err, StrictBlockMapped, false)
	if _, err := lax.ReadFile(20); err != nil {
		t.Errorf("default ReadFile: %v", err)
	}
}

// The kernel gives the EXTENTS flag only to regular files, directories and
// slow symlinks, so the check must pass device nodes and fast symlinks.
func TestStrictAcceptsSpecialInodesWithoutExtents(t *testing.T) {
	img := strictFixture(t,
		dirent(20, 12, FileTypeChr, "c"),
		dirent(21, 12, FileTypeSymlink, "l"),
		dirent(22, 12, FileTypeFIFO, "p"))
	writeTestInode(img, 20, ModeChar|0o600, 0, 0, nil)
	root := make([]byte, 60)
	copy(root, "/target")
	writeTestInode(img, 21, ModeSymlink|0o777, 7, 0, root)
	writeTestInode(img, 22, ModeFIFO|0o600, 0, 0, nil)
	strict := openFixture(t, img, Options{Strict: true})

	err := strict.WalkDir(RootInode, func(p string, e DirEntry) error {
		if p == "/l" {
			f, err := strict.Open(e.Inode)
			if err != nil {
				return err
			}
			_, err = f.ReadLink()
			return err
		}
		return nil
	})
	if err != nil {
		t.Errorf("strict WalkDir over special inodes: %v", err)
	}
}

// The checksum-dependent shapes use the e2fsprogs image, whose checksums are
// all valid, so Strict's implied VerifyChecksums passes and only the damage
// under test is seen.
func TestStrictRejectsMissingTails(t *testing.T) {
	base := loadCsumImage(t)
	fs := openCsumImage(t, base, Options{})
	many := lookupIno(t, fs, "/many")
	manyExts, err := fs.InodeExtents(many, ExtentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sparse := lookupIno(t, fs, "/sparse.bin")
	meta, err := fs.MetadataBlocks(sparse.Number)
	if err != nil || len(meta) != 1 {
		t.Fatalf("MetadataBlocks: %v %v", meta, err)
	}

	t.Run("directory block without a tail", func(t *testing.T) {
		img := bytes.Clone(base)
		tail := int(manyExts[0].PhysicalBlock+1)*1024 - DirTailSize
		clear(img[tail : tail+DirTailSize])

		_, err := openCsumImage(t, img, Options{Strict: true}).ListDir(many.Number)
		assertStrict(t, err, StrictDirTail, false)
		if _, err := openCsumImage(t, img, Options{}).ListDir(many.Number); err != nil {
			t.Errorf("default ListDir: %v", err)
		}
	})

	t.Run("extent block with no room for a tail", func(t *testing.T) {
		img := bytes.Clone(base)
		// eh_max large enough that the tail would fall past the block, while
		// eh_entries still fits it.
		binary.LittleEndian.PutUint16(img[meta[0]*1024+4:], 85)

		_, err := openCsumImage(t, img, Options{Strict: true}).Extents(sparse.Number)
		assertStrict(t, err, StrictExtentTail, false)
		if _, err := openCsumImage(t, img, Options{}).Extents(sparse.Number); err != nil {
			t.Errorf("default Extents: %v", err)
		}
	})
}

func TestStrictErrorFormat(t *testing.T) {
	err := strictErr(StrictHole, 12, 34, "hole of %d blocks", 2)
	if got, want := err.Error(), "ext strict check failed: hole: inode 12 block 34: hole of 2 blocks"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// deepenExtentTree moves an inode's in-inode extent leaf into a block of its
// own, so the inode's map takes one block read to resolve, and restamps the
// checksums it touches. It returns the leaf block, which is the last zero block
// of the image.
func deepenExtentTree(t *testing.T, img []byte, ino uint32) uint64 {
	t.Helper()
	fs := openCsumImage(t, img, Options{})
	inode, err := fs.ReadInode(ino)
	if err != nil {
		t.Fatal(err)
	}
	if depth := binary.LittleEndian.Uint16(inode.BlockRaw[6:]); depth != 0 {
		t.Fatalf("inode %d extent tree has depth %d, want 0", ino, depth)
	}
	bs := int(fs.sb.BlockSize)
	leaf := fs.sb.BlocksCount - 1
	for ; leaf > 0 && !bytes.Equal(img[int(leaf)*bs:int(leaf+1)*bs], make([]byte, bs)); leaf-- {
	}
	seed := fs.csumSeed()

	n := binary.LittleEndian.Uint16(inode.BlockRaw[2:])
	slots := uint16((bs - ExtentHeaderSize - ExtentTailSize) / ExtentEntrySize)
	blk := img[int(leaf)*bs : int(leaf+1)*bs]
	copy(blk, extentHeader(n, slots, 0))
	copy(blk[ExtentHeaderSize:], inode.BlockRaw[ExtentHeaderSize:ExtentHeaderSize+int(n)*ExtentEntrySize])
	binary.LittleEndian.PutUint32(blk[ExtentHeaderSize+int(slots)*ExtentEntrySize:],
		ExtentTailChecksum(seed, ino, inode.Generation, blk))

	tb, off, err := fs.inodeTableLocation(ino)
	if err != nil {
		t.Fatal(err)
	}
	raw := img[int(tb)*bs+int(off):][:fs.sb.InodeSize]
	root := raw[InodeOffBlockRaw : InodeOffBlockRaw+BlockMapSize]
	clear(root)
	copy(root, extentHeader(1, 4, 1))
	copy(root[ExtentHeaderSize:], extentIndexEntry(0, leaf))
	lo, hi, hasHi := InodeChecksum(seed, ino, inode.Generation, raw, len(raw))
	binary.LittleEndian.PutUint16(raw[InodeOffChecksumLo:], lo)
	if hasHi {
		binary.LittleEndian.PutUint16(raw[InodeOffChecksumHi:], hi)
	}
	return leaf
}

// blockReads counts the reads that cover one byte offset.
type blockReads struct {
	r   *bytes.Reader
	off int64
	n   int
}

func (c *blockReads) ReadAt(p []byte, off int64) (int, error) {
	if off <= c.off && c.off < off+int64(len(p)) {
		c.n++
	}
	return c.r.ReadAt(p, off)
}

func (c *blockReads) Size() int64 { return c.r.Size() }

var _ io.ReaderAt = (*blockReads)(nil)

// A directory-tail error names the physical block from the map the directory
// was just read through. Building the error must not resolve the map again,
// and must report the same check, inode, block and detail as before.
func TestStrictDirTailErrorReusesBlockMap(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{})
	many := lookupIno(t, fs, "/many")
	exts, err := fs.InodeExtents(many, ExtentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dirBlock := exts[0].PhysicalBlock
	leaf := deepenExtentTree(t, img, many.Number)
	if _, err := openCsumImage(t, img, Options{Strict: true}).ListDir(many.Number); err != nil {
		t.Fatalf("strict ListDir of the deepened directory: %v", err)
	}

	tail := int(dirBlock+1)*int(fs.sb.BlockSize) - DirTailSize
	clear(img[tail : tail+DirTailSize])
	reads := &blockReads{r: bytes.NewReader(img), off: int64(leaf) * int64(fs.sb.BlockSize)}
	strict, err := OpenWithOptions(reads, Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = strict.ListDir(many.Number)
	assertStrict(t, err, StrictDirTail, false)
	var se *StrictError
	errors.As(err, &se)
	want := StrictError{Check: StrictDirTail, Inode: many.Number, Block: dirBlock,
		Detail: "logical block 0 has no checksum tail"}
	if *se != want {
		t.Errorf("error = %+v, want %+v", *se, want)
	}
	if reads.n != 1 {
		t.Errorf("ListDir read the extent leaf %d times, want 1: the error report resolved the map again", reads.n)
	}
}
