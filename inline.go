package libext

import "fmt"

// Inline data.
//
// A small file or directory can live entirely inside its inode. The first 60
// bytes occupy the block-pointer area, and any remainder is stored as the value
// of a system.data extended attribute in the inode's own xattr space. Such an
// inode has no blocks at all, so a reader that only follows block maps reports
// it as empty rather than as small.

const (
	// inlineDataInBlock is how many bytes fit in the block-pointer area.
	inlineDataInBlock = 60

	// xattrIndexSystem is the namespace holding system.data.
	xattrIndexSystem uint8 = 7

	// inlineDataAttrName is the attribute carrying the overflow.
	inlineDataAttrName = "data"

	// xattrInodeMagic marks the xattr area inside an inode.
	xattrInodeMagic = 0xEA020000

	// xattrEntryHeaderSize is the fixed part of an attribute entry.
	xattrEntryHeaderSize = 16
)

// HasInlineData reports whether an inode stores its contents in the inode.
func (fs *FS) HasInlineData(inode Inode) bool {
	return inode.HasInline
}

// InlineData returns the contents of an inode that stores them inline.
//
// The second result reports whether the inode is inline at all; a regular file
// returns (nil, false, nil).
func (fs *FS) InlineData(inodeNum uint32) ([]byte, bool, error) {
	raw, err := fs.readInodeRaw(inodeNum)
	if err != nil {
		return nil, false, err
	}
	inode := parseInode(raw, inodeNum)
	if !inode.HasInline {
		return nil, false, nil
	}
	data, err := fs.inlineDataFromRaw(inode, raw)
	return data, true, err
}

// inlineDataFromRaw assembles inline contents from an already-read inode.
func (fs *FS) inlineDataFromRaw(inode Inode, raw []byte) ([]byte, error) {
	out := make([]byte, 0, inode.Size)

	head := inode.BlockRaw[:]
	if inode.Size < uint64(len(head)) {
		head = head[:inode.Size]
	}
	out = append(out, head...)

	if inode.Size <= inlineDataInBlock {
		return out, nil
	}

	// The remainder lives in system.data. Without it the file silently
	// truncates to 60 bytes rather than failing.
	attrs, err := parseInodeXAttrs(raw, inode.ExtraISize)
	if err != nil {
		return out, fmt.Errorf("inode %d inline overflow: %w", inode.Number, err)
	}
	for _, a := range attrs {
		if a.index == xattrIndexSystem && a.name == inlineDataAttrName {
			out = append(out, a.value...)
			break
		}
	}

	if uint64(len(out)) > inode.Size {
		out = out[:inode.Size]
	}
	return out, nil
}

// inlineDirEntries parses the directory format used inside an inode.
//
// It differs from an on-disk directory block in two ways that break a normal
// parser: the first four bytes are the parent inode rather than a record, and
// there are no "." or ".." entries at all.
func (fs *FS) inlineDirEntries(inode Inode, data []byte) ([]DirEntry, error) {
	if len(data) < 4 {
		return nil, nil
	}

	parent := le32(data, 0)
	entries := []DirEntry{
		{Inode: inode.Number, Name: ".", FileType: extDirentTypeDirectory, IsDirectory: true},
		{Inode: parent, Name: "..", FileType: extDirentTypeDirectory, IsDirectory: true},
	}

	rest, err := fs.parseDirEntries(data[4:])
	entries = append(entries, rest...)
	return entries, err
}

// inodeXAttr is one attribute as stored, before namespace prefixing.
type inodeXAttr struct {
	index uint8
	name  string
	value []byte
	inum  uint32 // e_value_inum: the value lives in its own inode
}

// parseInodeXAttrs reads the attributes stored in an inode's own xattr area,
// which begins after the fixed 128-byte inode and the extra fields.
func parseInodeXAttrs(raw []byte, extraISize uint16) ([]inodeXAttr, error) {
	start := inodeBaseSize + int(extraISize)
	if start+4 > len(raw) {
		return nil, nil
	}
	if le32(raw, start) != xattrInodeMagic {
		return nil, nil
	}

	// Entries begin after the 4-byte magic, and value offsets are relative to
	// that same point rather than to the start of the inode.
	base := start + 4
	return parseXAttrEntries(raw, base, base, len(raw))
}

// parseXAttrEntries walks a run of attribute entries.
//
// entriesAt is where the entry list starts; valueBase is what e_value_offs is
// measured from, which differs between in-inode and block-resident attributes.
func parseXAttrEntries(data []byte, entriesAt, valueBase, limit int) ([]inodeXAttr, error) {
	var out []inodeXAttr

	for off := entriesAt; off+xattrEntryHeaderSize <= limit; {
		nameLen := int(data[off])
		nameIndex := data[off+1]
		valueOffs := int(le16(data, off+2))
		valueInum := le32(data, off+4)
		valueSize := int(le32(data, off+8))

		// The list ends at the first entry whose leading 32 bits are zero, which
		// covers e_name_len, e_name_index and e_value_offs. The remaining header
		// words must not be included in the test: entries grow forward while
		// values are packed backward from the end, so the bytes after the
		// terminator are usually the tail of a value and are rarely zero.
		if nameLen == 0 && nameIndex == 0 && valueOffs == 0 {
			break
		}
		if off+xattrEntryHeaderSize+nameLen > limit {
			return out, fmt.Errorf("%w: xattr name overruns the area", ErrUnsupportedLayout)
		}

		attr := inodeXAttr{
			index: nameIndex,
			name:  string(data[off+xattrEntryHeaderSize : off+xattrEntryHeaderSize+nameLen]),
			inum:  valueInum,
		}

		// A value stored in its own inode is fetched separately; here only the
		// reference is recorded.
		if valueInum == 0 && valueSize > 0 {
			start := valueBase + valueOffs
			end := start + valueSize
			if start < 0 || end > limit || start > end {
				return out, fmt.Errorf("%w: xattr value outside the area", ErrUnsupportedLayout)
			}
			attr.value = append([]byte(nil), data[start:end]...)
		}
		out = append(out, attr)

		// Entries are padded to a four-byte boundary.
		off += (xattrEntryHeaderSize + nameLen + 3) &^ 3
	}
	return out, nil
}
