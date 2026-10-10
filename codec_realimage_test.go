package libext

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// The codecs are checked here against an image every checksum of which was
// written by e2fsprogs 1.47.0, built by testdata/mkcsumimg.sh. Agreeing with
// it is agreement with the reference implementation, which is a stronger claim
// than agreeing with this package's own reading of the format. Where dumpe2fs
// printed a value, the expected value below is copied from its output rather
// than read back from the image.

const (
	csumImageBlocks = 8192
	csumImageGroups = 8
)

// csumImageGDChecksums is bg_checksum per group as dumpe2fs printed it.
var csumImageGDChecksums = [csumImageGroups]uint16{
	0xbf86, 0xf5a6, 0x089e, 0x14cd, 0x9d14, 0x36d7, 0x8416, 0xa673,
}

func loadCsumImage(t testing.TB) []byte {
	t.Helper()
	f, err := os.Open("testdata/e2fs_csum.img.gz")
	if err != nil {
		t.Fatalf("open reference image: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip reference image: %v", err)
	}
	img, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip reference image: %v", err)
	}
	if len(img) != csumImageBlocks*1024 {
		t.Fatalf("reference image is %d bytes, want %d", len(img), csumImageBlocks*1024)
	}
	return img
}

func openCsumImage(t testing.TB, img []byte, opts Options) *FS {
	t.Helper()
	fs, err := OpenWithOptions(bytes.NewReader(img), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return fs
}

func lookupIno(t testing.TB, fs *FS, p string) Inode {
	t.Helper()
	e, err := fs.LookupPath(p)
	if err != nil {
		t.Fatalf("LookupPath(%s): %v", p, err)
	}
	inode, err := fs.ReadInode(e.Inode)
	if err != nil {
		t.Fatalf("ReadInode(%s): %v", p, err)
	}
	return inode
}

func TestRealImageSuperblockFields(t *testing.T) {
	img := loadCsumImage(t)
	sb := img[SuperblockOffset : SuperblockOffset+SuperblockSize]

	if got, want := SuperblockChecksum(sb), le32(sb, SbOffChecksum); got != want {
		t.Errorf("SuperblockChecksum = 0x%08x, stored 0x%08x", got, want)
	}

	// Offsets added for writers, checked against what dumpe2fs printed.
	checks := []struct {
		name      string
		got, want uint32
	}{
		{"s_magic", uint32(le16(sb, SbOffMagic)), uint32(Magic)},
		{"s_def_hash_version (half_md4)", uint32(sb[SbOffDefHashVersion]), uint32(HashHalfMD4)},
		{"s_flags (signed_directory_hash)", le32(sb, SbOffFlags), FlagSignedHash},
		{"s_checksum_type (crc32c)", uint32(sb[SbOffChecksumType]), uint32(ChecksumTypeCRC32C)},
		{"s_desc_size", uint32(le16(sb, SbOffDescSize)), GroupDescSize64Bit},
		{"s_want_extra_isize", uint32(le16(sb, SbOffWantExtraIsize)), 32},
		{"s_min_extra_isize", uint32(le16(sb, SbOffMinExtraIsize)), 32},
		{"s_mkfs_time (Tue Nov 14 22:13:20 2023)", le32(sb, SbOffMkfsTime), 1700000000},
		{"s_log_groups_per_flex (16)", uint32(sb[SbOffLogGroupsPerFlex]), 4},
		{"s_inode_size", uint32(le16(sb, SbOffInodeSize)), 256},
		{"s_feature_ro_compat has METADATA_CSUM", le32(sb, SbOffFeatureROCompat) & ROCompatMetadataCsum, ROCompatMetadataCsum},
		{"s_feature_incompat has 64BIT", le32(sb, SbOffFeatureIncompat) & Incompat64Bit, Incompat64Bit},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d (0x%x), want %d (0x%x)", c.name, c.got, c.got, c.want, c.want)
		}
	}
	if got := string(bytes.TrimRight(sb[SbOffVolumeName:SbOffVolumeName+VolumeNameLen], "\x00")); got != "libext-csum" {
		t.Errorf("s_volume_name = %q", got)
	}
}

