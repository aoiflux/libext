package libext

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// buildInodeWithXAttrs renders a 256-byte inode carrying in-inode attributes.
//
// Entries grow forward from the start of the xattr area while values are packed
// backward from the end of the inode, which is the layout detail that makes the
// list terminator subtle.
func buildInodeWithXAttrs(t testing.TB, mode uint16, size uint32, flags uint32, iblock []byte, attrs []inodeXAttr) []byte {
	t.Helper()

	const inodeSize = 256
	raw := make([]byte, inodeSize)

	binary.LittleEndian.PutUint16(raw[InodeOffMode:], mode)
	binary.LittleEndian.PutUint32(raw[InodeOffSizeLo:], size)
	binary.LittleEndian.PutUint16(raw[InodeOffLinksCount:], 1)
	binary.LittleEndian.PutUint32(raw[InodeOffFlags:], flags)
	binary.LittleEndian.PutUint16(raw[InodeOffExtraISize:], 32)
	copy(raw[InodeOffBlockRaw:InodeOffBlockRaw+60], iblock)

	xstart := GoodOldInodeSize + 32
	binary.LittleEndian.PutUint32(raw[xstart:], xattrInodeMagic)

	base := xstart + 4
	entryOff := base
	valueEnd := inodeSize // values are allocated downward from here

	for _, a := range attrs {
		valueEnd -= len(a.value)
		valueOffs := valueEnd - base

		raw[entryOff] = uint8(len(a.name))
		raw[entryOff+1] = a.index
		binary.LittleEndian.PutUint16(raw[entryOff+2:], uint16(valueOffs))
		binary.LittleEndian.PutUint32(raw[entryOff+4:], a.inum)
		binary.LittleEndian.PutUint32(raw[entryOff+8:], uint32(len(a.value)))
		copy(raw[entryOff+16:], a.name)
		copy(raw[valueEnd:], a.value)

		entryOff += (xattrEntryHeaderSize + len(a.name) + 3) &^ 3
	}
	// Terminator: only the leading word is zeroed, exactly as ext4 writes it.
	binary.LittleEndian.PutUint32(raw[entryOff:], 0)

	return raw
}

// ---------------------------------------------------------------------------
// in-inode xattrs
// ---------------------------------------------------------------------------

func TestParseInodeXAttrs(t *testing.T) {
	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, 6, 0, nil, []inodeXAttr{
		{index: xattrNamespaceSecurity, name: "selinux", value: []byte("system_u:object_r:etc_t:s0\n")},
		{index: xattrNamespaceUser, name: "comment", value: []byte("hello-attr")},
	})

	attrs, err := parseInodeXAttrs(raw, 32)
	if err != nil {
		t.Fatalf("parseInodeXAttrs: %v", err)
	}
	if len(attrs) != 2 {
		t.Fatalf("got %d attributes, want 2: %+v", len(attrs), attrs)
	}
	if attrs[0].name != "selinux" || string(attrs[0].value) != "system_u:object_r:etc_t:s0\n" {
		t.Errorf("attr 0 = %q=%q", attrs[0].name, attrs[0].value)
	}
	if attrs[1].name != "comment" || string(attrs[1].value) != "hello-attr" {
		t.Errorf("attr 1 = %q=%q", attrs[1].name, attrs[1].value)
	}
}

func TestParseInodeXAttrsTerminatorIgnoresTrailingValueBytes(t *testing.T) {
	// The entry list ends at the first entry whose leading 32 bits are zero.
	// Requiring the whole header to be zero fails here, because the bytes just
	// past the terminator are the tail of a packed value and are not zero.
	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, 0, 0, nil, []inodeXAttr{
		{index: xattrNamespaceUser, name: "comment", value: []byte("hello-attr")},
	})

	attrs, err := parseInodeXAttrs(raw, 32)
	if err != nil {
		t.Fatalf("parseInodeXAttrs: %v", err)
	}
	if len(attrs) != 1 {
		t.Fatalf("got %d attributes, want 1: %+v", len(attrs), attrs)
	}
}

func TestParseInodeXAttrsAbsentWithoutMagic(t *testing.T) {
	raw := make([]byte, 256)
	binary.LittleEndian.PutUint16(raw[InodeOffExtraISize:], 32)

	attrs, err := parseInodeXAttrs(raw, 32)
	if err != nil {
		t.Fatalf("parseInodeXAttrs: %v", err)
	}
	if len(attrs) != 0 {
		t.Errorf("got %d attributes from an inode with no xattr area", len(attrs))
	}
}

