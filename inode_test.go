package libext

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// timestamp decoding
// ---------------------------------------------------------------------------

// encodeExtra packs an ext4 "extra" timestamp word: bits 0-1 extend the epoch,
// bits 2-31 carry nanoseconds.
func encodeExtra(epoch uint32, nsec uint32) uint32 {
	return (nsec << 2) | (epoch & 0x3)
}

// rawInode allocates an inode of the given size and lets a test fill it.
func rawInode(size int, fill func(raw []byte)) []byte {
	raw := make([]byte, size)
	if fill != nil {
		fill(raw)
	}
	return raw
}

func TestParseInodeDecodesNanoseconds(t *testing.T) {
	const (
		sec  = 0x69e58838
		nsec = 490096625
	)

	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffAtime:], sec)
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], sec)
		binary.LittleEndian.PutUint32(raw[inodeOffCtime:], sec)
		binary.LittleEndian.PutUint32(raw[inodeOffCrtime:], sec)
		binary.LittleEndian.PutUint32(raw[inodeOffAtimeExtra:], encodeExtra(0, nsec))
		binary.LittleEndian.PutUint32(raw[inodeOffMtimeExtra:], encodeExtra(0, nsec))
		binary.LittleEndian.PutUint32(raw[inodeOffCtimeExtra:], encodeExtra(0, nsec))
		binary.LittleEndian.PutUint32(raw[inodeOffCrtimeExtra:], encodeExtra(0, nsec))
		binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)
	})

	inode := parseInode(raw, 13)
	ts := inode.Timestamps()

	for _, tc := range []struct {
		name string
		got  time.Time
	}{
		{"atime", ts.Atime},
		{"mtime", ts.Mtime},
		{"ctime", ts.Ctime},
		{"crtime", ts.Crtime},
	} {
		if tc.got.Unix() != sec {
			t.Errorf("%s seconds = %d, want %d", tc.name, tc.got.Unix(), sec)
		}
		if tc.got.Nanosecond() != nsec {
			t.Errorf("%s nanoseconds = %d, want %d", tc.name, tc.got.Nanosecond(), nsec)
		}
	}

	if !inode.HasCrtime {
		t.Error("HasCrtime = false for an inode carrying a creation time")
	}
	if inode.ExtraISize != 32 {
		t.Errorf("ExtraISize = %d, want 32", inode.ExtraISize)
	}
}

func TestParseInodeDecodesPost2038Dates(t *testing.T) {
	// The seconds field is signed. Without the epoch bits, a value with the high
	// bit set decodes as a date in 1901 rather than after 2038 — a plausible
	// wrong answer rather than an obviously broken one.
	const secBits = 0x80000000 // would be negative if read as int32

	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], secBits)
		binary.LittleEndian.PutUint32(raw[inodeOffMtimeExtra:], encodeExtra(1, 0))
		binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)
	})

	got := parseInode(raw, 1).Mtime
	want := int64(secBits) | (int64(1) << 32)

	if got.Unix() != want {
		t.Errorf("mtime = %d (%s), want %d (%s)",
			got.Unix(), got.Format(time.RFC3339), want, time.Unix(want, 0).UTC().Format(time.RFC3339))
	}
	if got.Year() < 2038 {
		t.Errorf("mtime decoded as %s; the epoch extension bits were dropped", got.Format(time.RFC3339))
	}
}

func TestParseInodeWithoutEpochBitsKeepsSignedSeconds(t *testing.T) {
	// Epoch bits clear means the classic signed interpretation still applies, so
	// genuinely pre-1970 timestamps keep working.
	const secBits = 0xFFFFFFFF // -1: one second before the epoch

	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], secBits)
		binary.LittleEndian.PutUint32(raw[inodeOffMtimeExtra:], encodeExtra(0, 0))
		binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)
	})

	if got := parseInode(raw, 1).Mtime; got.Unix() != -1 {
		t.Errorf("mtime = %d, want -1", got.Unix())
	}
}