func TestRealImageGroupDescChecksums(t *testing.T) {
	img := loadCsumImage(t)
	var uuid [16]byte
	copy(uuid[:], img[SuperblockOffset+SbOffUUID:])
	seed := MetadataSeed(uuid, 0, false)

	gdt := img[2*1024:]
	for g := uint32(0); g < csumImageGroups; g++ {
		raw := gdt[g*GroupDescSize64Bit : (g+1)*GroupDescSize64Bit]
		got := GroupDescChecksum(seed, g, raw, GroupDescSize64Bit)
		if want := csumImageGDChecksums[g]; got != want {
			t.Errorf("group %d: GroupDescChecksum = 0x%04x, dumpe2fs says 0x%04x", g, got, want)
		}
		if stored := le16(raw, GdOffChecksum); got != stored {
			t.Errorf("group %d: GroupDescChecksum = 0x%04x, stored 0x%04x", g, got, stored)
		}
	}
}

func TestRealImageInodeChecksums(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{})
	seed := fs.csumSeed()

	checked := 0
	for ino := uint32(1); ino <= fs.sb.InodesCount; ino++ {
		if ok, err := fs.InodeAllocated(ino); err != nil || !ok {
			continue
		}
		raw, err := fs.readInodeRaw(ino)
		if err != nil {
			t.Fatalf("read inode %d: %v", ino, err)
		}
		lo, hi, hasHi := InodeChecksum(seed, ino, le32(raw, InodeOffGeneration), raw, int(fs.sb.InodeSize))
		// mke2fs leaves i_extra_isize at 0 on the reserved inodes other than
		// the root, so those store only the low half; every other inode has
		// the 32 bytes that reach i_checksum_hi.
		if wantHi := ino == RootInode || ino >= fs.sb.FirstInode; hasHi != wantHi {
			t.Errorf("inode %d: hasHi = %v, want %v (i_extra_isize %d)",
				ino, hasHi, wantHi, le16(raw, InodeOffExtraISize))
		}
		if storedLo := le16(raw, InodeOffChecksumLo); lo != storedLo {
			t.Errorf("inode %d: checksum lo = 0x%04x, stored 0x%04x", ino, lo, storedLo)
		}
		if storedHi := le16(raw, InodeOffChecksumHi); hasHi && hi != storedHi {
			t.Errorf("inode %d: checksum hi = 0x%04x, stored 0x%04x", ino, hi, storedHi)
		}
		checked++
	}
	// Reserved inodes 1-11, the root and lost+found among them, plus the tree.
	if checked < 70 {
		t.Errorf("checked %d allocated inodes, expected the whole tree", checked)
	}
}

func TestRealImageBitmapChecksums(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{})
	seed := fs.csumSeed()

	var blocks, inodes int
	for g := uint32(0); g < csumImageGroups; g++ {
		gd := fs.groups[g]
		if !gd.BlockUninit() {
			bm, err := fs.BlockBitmap(g)
			if err != nil {
				t.Fatalf("BlockBitmap(%d): %v", g, err)
			}
			if got := BitmapChecksum(seed, bm); got != gd.BlockBitmapChecksum {
				t.Errorf("group %d block bitmap: BitmapChecksum = 0x%08x, stored 0x%08x", g, got, gd.BlockBitmapChecksum)
			}
			if err := fs.VerifyBlockBitmapChecksum(g, bm); err != nil {
				t.Errorf("group %d: %v", g, err)
			}
			blocks++
		}
		if !gd.InodeUninit() {
			bm, err := fs.InodeBitmap(g)
			if err != nil {
				t.Fatalf("InodeBitmap(%d): %v", g, err)
			}
			if got := BitmapChecksum(seed, bm); got != gd.InodeBitmapChecksum {
				t.Errorf("group %d inode bitmap: BitmapChecksum = 0x%08x, stored 0x%08x", g, got, gd.InodeBitmapChecksum)
			}
			inodes++
		}
	}
	if blocks == 0 || inodes == 0 {
		t.Errorf("checked %d block and %d inode bitmaps; the image should have both", blocks, inodes)
	}
}