func TestGetInlineXAttrsSkipsInlineDataAttribute(t *testing.T) {
	// system.data carries the inline-data overflow. It is storage, not a
	// user-visible attribute, and listing it would be misleading.
	fs := &FS{sb: Superblock{FeatureCompat: CompatExtAttr}}

	attrs := []inodeXAttr{
		{index: xattrIndexSystem, name: inlineDataAttrName, value: []byte("overflow")},
		{index: xattrNamespaceUser, name: "keep", value: []byte("yes")},
	}

	list := fs.materializeXAttrs(attrs)
	if list.Len() != 1 {
		t.Fatalf("got %d attributes, want 1: %+v", list.Len(), list.Attrs)
	}
	if list.Attrs[0].Name != "user.keep" {
		t.Errorf("name = %q, want user.keep", list.Attrs[0].Name)
	}
}

// ---------------------------------------------------------------------------
// inline data
// ---------------------------------------------------------------------------

func TestInlineDataFitsInBlockArea(t *testing.T) {
	content := []byte("tiny inline content")
	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, uint32(len(content)),
		InodeFlagInlineData, content, nil)

	inode := parseInode(raw, 14)
	if !inode.HasInline {
		t.Fatal("HasInline = false for an inode flagged inline")
	}

	fs := &FS{}
	got, err := fs.inlineDataFromRaw(inode, raw)
	if err != nil {
		t.Fatalf("inlineDataFromRaw: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("inline data = %q, want %q", got, content)
	}
}

func TestInlineDataOverflowsIntoSystemData(t *testing.T) {
	// Anything past the 60-byte block area lives in a system.data attribute.
	// Missing it truncates the file to 60 bytes rather than failing.
	head := bytes.Repeat([]byte("A"), inlineDataInBlock)
	tail := []byte("BBBBBBBBBB")
	full := append(append([]byte{}, head...), tail...)

	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, uint32(len(full)),
		InodeFlagInlineData, head, []inodeXAttr{
			{index: xattrIndexSystem, name: inlineDataAttrName, value: tail},
		})

	inode := parseInode(raw, 14)
	fs := &FS{}

	got, err := fs.inlineDataFromRaw(inode, raw)
	if err != nil {
		t.Fatalf("inlineDataFromRaw: %v", err)
	}
	if !bytes.Equal(got, full) {
		t.Errorf("inline data = %d bytes, want %d", len(got), len(full))
	}
}

func TestInlineDataTruncatesToSize(t *testing.T) {
	// The block area is always 60 bytes on disk; only Size says how much of it
	// is real.
	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, 4, InodeFlagInlineData,
		[]byte("abcdefghij"), nil)

	inode := parseInode(raw, 14)
	fs := &FS{}

	got, err := fs.inlineDataFromRaw(inode, raw)
	if err != nil {
		t.Fatalf("inlineDataFromRaw: %v", err)
	}
	if string(got) != "abcd" {
		t.Errorf("inline data = %q, want %q", got, "abcd")
	}
}

// ---------------------------------------------------------------------------
// inline directories
// ---------------------------------------------------------------------------

func TestInlineDirectoryEntries(t *testing.T) {
	// An inline directory has no "." or ".." records at all; the parent inode is
	// stored in the first four bytes. Parsing it as a normal directory block
	// reads that number as a record header and yields nothing usable.
	var data []byte
	data = binary.LittleEndian.AppendUint32(data, RootInode) // parent
	data = append(data, dirent(16, 20, 1, "nested.txt")...)

	fs := &FS{sb: Superblock{
		FeatureIncompat: IncompatFiletype,
		BlockSize:       1024,
	}}
	inode := Inode{Number: 15, IsDirectory: true, HasInline: true}

	entries, err := fs.inlineDirEntries(inode, data)
	if err != nil {
		t.Fatalf("inlineDirEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (. .. nested.txt): %+v", len(entries), entries)
	}

	if entries[0].Name != "." || entries[0].Inode != 15 {
		t.Errorf("entry 0 = %q inode %d, want . inode 15", entries[0].Name, entries[0].Inode)
	}
	if entries[1].Name != ".." || entries[1].Inode != RootInode {
		t.Errorf("entry 1 = %q inode %d, want .. inode %d",
			entries[1].Name, entries[1].Inode, RootInode)
	}
	if entries[2].Name != "nested.txt" || entries[2].Inode != 16 {
		t.Errorf("entry 2 = %q inode %d, want nested.txt inode 16",
			entries[2].Name, entries[2].Inode)
	}
}

