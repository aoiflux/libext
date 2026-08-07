package libext

import (
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

// Extended attribute name indices, as defined by ext4. The prefix is not stored
// with the name; only this index identifies the namespace, so getting the
// mapping wrong silently relabels every attribute on the filesystem.
const (
	xattrNamespaceUser           uint8 = 1
	xattrNamespacePosixACLAccess uint8 = 2
	xattrNamespacePosixACLDefaul uint8 = 3
	xattrNamespaceTrusted        uint8 = 4
	xattrNamespaceLustre         uint8 = 5
	xattrNamespaceSecurity       uint8 = 6
	xattrNamespaceSystem         uint8 = 7
	xattrNamespaceRichACL        uint8 = 8
	xattrNamespaceEncryption     uint8 = 9
	xattrNamespaceHurd           uint8 = 10
)

// getXAttrNamespace returns the namespace prefix for an attribute.
//
// A prefixed index yields a complete attribute name on its own: index 2 is
// "system.posix_acl_access" in full, with an empty stored name.
func getXAttrNamespace(index uint8) string {
	switch index {
	case xattrNamespaceUser:
		return "user"
	case xattrNamespacePosixACLAccess:
		return "system.posix_acl_access"
	case xattrNamespacePosixACLDefaul:
		return "system.posix_acl_default"
	case xattrNamespaceTrusted:
		return "trusted"
	case xattrNamespaceLustre:
		return "lustre"
	case xattrNamespaceSecurity:
		return "security"
	case xattrNamespaceSystem:
		return "system"
	case xattrNamespaceRichACL:
		return "system.richacl"
	case xattrNamespaceEncryption:
		return "c" // ext4 stores encryption context under the "c" prefix
	case xattrNamespaceHurd:
		return "gnu"
	default:
		return fmt.Sprintf("ns%d", index)
	}
}

// GetXAttrs reads extended attributes from an inode.
// Returns empty list if inode has no xattr block or if xattr is not available.
func (fs *FS) GetXAttrs(inodeNum uint32) (XAttrList, error) {
	var result XAttrList
	// Check if filesystem supports extended attributes
	if (fs.sb.FeatureCompat & featureCompatExtAttr) == 0 {
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
	attrs, err := parseXAttrBlockEntries(xattrData)
	if err != nil {
		return result, err
	}
	return fs.materializeXAttrs(attrs), nil
}

// xattrBlockHeaderSize is the fixed header of an external attribute block.
// Entries begin immediately after it, and value offsets are measured from the
// start of the block rather than from the entries.
const xattrBlockHeaderSize = 32

// parseXAttrBlockEntries decodes the entries of an external attribute block.
func parseXAttrBlockEntries(data []byte) ([]inodeXAttr, error) {
	if len(data) < xattrBlockHeaderSize {
		return nil, fmt.Errorf("%w: xattr block too small", ErrUnsupportedLayout)
	}
	if magic := le32(data, 0); magic != xattrInodeMagic {
		return nil, fmt.Errorf("%w: invalid xattr block magic 0x%08x", ErrUnsupportedLayout, magic)
	}
	return parseXAttrEntries(data, xattrBlockHeaderSize, 0, len(data))
}

// parseXAttrBlock parses extended attributes from a block.
//
// Values stored in their own inode cannot be followed from a bare block, so
// those come back empty here; GetXAttrs resolves them.
func parseXAttrBlock(data []byte) (XAttrList, error) {
	attrs, err := parseXAttrBlockEntries(data)
	if err != nil {
		return XAttrList{}, err
	}
	var result XAttrList
	for _, a := range attrs {
		name := getXAttrNamespace(a.index)
		if a.name != "" {
			name += "." + a.name
		}
		result.Attrs = append(result.Attrs, XAttr{Name: name, Value: a.value})
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

// GetInlineXAttrs extracts extended attributes stored inside the inode itself,
// in the space after the fixed fields and the extra-timestamp area.
//
// This is where security.selinux and system.posix_acl_access live on a typical
// modern system: they are small enough never to need an external block, so a
// reader that only follows the external block reports no attributes at all.
func (fs *FS) GetInlineXAttrs(inode *Inode) (XAttrList, error) {
	var result XAttrList
	if inode == nil {
		return result, nil
	}
	if (fs.sb.FeatureCompat & featureCompatExtAttr) == 0 {
		return result, nil
	}
	raw, err := fs.readInodeRaw(inode.Number)
	if err != nil {
		return result, err
	}
	attrs, err := parseInodeXAttrs(raw, inode.ExtraISize)
	if err != nil {
		return result, err
	}
	return fs.materializeXAttrs(attrs), nil
}

// materializeXAttrs converts stored attributes to the public form, fetching any
// value that lives in its own inode.
func (fs *FS) materializeXAttrs(attrs []inodeXAttr) XAttrList {
	var result XAttrList
	for _, a := range attrs {
		// system.data is the inline-data overflow, not a user-visible attribute.
		if a.index == xattrIndexSystem && a.name == inlineDataAttrName {
			continue
		}
		value := a.value
		if a.inum != 0 {
			// EA_INODE: the value is the contents of a separate inode. Without
			// following it, the attribute reads back empty.
			v, err := fs.readXAttrValueInode(a.inum)
			if err != nil {
				fs.warn(WarnDegradedRead, "EA_INODE",
					fmt.Sprintf("xattr %q value inode %d unreadable: %v", a.name, a.inum, err))
			} else {
				value = v
			}
		}
		name := getXAttrNamespace(a.index)
		if a.name != "" {
			name += "." + a.name
		}
		result.Attrs = append(result.Attrs, XAttr{Name: name, Value: value})
	}
	return result
}

// readXAttrValueInode reads an attribute value stored in its own inode.
func (fs *FS) readXAttrValueInode(inodeNum uint32) ([]byte, error) {
	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return nil, err
	}
	return fs.readInodeData(inode)
}
