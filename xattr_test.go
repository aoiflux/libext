package libext

import (
	"encoding/binary"
	"testing"
)

// TestParseValidXAttrBlock tests parsing a valid xattr block.
func TestParseValidXAttrBlock(t *testing.T) {
	// Create a minimal valid xattr block
	block := make([]byte, 256)

	// Write magic number at offset 0
	binary.LittleEndian.PutUint32(block[0:4], 0xEA020000)

	// Entry at offset 36:
	// nameLen=4, nameIndex=1 (user), valueOffset=100, valueSize=5
	offset := 36
	block[offset] = 4                                    // nameLen
	block[offset+1] = 1                                  // nameIndex (user)
	binary.LittleEndian.PutUint16(block[offset+2:], 100) // valueOffset
	binary.LittleEndian.PutUint32(block[offset+4:], 5)   // valueSize

	// Entry name starts at offset+16=52
	copy(block[52:56], "test")

	// Entry value at offset 100
	copy(block[100:105], "value")

	// Terminator entry at next aligned offset (36+20=56)
	block[56] = 0 // nameLen=0 (terminator)
	block[57] = 0 // nameIndex=0 (terminator)

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
		{xattrNamespaceUser, "user"},
		{xattrNamespaceSystemACL, "system.posix_acl"},
		{xattrNamespaceTrusted, "trusted"},
		{xattrNamespaceSecurity, "security"},
		{99, "ns99"},
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

	// Write magic
	binary.LittleEndian.PutUint32(block[0:4], 0xEA020000)

	// First entry at offset 36
	block[36] = 5                                  // nameLen
	block[37] = 1                                  // nameIndex (user)
	binary.LittleEndian.PutUint16(block[38:], 120) // valueOffset
	binary.LittleEndian.PutUint32(block[40:], 3)   // valueSize

	// Entry name at offset 36+16=52
	copy(block[52:57], "first")

	// Entry value at offset 120
	copy(block[120:123], "val")

	// Second entry at offset 36+20=56 (first entry is 20 bytes aligned)
	block[56] = 6                                  // nameLen
	block[57] = 6                                  // nameIndex (system.posix_acl)
	binary.LittleEndian.PutUint16(block[58:], 140) // valueOffset
	binary.LittleEndian.PutUint32(block[60:], 4)   // valueSize

	// Entry name at offset 56+16=72
	copy(block[72:78], "second")

	// Entry value at offset 140
	copy(block[140:144], "data")

	// Terminator at next aligned offset (56+24=80)
	block[80] = 0
	block[81] = 0

	result, err := parseXAttrBlock(block)
	if err != nil {
		t.Fatalf("failed to parse xattr block with multiple attributes: %v", err)
	}

	if len(result.Attrs) < 1 {
		t.Errorf("expected at least 1 attribute, got %d", len(result.Attrs))
	}
}