func TestInlineDirectoryListedThroughListDir(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())

	var inline []byte
	inline = binary.LittleEndian.AppendUint32(inline, RootInode)
	inline = append(inline, dirent(41, 20, 1, "nested.txt")...)

	writeTestInode(img, 40, ModeDir|0o755, uint32(len(inline)), InodeFlagInlineData, inline)
	writeTestInode(img, 41, ModeRegular|0o644, 0, 0, nil)

	fs := openFixture(t, img, Options{})

	entries, err := fs.ListDir(40)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if !containsName(names, "nested.txt") {
		t.Errorf("inline directory listed as %v; nested.txt missing", names)
	}
	if !containsName(names, "..") {
		t.Errorf("inline directory listed as %v; parent entry missing", names)
	}
}

func TestInlineInodeReportsInlineExtent(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	writeTestInode(img, 40, ModeRegular|0o644, 10, InodeFlagInlineData, []byte("0123456789"))

	fs := openFixture(t, img, Options{})

	exts, err := fs.Extents(40)
	if err != nil {
		t.Fatalf("Extents: %v", err)
	}
	if len(exts) != 1 || !exts[0].Inline() {
		t.Fatalf("Extents = %+v, want a single inline extent", exts)
	}

	// An inline file occupies no blocks, so it has no byte ranges on disk.
	runs, err := fs.DataRuns(40)
	if err != nil {
		t.Fatalf("DataRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("DataRuns = %+v, want none for an inline file", runs)
	}
}

func TestReadFileReturnsInlineContent(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	content := "inline file body"
	writeTestInode(img, 40, ModeRegular|0o644, uint32(len(content)),
		InodeFlagInlineData, []byte(content))

	fs := openFixture(t, img, Options{})

	got, err := fs.ReadFile(40)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != content {
		t.Errorf("ReadFile = %q, want %q", got, content)
	}
}

// ---------------------------------------------------------------------------
// PAR-35: the claimed size is bounded by the inode's own capacity
// ---------------------------------------------------------------------------

func TestInlineSizeBoundIsNotTooTight(t *testing.T) {
	// The bound is the inode's own capacity: the 60-byte block-pointer area
	// plus the xattr space after the fixed fields and the extra area, which is
	// where system.data lives. A 256-byte inode with i_extra_isize 32 holds at
	// most 156 bytes. Pinned from both sides, so the guard cannot drift tight
	// and start refusing inodes a real filesystem writes.
	const maxForFixture = inlineDataInBlock + (256 - GoodOldInodeSize - 32)
	if maxForFixture != 156 {
		t.Fatalf("fixture arithmetic changed: max = %d, want 156", maxForFixture)
	}

	for _, tc := range []struct {
		size    uint32
		wantErr bool
	}{
		{maxForFixture, false},
		{maxForFixture + 1, true},
	} {
		raw := buildInodeWithXAttrs(t, ModeRegular|0o644, tc.size,
			InodeFlagInlineData, bytes.Repeat([]byte("A"), inlineDataInBlock), nil)
		inode := parseInode(raw, 14)

		_, err := (&FS{}).inlineDataFromRaw(inode, raw)
		switch {
		case tc.wantErr && err == nil:
			t.Errorf("size %d was accepted; a 256-byte inode cannot hold it", tc.size)
		case tc.wantErr && !errors.Is(err, ErrUnsupportedLayout):
			t.Errorf("size %d: error = %v, want one wrapping ErrUnsupportedLayout", tc.size, err)
		case !tc.wantErr && err != nil:
			t.Errorf("size %d was refused (%v), want accepted", tc.size, err)
		}
	}
}

func TestInlineDataAtMaximumStoredSizeStillReads(t *testing.T) {
	// The largest inline file a 256-byte inode can really store: 60 bytes in
	// the block-pointer area plus the largest system.data value that fits the
	// 96-byte xattr area after its 4-byte magic, its 20-byte entry and the
	// 4-byte terminator.
	const tailLen = 68
	head := bytes.Repeat([]byte("H"), inlineDataInBlock)
	tail := bytes.Repeat([]byte("T"), tailLen)
	full := append(append([]byte{}, head...), tail...)

	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, uint32(len(full)),
		InodeFlagInlineData, head, []inodeXAttr{
			{index: xattrIndexSystem, name: inlineDataAttrName, value: tail},
		})

	inode := parseInode(raw, 40)
	got, err := (&FS{}).inlineDataFromRaw(inode, raw)
	if err != nil {
		t.Fatalf("inlineDataFromRaw refused the largest inline file a 256-byte inode holds: %v", err)
	}
	if !bytes.Equal(got, full) {
		t.Errorf("inline data = %q (%d bytes), want %d bytes", got, len(got), len(full))
	}

	// And through the whole-file read path, on an image whose inodes are 256
	// bytes, so the xattr area the overflow lives in exists on disk too.
	img := buildExt4Image(t)
	writeRawInode(img, 256, 40, raw)
	fs := openFixture(t, img, Options{})

	whole, err := fs.ReadFile(40)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(whole, full) {
		t.Errorf("ReadFile returned %d bytes, want %d", len(whole), len(full))
	}
}

