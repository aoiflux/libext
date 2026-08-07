package libext

import (
	"encoding/binary"
	"testing"
)

// TestParseValidXAttrBlock tests parsing a valid xattr block.
//
// The layout here is the real one. This fixture previously placed entries at
// offset 36 rather than 32, and wrote the value size over e_value_inum at
// offset+4 rather than at offset+8, which matched the parser's own mistake: the
// value size was always read as zero, so block-resident values came back empty
// on every real filesystem while this test passed.
func TestParseValidXAttrBlock(t *testing.T) {
	block := make([]byte, 256)

	binary.LittleEndian.PutUint32(block[0:4], 0xEA020000) // header magic

	// Entries begin immediately after the 32-byte header.
	offset := xattrBlockHeaderSize
	block[offset] = 4                                    // e_name_len
	block[offset+1] = 1                                  // e_name_index (user)
	binary.LittleEndian.PutUint16(block[offset+2:], 100) // e_value_offs
	binary.LittleEndian.PutUint32(block[offset+4:], 0)   // e_value_inum
	binary.LittleEndian.PutUint32(block[offset+8:], 5)   // e_value_size

	// The name follows the 16-byte header.
	copy(block[offset+16:], "test")

	// Value offsets in a block are measured from the start of the block.
	copy(block[100:105], "value")

	// A zero leading word terminates the entry list.
	binary.LittleEndian.PutUint32(block[offset+20:], 0)

	result, err := parseXAttrBlock(block)
	if err != nil {
		t.Fatalf("failed to parse valid xattr block: %v", err)
	}

	if len(result.Attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d", len(result.Attrs))
	}

	if result.Attrs[0].Name != "user.test" {
		t.Errorf("expected name 'user.test', got '%s'", result.Attrs[0].Name)
	}

	if string(result.Attrs[0].Value) != "value" {
		t.Errorf("expected value 'value', got '%s'", string(result.Attrs[0].Value))
	}
}

// TestParseXAttrBlockInvalidMagic tests rejection of invalid magic number.
func TestParseXAttrBlockInvalidMagic(t *testing.T) {
	block := make([]byte, 256)
	binary.LittleEndian.PutUint32(block[0:4], 0xDEADBEEF) // wrong magic

	result, err := parseXAttrBlock(block)
	if err == nil {
		t.Error("expected error for invalid magic, got nil")
	}

	if len(result.Attrs) != 0 {
		t.Errorf("expected 0 attributes on error, got %d", len(result.Attrs))
	}
}

// TestParseXAttrBlockTooSmall tests rejection of blocks that are too small.
func TestParseXAttrBlockTooSmall(t *testing.T) {
	block := make([]byte, 20) // too small for header

	result, err := parseXAttrBlock(block)
	if err == nil {
		t.Error("expected error for small block, got nil")
	}

	if len(result.Attrs) != 0 {
		t.Errorf("expected 0 attributes on error, got %d", len(result.Attrs))
	}
}

// TestXAttrNamespaces tests different namespace prefixes.
func TestXAttrNamespaces(t *testing.T) {
	tests := []struct {
		index    uint8
		expected string
	}{
		// These indices are fixed by ext4. The mapping was previously wrong —
		// security was read as 8 and trusted as 7 — which relabelled every
		// attribute on the filesystem, most visibly turning security.selinux
		// into system.posix_acl.selinux.
		{xattrNamespaceUser, "user"},
		{xattrNamespacePosixACLAccess, "system.posix_acl_access"},
		{xattrNamespacePosixACLDefaul, "system.posix_acl_default"},
		{xattrNamespaceTrusted, "trusted"},
		{xattrNamespaceSecurity, "security"},
		{xattrNamespaceSystem, "system"},
		{99, "ns99"},
	}

	if xattrNamespaceSecurity != 6 || xattrNamespaceTrusted != 4 || xattrNamespaceSystem != 7 {
		t.Fatalf("namespace indices drifted from the ext4 definitions: security=%d trusted=%d system=%d",
			xattrNamespaceSecurity, xattrNamespaceTrusted, xattrNamespaceSystem)
	}

	for _, tc := range tests {
		result := getXAttrNamespace(tc.index)
		if result != tc.expected {
			t.Errorf("namespace %d: expected '%s', got '%s'", tc.index, tc.expected, result)
		}
	}
}

