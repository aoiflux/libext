package libext

import (
	"bytes"
	"encoding/binary"
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

	binary.LittleEndian.PutUint16(raw[inodeOffMode:], mode)
	binary.LittleEndian.PutUint32(raw[inodeOffSizeLo:], size)
	binary.LittleEndian.PutUint16(raw[inodeOffLinksCount:], 1)
	binary.LittleEndian.PutUint32(raw[inodeOffFlags:], flags)
	binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)
	copy(raw[inodeOffBlockRaw:inodeOffBlockRaw+60], iblock)

	xstart := inodeBaseSize + 32
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
	raw := buildInodeWithXAttrs(t, inodeTypeRegular|0o644, 6, 0, nil, []inodeXAttr{
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
	raw := buildInodeWithXAttrs(t, inodeTypeRegular|0o644, 0, 0, nil, []inodeXAttr{
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
	binary.LittleEndian.PutUint16(raw[inodeOffExtraISize:], 32)

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
	fs := &FS{sb: Superblock{FeatureCompat: featureCompatExtAttr}}

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
	raw := buildInodeWithXAttrs(t, inodeTypeRegular|0o644, uint32(len(content)),
		inodeFlagInlineData, content, nil)

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

	raw := buildInodeWithXAttrs(t, inodeTypeRegular|0o644, uint32(len(full)),
		inodeFlagInlineData, head, []inodeXAttr{
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
	raw := buildInodeWithXAttrs(t, inodeTypeRegular|0o644, 4, inodeFlagInlineData,
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
		FeatureIncompat: featureIncompatFileType,
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

	writeTestInode(img, 40, inodeTypeDir|0o755, uint32(len(inline)), inodeFlagInlineData, inline)
	writeTestInode(img, 41, inodeTypeRegular|0o644, 0, 0, nil)

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
	writeTestInode(img, 40, inodeTypeRegular|0o644, 10, inodeFlagInlineData, []byte("0123456789"))

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
	writeTestInode(img, 40, inodeTypeRegular|0o644, uint32(len(content)),
		inodeFlagInlineData, []byte(content))

	fs := openFixture(t, img, Options{})

	got, err := fs.ReadFile(40)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != content {
		t.Errorf("ReadFile = %q, want %q", got, content)
	}
}
