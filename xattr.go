package libext

import (
	"encoding/binary"
	"fmt"
)

// XAttr represents a single extended attribute.
type XAttr struct {
	Name  string // attribute name
	Value []byte // attribute value
}

// XAttrList is a collection of extended attributes.
type XAttrList struct {
	Attrs []XAttr
}

// XAttrBlockHeader represents the header of an extended attribute block.
type XAttrBlockHeader struct {
	Magic    uint32 // magic number (should be 0xEA020000)
	Refcount uint32 // reference count
	Blocks   uint32 // number of blocks used
	Hash     uint32 // hash of attributes
	Checksum uint32 // checksum of block
	Reserved [4]uint32
}

// XAttrEntry represents a single extended attribute entry in storage format.
type XAttrEntry struct {
	NameLen     uint8  // length of attribute name
	NameIndex   uint8  // namespace of attribute
	ValueOffset uint16 // offset of value in block
	ValueBlock  uint32 // block number if value is in block
	ValueSize   uint32 // size of value
	Hash        uint32 // hash of attribute
	Name        string // attribute name
	Value       []byte // attribute value
}

// Extended attribute namespaces.
const (
	xattrNamespaceUser      uint8 = 1
	xattrNamespaceSystemACL uint8 = 6
	xattrNamespaceTrusted   uint8 = 7
	xattrNamespaceSecurity  uint8 = 8
)

// getXAttrNamespace returns the namespace prefix for an attribute.
func getXAttrNamespace(index uint8) string {
	switch index {
	case xattrNamespaceUser:
		return "user"
	case xattrNamespaceSystemACL:
		return "system.posix_acl"
	case xattrNamespaceTrusted:
		return "trusted"
	case xattrNamespaceSecurity:
		return "security"
	default:
		return fmt.Sprintf("ns%d", index)
	}
}

// GetXAttrs reads extended attributes from an inode.
// Returns empty list if inode has no xattr block or if xattr is not available.
func (fs *FS) GetXAttrs(inodeNum uint32) (XAttrList, error) {
	var result XAttrList

	// Check if filesystem supports extended attributes
	if (fs.sb.FeatureCompat & 0x0008) == 0 { // EXT_ATTR feature
		// No xattr support in this filesystem
		return result, nil
	}

	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return result, err
	}

	// Check if inode has xattr block reference
	// In ext2/ext3, inode.osd2.file_acl_high (for 64-bit) or osd2.file_acl stores block number
	xattrBlock := inode.FileACL

	if xattrBlock == 0 {
		// No extended attribute block
		return result, nil
	}

	// Read xattr block
	xattrData, err := fs.readBlock(xattrBlock)
	if err != nil {
		return result, fmt.Errorf("failed to read xattr block %d: %w", xattrBlock, err)
	}

	// Parse xattr block
	return parseXAttrBlock(xattrData)
}

// parseXAttrBlock parses extended attributes from a block.
func parseXAttrBlock(data []byte) (XAttrList, error) {
	var result XAttrList

	if len(data) < 32 {
		return result, fmt.Errorf("xattr block too small")
	}

	// Verify magic number
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != 0xEA020000 {
		return result, fmt.Errorf("invalid xattr block magic: 0x%08x", magic)
	}

	// Parse entries starting at offset 36
	offset := 36

	for offset+4 < len(data) {
		nameLen := uint8(data[offset])
		nameIndex := uint8(data[offset+1])
		valueOffset := binary.LittleEndian.Uint16(data[offset+2 : offset+4])

		// Read value size at offset+4
		if offset+8 > len(data) {
			break
		}
		valueSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		// Entry header is 16 bytes, name comes immediately after
		nameStart := offset + 16
		nameEnd := nameStart + int(nameLen)

		// Validate bounds
		if nameEnd > len(data) {
			break
		}

		// Stop if we encounter a terminating entry (nameLen == 0 and nameIndex == 0)
		if nameLen == 0 && nameIndex == 0 {
			break
		}

		name := string(data[nameStart:nameEnd])

		// Read value from valueOffset in the block
		var value []byte
		if valueOffset > 0 && valueSize > 0 {
			valueEnd := int(valueOffset) + int(valueSize)
			if valueEnd <= len(data) {
				value = make([]byte, valueSize)
				copy(value, data[valueOffset:valueEnd])
			}
		}

		// Add to list with namespace prefix
		fullName := getXAttrNamespace(nameIndex)
		if name != "" {
			fullName = fullName + "." + name
		}

		result.Attrs = append(result.Attrs, XAttr{
			Name:  fullName,
			Value: value,
		})

		// Move to next entry
		// Entries are padded to 4-byte alignment
		entrySize := 16 + int(nameLen)   // header + name length
		entrySize = (entrySize + 3) & ^3 // align to 4-byte boundary
		offset += entrySize
	}

	return result, nil
}

// GetXAttrValue retrieves a specific xattr value by name.
// Returns nil if attribute not found.
func (list *XAttrList) GetXAttrValue(name string) []byte {
	for _, attr := range list.Attrs {
		if attr.Name == name {
			return attr.Value
		}
	}
	return nil
}

// ListXAttrNames returns a list of all xattr names in the list.
func (list *XAttrList) ListXAttrNames() []string {
	var names []string
	for _, attr := range list.Attrs {
		names = append(names, attr.Name)
	}
	return names
}

// Len returns the number of extended attributes.
func (list *XAttrList) Len() int {
	return len(list.Attrs)
}

// GetInlineXAttrs extracts inline extended attributes stored directly in the inode.
// For ext4, xattrs can be stored in the inode's extra space.
func (fs *FS) GetInlineXAttrs(inode *Inode) (XAttrList, error) {
	var result XAttrList

	// Check if filesystem supports inline data/xattrs
	if (fs.sb.FeatureCompat & 0x0008) == 0 {
		return result, nil
	}

	// Inline xattrs are stored in the inode's extended area (after the standard inode)
	// This is typically at offset 128+ for ext4 with i_extra_isize set
	// For now, return empty as full implementation requires inode buffer parsing

	return result, nil
}