// TestXAttrListGetValue tests retrieving values from an xattr list.
func TestXAttrListGetValue(t *testing.T) {
	list := XAttrList{
		Attrs: []XAttr{
			{Name: "user.foo", Value: []byte("bar")},
			{Name: "user.baz", Value: []byte("qux")},
		},
	}

	// Test getting existing value
	if val := list.GetXAttrValue("user.foo"); string(val) != "bar" {
		t.Errorf("expected 'bar', got '%s'", string(val))
	}

	// Test getting non-existent value
	if val := list.GetXAttrValue("user.nonexistent"); val != nil {
		t.Errorf("expected nil for non-existent key, got %v", val)
	}
}

// TestXAttrListNames tests listing attribute names.
func TestXAttrListNames(t *testing.T) {
	list := XAttrList{
		Attrs: []XAttr{
			{Name: "user.foo", Value: []byte("bar")},
			{Name: "user.baz", Value: []byte("qux")},
		},
	}

	names := list.ListXAttrNames()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}

	if names[0] != "user.foo" || names[1] != "user.baz" {
		t.Errorf("unexpected names: %v", names)
	}
}

// TestXAttrListLen tests length counting.
func TestXAttrListLen(t *testing.T) {
	tests := []struct {
		list     XAttrList
		expected int
	}{
		{XAttrList{Attrs: []XAttr{}}, 0},
		{XAttrList{Attrs: []XAttr{{Name: "a"}}}, 1},
		{XAttrList{Attrs: []XAttr{{}, {}, {}}}, 3},
	}

	for i, tc := range tests {
		result := tc.list.Len()
		if result != tc.expected {
			t.Errorf("test %d: expected len %d, got %d", i, tc.expected, result)
		}
	}
}

// TestParseMultipleXAttrs tests parsing multiple attributes in one block.
func TestParseMultipleXAttrs(t *testing.T) {
	block := make([]byte, 512)
	binary.LittleEndian.PutUint32(block[0:4], 0xEA020000)

	// entry writes one attribute header at off and returns the next offset.
	entry := func(off int, nameIndex uint8, name string, valueOff int, value string) int {
		block[off] = uint8(len(name))
		block[off+1] = nameIndex
		binary.LittleEndian.PutUint16(block[off+2:], uint16(valueOff))
		binary.LittleEndian.PutUint32(block[off+4:], 0) // e_value_inum
		binary.LittleEndian.PutUint32(block[off+8:], uint32(len(value)))
		copy(block[off+16:], name)
		copy(block[valueOff:], value)
		return off + (16+len(name)+3)&^3
	}

	off := xattrBlockHeaderSize
	off = entry(off, xattrNamespaceUser, "first", 200, "val")
	off = entry(off, xattrNamespaceSecurity, "second", 220, "data")
	binary.LittleEndian.PutUint32(block[off:], 0) // terminator

	result, err := parseXAttrBlock(block)
	if err != nil {
		t.Fatalf("failed to parse xattr block with multiple attributes: %v", err)
	}

	if len(result.Attrs) != 2 {
		t.Fatalf("expected 2 attributes, got %d: %+v", len(result.Attrs), result.Attrs)
	}
	if result.Attrs[0].Name != "user.first" || string(result.Attrs[0].Value) != "val" {
		t.Errorf("attr 0 = %q=%q, want user.first=val",
			result.Attrs[0].Name, string(result.Attrs[0].Value))
	}
	if result.Attrs[1].Name != "security.second" || string(result.Attrs[1].Value) != "data" {
		t.Errorf("attr 1 = %q=%q, want security.second=data",
			result.Attrs[1].Name, string(result.Attrs[1].Value))
	}
}