// ---------------------------------------------------------------------------
// EXT-U1: the streaming read path must serve an inline inode too
// ---------------------------------------------------------------------------

// assertReadAtMatches reads f in pieces at varied offsets and requires every
// piece to equal the same slice of want.
//
// The piece sizes straddle the structures involved: 1 and 7 bytes land inside
// the block-pointer area, 60 is exactly that area, and 4096 asks for more than
// any inline inode holds.
func assertReadAtMatches(t *testing.T, f *File, want []byte) {
	t.Helper()
	for _, size := range []int{1, 7, 60, 4096} {
		for _, off := range []int{0, 1, 13, len(want) - 1} {
			if off < 0 || off >= len(want) {
				continue
			}
			buf := make([]byte, size)
			n, err := f.ReadAt(buf, int64(off))
			if err != nil && err != io.EOF {
				t.Fatalf("ReadAt(%d bytes at %d): %v", size, off, err)
			}
			exp := want[off:]
			if len(exp) > size {
				exp = exp[:size]
			}
			if !bytes.Equal(buf[:n], exp) {
				t.Errorf("ReadAt(%d bytes at %d) = %q, want %q", size, off, buf[:n], exp)
			}
		}
	}
}

func TestReadAtServesInlineContent(t *testing.T) {
	// An inline inode owns no blocks, so a block-mapped read maps nothing and
	// fills the range with zeros. That is a wrong answer rather than an error,
	// and ReadFile on the same inode returns the right bytes.
	img := buildTestImage(t, defaultSBConfig())
	content := bytes.Repeat([]byte("inline-"), 9)[:inlineDataInBlock]
	writeTestInode(img, 40, ModeRegular|0o644, uint32(len(content)),
		InodeFlagInlineData, content)

	fs := openFixture(t, img, Options{})

	whole, err := fs.ReadFile(40)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(whole, content) {
		t.Fatalf("ReadFile = %q, want %q", whole, content)
	}

	f, err := fs.Open(40)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	assertReadAtMatches(t, f, whole)
}

func TestReadAtServesInlineOverflowContent(t *testing.T) {
	// The seam between i_block and system.data is where a reader serving
	// ReadAt from only the first 60 bytes would still be wrong.
	const tailLen = 68
	head := bytes.Repeat([]byte("H"), inlineDataInBlock)
	tail := bytes.Repeat([]byte("T"), tailLen)
	full := append(append([]byte{}, head...), tail...)

	raw := buildInodeWithXAttrs(t, ModeRegular|0o644, uint32(len(full)),
		InodeFlagInlineData, head, []inodeXAttr{
			{index: xattrIndexSystem, name: inlineDataAttrName, value: tail},
		})

	img := buildExt4Image(t)
	writeRawInode(img, 256, 40, raw)
	fs := openFixture(t, img, Options{})

	f, err := fs.Open(40)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	assertReadAtMatches(t, f, full)
}

func TestReadServesInlineContent(t *testing.T) {
	// Read routes through ReadAt, so it carried the same bug.
	img := buildTestImage(t, defaultSBConfig())
	content := []byte("inline file body")
	writeTestInode(img, 40, ModeRegular|0o644, uint32(len(content)),
		InodeFlagInlineData, content)

	f, err := openFixture(t, img, Options{}).Open(40)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("sequential Read = %q, want %q", got, content)
	}
}

func TestReadAtPastEndOfInlineFileReturnsEOF(t *testing.T) {
	img := buildTestImage(t, defaultSBConfig())
	content := []byte("inline file body")
	writeTestInode(img, 40, ModeRegular|0o644, uint32(len(content)),
		InodeFlagInlineData, content)

	f, err := openFixture(t, img, Options{}).Open(40)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for _, off := range []int64{int64(len(content)), int64(len(content)) + 1, 1 << 20} {
		buf := make([]byte, 4)
		n, err := f.ReadAt(buf, off)
		if n != 0 || err != io.EOF {
			t.Errorf("ReadAt at %d = (%d, %v), want (0, io.EOF)", off, n, err)
		}
	}
}
