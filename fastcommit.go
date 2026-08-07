package libext

import (
	"encoding/binary"
	"fmt"
)

// ext4 fast commit.
//
// Fast commit records individual filesystem operations in the tail of the
// journal instead of copying whole metadata blocks. For a deleted-file
// investigation the interesting one is EXT4_FC_TAG_UNLINK, which stores the
// parent inode and the filename of a deletion: often the only record of a very
// recent unlink, since the inode's own evidence may already be gone.
//
// Like the rest of the journal, these structures are big-endian.

// FastCommitTag identifies a fast-commit record.
type FastCommitTag uint16

const (
	FastCommitTagAddRange FastCommitTag = 1
	FastCommitTagDelRange FastCommitTag = 2
	FastCommitTagCreat    FastCommitTag = 3
	FastCommitTagLink     FastCommitTag = 4
	FastCommitTagUnlink   FastCommitTag = 5
	FastCommitTagInode    FastCommitTag = 6
	FastCommitTagPad      FastCommitTag = 7
	FastCommitTagTail     FastCommitTag = 8
	FastCommitTagHead     FastCommitTag = 9
)

func (t FastCommitTag) String() string {
	switch t {
	case FastCommitTagAddRange:
		return "add_range"
	case FastCommitTagDelRange:
		return "del_range"
	case FastCommitTagCreat:
		return "creat"
	case FastCommitTagLink:
		return "link"
	case FastCommitTagUnlink:
		return "unlink"
	case FastCommitTagInode:
		return "inode"
	case FastCommitTagPad:
		return "pad"
	case FastCommitTagTail:
		return "tail"
	case FastCommitTagHead:
		return "head"
	default:
		return fmt.Sprintf("tag%d", uint16(t))
	}
}

// FastCommitOp is one operation recorded in the fast-commit area.
type FastCommitOp struct {
	Tag FastCommitTag

	// Inode is the subject of the operation, and Parent the directory it was
	// linked into. Both are 0 for records that do not carry them.
	Inode  uint32
	Parent uint32

	// Name is the filename for creat, link and unlink records.
	Name string

	// Block is the journal block the record was found in.
	Block uint64
}

// fastCommitTLSize is the size of the tag/length header preceding each record.
const fastCommitTLSize = 4

// FastCommitOps returns the operations recorded in the fast-commit area.
//
// The area occupies the last s_num_fc_blks blocks of the journal. A filesystem
// without the feature returns no operations and no error.
func (fs *FS) FastCommitOps() ([]FastCommitOp, error) {
	jsb, err := fs.JournalSuperblock()
	if err != nil {
		return nil, err
	}
	if !jsb.HasFastCommit() || jsb.FastCommitBlocks == 0 {
		return nil, nil
	}

	blocks, err := fs.journalBlocks()
	if err != nil {
		return nil, err
	}
	fcCount := int(jsb.FastCommitBlocks)
	if fcCount > len(blocks) {
		fcCount = len(blocks)
	}
	start := len(blocks) - fcCount

	var ops []FastCommitOp
	for i := start; i < len(blocks); i++ {
		data, err := fs.readBlock(blocks[i])
		if err != nil {
			fs.warn(WarnDegradedRead, "fast_commit",
				fmt.Sprintf("fast commit block %d unreadable: %v", i, err))
			continue
		}
		ops = append(ops, parseFastCommitBlock(data, uint64(i))...)
	}
	return ops, nil
}

// parseFastCommitBlock walks the tag/length records in one block.
func parseFastCommitBlock(data []byte, block uint64) []FastCommitOp {
	var ops []FastCommitOp

	for off := 0; off+fastCommitTLSize <= len(data); {
		tag := FastCommitTag(binary.BigEndian.Uint16(data[off : off+2]))
		length := int(binary.BigEndian.Uint16(data[off+2 : off+4]))

		if tag == 0 || length < 0 || off+fastCommitTLSize+length > len(data) {
			break
		}
		body := data[off+fastCommitTLSize : off+fastCommitTLSize+length]

		if op, ok := parseFastCommitRecord(tag, body, block); ok {
			ops = append(ops, op)
		}
		if tag == FastCommitTagTail {
			break
		}
		off += fastCommitTLSize + length
	}
	return ops
}

// parseFastCommitRecord decodes one record body.
func parseFastCommitRecord(tag FastCommitTag, body []byte, block uint64) (FastCommitOp, bool) {
	op := FastCommitOp{Tag: tag, Block: block}

	switch tag {
	case FastCommitTagCreat, FastCommitTagLink, FastCommitTagUnlink:
		// struct ext4_fc_dentry_info: parent inode, inode, then the name.
		if len(body) < 8 {
			return op, false
		}
		op.Parent = binary.BigEndian.Uint32(body[0:4])
		op.Inode = binary.BigEndian.Uint32(body[4:8])
		name := body[8:]
		if i := indexByteZero(name); i >= 0 {
			name = name[:i]
		}
		op.Name = string(name)
		return op, true

	case FastCommitTagInode, FastCommitTagAddRange, FastCommitTagDelRange:
		if len(body) < 4 {
			return op, false
		}
		op.Inode = binary.BigEndian.Uint32(body[0:4])
		return op, true

	case FastCommitTagPad, FastCommitTagTail, FastCommitTagHead:
		return op, false

	default:
		return op, false
	}
}

// indexByteZero returns the offset of the first NUL, or -1.
func indexByteZero(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}