func TestRealImageExtentTailChecksum(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{})
	sparse := lookupIno(t, fs, "/sparse.bin")

	meta, err := fs.MetadataBlocks(sparse.Number)
	if err != nil {
		t.Fatalf("MetadataBlocks: %v", err)
	}
	if len(meta) != 1 {
		t.Fatalf("sparse.bin has %d extent tree blocks, want 1 (mkcsumimg.sh forces one)", len(meta))
	}
	block, err := fs.readBlock(meta[0])
	if err != nil {
		t.Fatal(err)
	}
	off, ok := extentTailOffset(block)
	if !ok {
		t.Fatalf("extent block %d has no room for a tail", meta[0])
	}
	got := ExtentTailChecksum(fs.csumSeed(), sparse.Number, sparse.Generation, block)
	if stored := le32(block, off); got != stored {
		t.Errorf("ExtentTailChecksum = 0x%08x, stored 0x%08x", got, stored)
	}
}

func TestRealImageDirTailChecksums(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{})
	seed := fs.csumSeed()

	checked := 0
	for _, p := range []string{"/", "/dir", "/many", "/lost+found"} {
		dir := lookupIno(t, fs, p)
		exts, err := fs.InodeExtents(dir, ExtentOptions{})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for _, e := range exts {
			for b := e.PhysicalBlock; b < e.PhysicalBlock+e.Blocks; b++ {
				block, err := fs.readBlock(b)
				if err != nil {
					t.Fatal(err)
				}
				tail := block[len(block)-DirTailSize:]
				if le16(tail, 4) != DirTailSize || tail[7] != DirTailFileType {
					t.Errorf("%s block %d: no dirent tail", p, b)
					continue
				}
				got := DirTailChecksum(seed, dir.Number, dir.Generation, block)
				if stored := le32(tail, 8); got != stored {
					t.Errorf("%s block %d: DirTailChecksum = 0x%08x, stored 0x%08x", p, b, got, stored)
				}
				checked++
			}
		}
	}
	if checked < 6 {
		t.Errorf("checked %d directory blocks; /many alone spans 3", checked)
	}
}

func TestRealImageSuperBackups(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{})
	sparseSuper := fs.sb.FeatureROCompat&ROCompatSparseSuper != 0

	// dumpe2fs: primary in group 0, backups in groups 1, 3, 5 and 7.
	want := map[uint32]bool{0: true, 1: true, 3: true, 5: true, 7: true}
	for g := uint32(0); g < csumImageGroups; g++ {
		if got := HasSuperBackup(g, sparseSuper, false, [2]uint32{}); got != want[g] {
			t.Errorf("HasSuperBackup(%d) = %v, dumpe2fs says %v", g, got, want[g])
		}
		// A backup carries the magic; a group without one does not.
		sb := img[(1+g*1024)*1024:]
		if hasMagic := le16(sb, SbOffMagic) == Magic; hasMagic != want[g] {
			t.Errorf("group %d: superblock magic present = %v, want %v", g, hasMagic, want[g])
		}
	}
}