func TestParseInodeSmallInodeHasNoCreationTime(t *testing.T) {
	// A 128-byte ext2 inode has no room for crtime; reading past the fixed part
	// would decode neighbouring inodes' bytes as a timestamp.
	raw := rawInode(128, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], 0x69e58838)
	})

	inode := parseInode(raw, 1)
	if inode.HasCrtime {
		t.Error("HasCrtime = true for a 128-byte inode")
	}
	if !inode.Crtime.IsZero() {
		t.Errorf("Crtime = %v, want zero", inode.Crtime)
	}
	if inode.Mtime.Nanosecond() != 0 {
		t.Errorf("nanoseconds = %d, want 0: there is no extra word to read", inode.Mtime.Nanosecond())
	}
}

func TestParseInodeExtraISizeGatesTheExtraFields(t *testing.T) {
	// The inode is 256 bytes, but i_extra_isize says only 4 of them are in use,
	// so crtime is not present even though the space physically exists.
	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 4)
		binary.LittleEndian.PutUint32(raw[inodeOffCrtime:], 0x69e58838)
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], 0x69e58838)
		binary.LittleEndian.PutUint32(raw[inodeOffMtimeExtra:], encodeExtra(0, 500))
	})

	inode := parseInode(raw, 1)
	if inode.HasCrtime {
		t.Error("HasCrtime = true although i_extra_isize does not cover i_crtime")
	}
	if inode.Mtime.Nanosecond() != 0 {
		t.Errorf("nanoseconds = %d; i_extra_isize does not cover i_mtime_extra", inode.Mtime.Nanosecond())
	}
}

func TestParseInodeDeletionTimeHasNoExtraWord(t *testing.T) {
	// i_dtime is whole seconds even on ext4: there is no i_dtime_extra.
	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[inodeOffDtime:], 0x69e58838)
		binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)
	})

	dtime := parseInode(raw, 1).Dtime
	if dtime.Unix() != 0x69e58838 {
		t.Errorf("dtime = %d, want %d", dtime.Unix(), 0x69e58838)
	}
	if dtime.Nanosecond() != 0 {
		t.Errorf("dtime nanoseconds = %d, want 0", dtime.Nanosecond())
	}
}

// ---------------------------------------------------------------------------
// widened fields
// ---------------------------------------------------------------------------

func TestParseInodeWidensBlocksAndFileACL(t *testing.T) {
	// The high halves live in osd2 and were previously dropped, which put xattr
	// blocks beyond the 32-bit boundary out of reach on large volumes.
	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[inodeOffBlocksLo:], 0x11223344)
		binary.LittleEndian.PutUint16(raw[inodeOffBlocksHi:], 0x0055)
		binary.LittleEndian.PutUint32(raw[inodeOffFileACLLo:], 0xAABBCCDD)
		binary.LittleEndian.PutUint16(raw[inodeOffFileACLHi:], 0x0066)
	})

	inode := parseInode(raw, 1)
	if want := uint64(0x0055_11223344); inode.Blocks512 != want {
		t.Errorf("Blocks512 = 0x%x, want 0x%x", inode.Blocks512, want)
	}
	if want := uint64(0x0066_AABBCCDD); inode.FileACL != want {
		t.Errorf("FileACL = 0x%x, want 0x%x", inode.FileACL, want)
	}
}

func TestParseInodeDirectorySizeIgnoresSizeHigh(t *testing.T) {
	// For directories on ext2/ext3 the field at 0x6C is i_dir_acl, not the high
	// word of the size. Folding it in produces absurd directory sizes.
	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeDir|0o755)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 4096)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeHi:], 0xDEADBEEF) // i_dir_acl
	})

	if got := parseInode(raw, 2).Size; got != 4096 {
		t.Errorf("directory Size = %d, want 4096", got)
	}

	// A regular file with the same bytes does use the high word.
	raw[inodeOffMode] = byte(inodeTypeRegular & 0xFF)
	binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
	if got := parseInode(raw, 2).Size; got != (0xDEADBEEF<<32)|4096 {
		t.Errorf("regular file Size = %d, want the widened value", got)
	}
}