// The positive control for Strict: an image e2fsprogs wrote and e2fsck passed
// must walk and read cleanly, symlinks either side of the inline limit
// included. Only the sparse file is refused, for its holes.
func TestRealImageStrictAcceptsWellFormedImage(t *testing.T) {
	img := loadCsumImage(t)
	fs := openCsumImage(t, img, Options{Strict: true})
	if !fs.Options().VerifyChecksums {
		t.Error("Strict did not imply VerifyChecksums")
	}

	var names []string
	err := fs.WalkDirWithInode(RootInode, func(p string, e DirEntry, inode Inode) error {
		names = append(names, p)
		switch {
		case inode.IsRegular && p != "/sparse.bin":
			if _, err := fs.ReadFile(inode.Number); err != nil {
				return err
			}
		case inode.IsSymlink:
			f, err := fs.Open(inode.Number)
			if err != nil {
				return err
			}
			if _, err := f.ReadLink(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("strict walk: %v", err)
	}
	if len(names) < 69 {
		t.Errorf("walk visited %d entries", len(names))
	}

	for path, want := range map[string]string{
		"/link59": strings.Repeat("a", 59),
		"/link60": strings.Repeat("b", 60),
	} {
		f, err := fs.OpenPath(path)
		if err != nil {
			t.Fatalf("OpenPath(%s): %v", path, err)
		}
		got, err := f.ReadLink()
		if err != nil {
			t.Fatalf("ReadLink(%s): %v", path, err)
		}
		if got != want {
			t.Errorf("ReadLink(%s) = %q, want %q", path, got, want)
		}
	}

	sparse := lookupIno(t, fs, "/sparse.bin")
	if _, err := fs.ReadFile(sparse.Number); !errors.Is(err, ErrStrict) {
		t.Errorf("strict ReadFile of a sparse file = %v, want ErrStrict", err)
	}
	var se *StrictError
	if errors.As(err, &se) && se.Check != StrictHole {
		t.Errorf("strict ReadFile of a sparse file failed %s, want %s", se.Check, StrictHole)
	}
	if _, err := openCsumImage(t, img, Options{}).ReadFile(sparse.Number); err != nil {
		t.Errorf("default ReadFile of a sparse file: %v", err)
	}
}

// One flipped byte in a block e2fsprogs checksummed: strict reports a checksum
// failure, the default options read straight through it.
func TestRealImageStrictRejectsCorruptTails(t *testing.T) {
	base := loadCsumImage(t)
	fs := openCsumImage(t, base, Options{})
	sparse := lookupIno(t, fs, "/sparse.bin")
	meta, err := fs.MetadataBlocks(sparse.Number)
	if err != nil || len(meta) != 1 {
		t.Fatalf("MetadataBlocks: %v %v", meta, err)
	}
	many := lookupIno(t, fs, "/many")
	manyExts, err := fs.InodeExtents(many, ExtentOptions{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("extent block", func(t *testing.T) {
		img := bytes.Clone(base)
		// The logical start of the fifth extent: still a valid tree, one block
		// further along, so only the checksum can tell.
		off := meta[0]*1024 + ExtentHeaderSize + 4*ExtentEntrySize
		binary.LittleEndian.PutUint32(img[off:], le32(img, int(off))+1)

		_, err := openCsumImage(t, img, Options{Strict: true}).Extents(sparse.Number)
		assertStrict(t, err, StrictExtentTail, true)
		if _, err := openCsumImage(t, img, Options{}).Extents(sparse.Number); err != nil {
			t.Errorf("default Extents: %v", err)
		}
	})

	t.Run("directory block", func(t *testing.T) {
		img := bytes.Clone(base)
		// A byte of a name in the directory's second block.
		blk := manyExts[0].PhysicalBlock + 1
		if manyExts[0].Blocks < 2 {
			blk = manyExts[1].PhysicalBlock
		}
		img[blk*1024+8] ^= 0x01

		_, err := openCsumImage(t, img, Options{Strict: true}).ListDir(many.Number)
		assertStrict(t, err, StrictDirTail, true)
		if _, err := openCsumImage(t, img, Options{}).ListDir(many.Number); err != nil {
			t.Errorf("default ListDir: %v", err)
		}
	})
}

// assertStrict checks that err is a StrictError for the given check.
func assertStrict(t *testing.T, err error, check StrictCheck, checksum bool) {
	t.Helper()
	if !errors.Is(err, ErrStrict) {
		t.Fatalf("err = %v, want ErrStrict", err)
	}
	var se *StrictError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *StrictError", err)
	}
	if se.Check != check {
		t.Errorf("check = %s, want %s (%v)", se.Check, check, err)
	}
	if got := errors.Is(err, ErrChecksumMismatch); got != checksum {
		t.Errorf("errors.Is(err, ErrChecksumMismatch) = %v, want %v", got, checksum)
	}
}