func TestParseInodeFlags(t *testing.T) {
	raw := rawInode(256, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[inodeOffFlags:], inodeFlagExtents|inodeFlagInlineData|inodeFlagHugeFile)
	})

	inode := parseInode(raw, 1)
	if !inode.HasExtents || !inode.HasInline || !inode.HugeFile {
		t.Errorf("flags decoded as extents=%v inline=%v huge=%v, want all true",
			inode.HasExtents, inode.HasInline, inode.HugeFile)
	}
}

func TestInodeDeleted(t *testing.T) {
	tests := []struct {
		name  string
		mode  uint16
		links uint16
		dtime uint32
		want  bool
	}{
		{"live file", inodeTypeRegular | 0o644, 1, 0, false},
		{"deletion time set", inodeTypeRegular | 0o644, 1, 0x69e58838, true},
		{"no links remaining", inodeTypeRegular | 0o644, 0, 0, true},
		{"never allocated", 0, 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := rawInode(256, func(raw []byte) {
				binary.LittleEndian.PutUint16(raw[inodeOffMode:], tt.mode)
				binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], tt.links)
				binary.LittleEndian.PutUint32(raw[inodeOffDtime:], tt.dtime)
			})
			if got := parseInode(raw, 30).Deleted(); got != tt.want {
				t.Errorf("Deleted() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// entry-level surfacing
// ---------------------------------------------------------------------------

// buildDirFixture lays out a root directory at inode 2 listing one regular file.
func buildDirFixture(t testing.TB) []byte {
	t.Helper()

	img := buildTestImage(t, defaultSBConfig())

	const dirBlock = 200
	var dir []byte
	dir = append(dir, dirent(2, 12, extDirentTypeDirectory, ".")...)
	dir = append(dir, dirent(2, 12, extDirentTypeDirectory, "..")...)
	// 8-byte header plus a 9-character name, padded to a 4-byte boundary.
	dir = append(dir, dirent(40, 20, 1, "notes.txt")...)
	// Final record spans the rest of the block, as ext4 lays it out.
	tail := dirent(41, uint16(testBlockSize-44), 1, "image.bin")
	dir = append(dir, tail...)

	writeTestBlock(img, dirBlock, dir)
	writeTestInode(img, 2, inodeTypeDir|0o755, testBlockSize, 0, classicRoot([]uint32{dirBlock}, 0, 0, 0))

	// notes.txt, with a full timestamp set.
	notes := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 1234)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 1)
		binary.LittleEndian.PutUint32(raw[inodeOffMtime:], 0x69e58838)
		binary.LittleEndian.PutUint32(raw[inodeOffAtime:], 0x69e58839)
		binary.LittleEndian.PutUint32(raw[inodeOffCtime:], 0x69e5883A)
		binary.LittleEndian.PutUint16(raw[inodeOffUIDLo:], 1000)
		binary.LittleEndian.PutUint16(raw[inodeOffGIDLo:], 1001)
	})
	copy(img[testInodeTableBlock*testBlockSize+39*testInodeSize:], notes)

	// image.bin, unlinked: a deletion time and no remaining links.
	deleted := rawInode(testInodeSize, func(raw []byte) {
		binary.LittleEndian.PutUint16(raw[inodeOffMode:], inodeTypeRegular|0o644)
		binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], 4096)
		binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 0)
		binary.LittleEndian.PutUint32(raw[inodeOffDtime:], 0x69e58900)
	})
	copy(img[testInodeTableBlock*testBlockSize+40*testInodeSize:], deleted)

	return img
}

func findEntry(entries []DirEntry, name string) (DirEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return DirEntry{}, false
}

func TestListDirExPopulatesInodeMetadata(t *testing.T) {
	fs := openFixture(t, buildDirFixture(t), Options{})

	entries, err := fs.ListDirEx(2, DirOptions{WithInodeMetadata: true})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}

	notes, ok := findEntry(entries, "notes.txt")
	if !ok {
		t.Fatalf("notes.txt missing from %+v", entries)
	}
	if notes.Times.Mtime.Unix() != 0x69e58838 {
		t.Errorf("Mtime = %v, want the inode's mtime", notes.Times.Mtime)
	}
	if notes.Times.Atime.Unix() != 0x69e58839 || notes.Times.Ctime.Unix() != 0x69e5883A {
		t.Errorf("atime/ctime = %v/%v, not taken from the inode", notes.Times.Atime, notes.Times.Ctime)
	}
	if notes.UID != 1000 || notes.GID != 1001 {
		t.Errorf("UID/GID = %d/%d, want 1000/1001", notes.UID, notes.GID)
	}
	if notes.Size != 1234 {
		t.Errorf("Size = %d, want 1234", notes.Size)
	}
	if notes.Deleted {
		t.Error("notes.txt reported as deleted")
	}
}

func TestListDirExSurfacesDeletedState(t *testing.T) {
	fs := openFixture(t, buildDirFixture(t), Options{})

	entries, err := fs.ListDirEx(2, DirOptions{WithInodeMetadata: true})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}

	// The name is still in the directory, but the inode it points at has been
	// unlinked. Surfacing that previously required a separate ReadInode call.
	img, ok := findEntry(entries, "image.bin")
	if !ok {
		t.Fatalf("image.bin missing from %+v", entries)
	}
	if !img.Deleted {
		t.Error("image.bin not reported as deleted despite a deletion time and zero links")
	}
	if img.Times.Dtime.Unix() != 0x69e58900 {
		t.Errorf("Dtime = %v, want the inode's deletion time", img.Times.Dtime)
	}
}

func TestListDirExWithoutMetadataDoesNotReadInodes(t *testing.T) {
	counter := &countingReaderAt{r: bytes.NewReader(buildDirFixture(t))}
	fs, err := OpenWithOptions(counter, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	counter.count.Store(0)
	entries, err := fs.ListDirEx(2, DirOptions{})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}

	// One read for the directory inode, one for its data block. Reading an inode
	// per entry is what WithInodeMetadata opts into.
	if got := counter.count.Load(); got > 3 {
		t.Errorf("issued %d reads for a plain listing; metadata is being read unasked", got)
	}
	if _, ok := findEntry(entries, "notes.txt"); !ok {
		t.Error("notes.txt missing from a plain listing")
	}
}

func TestDirOptionsDotEntries(t *testing.T) {
	fs := openFixture(t, buildDirFixture(t), Options{})

	without, err := fs.ListDirEx(2, DirOptions{})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}
	if _, ok := findEntry(without, "."); ok {
		t.Error("dot entries present by default")
	}

	with, err := fs.ListDirEx(2, DirOptions{IncludeDotEntries: true})
	if err != nil {
		t.Fatalf("ListDirEx: %v", err)
	}
	if _, ok := findEntry(with, "."); !ok {
		t.Error("dot entries missing with IncludeDotEntries")
	}
	if _, ok := findEntry(with, ".."); !ok {
		t.Error("parent entry missing with IncludeDotEntries")
	}
}

func TestReadDirPopulatesMetadata(t *testing.T) {
	// ReadDir already read one inode per entry to resolve type and size, so the
	// timestamps come along without any additional I/O.
	fs := openFixture(t, buildDirFixture(t), Options{})

	root, err := fs.GetRootDirectory()
	if err != nil {
		t.Fatalf("GetRootDirectory: %v", err)
	}
	entries, err := root.ReadDir()
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	notes, ok := findEntry(entries, "notes.txt")
	if !ok {
		t.Fatalf("notes.txt missing from %+v", entries)
	}
	if notes.Times.Mtime.IsZero() {
		t.Error("ReadDir left Times empty despite already reading the inode")
	}
	if notes.Size != 1234 {
		t.Errorf("Size = %d, want 1234", notes.Size)
	}
}

func TestFileTimestamps(t *testing.T) {
	fs := openFixture(t, buildDirFixture(t), Options{})

	f, err := fs.OpenPath("/notes.txt")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if got := f.Timestamps().Mtime.Unix(); got != 0x69e58838 {
		t.Errorf("File.Timestamps().Mtime = %d, want %d", got, 0x69e58838)
	}
	if f.Inode().UID != 1000 {
		t.Errorf("File.Inode().UID = %d, want 1000", f.Inode().UID)
	}
}
