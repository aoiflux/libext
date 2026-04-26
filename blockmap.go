package libext

import (
	"encoding/binary"
	"fmt"
)

type extentRecord struct {
	logicalStart uint32
	length       uint32
	physical     uint64
	unwritten    bool
}

func (fs *FS) inodeBlockNumber(inode Inode, logical uint64) (uint64, bool, error) {
	if inode.HasExtents || fs.kind == FSKindExt4 || (fs.sb.FeatureIncompat&featureIncompatExtents) != 0 {
		recs, err := fs.parseExtentTree(inode.BlockRaw[:])
		if err == nil && len(recs) > 0 {
			for _, rec := range recs {
				start := uint64(rec.logicalStart)
				end := start + uint64(rec.length)
				if logical < start || logical >= end {
					continue
				}
				if rec.unwritten {
					return 0, false, nil
				}
				return rec.physical + (logical - start), true, nil
			}
			return 0, false, nil
		}
	}
	return fs.classicBlockNumber(inode.BlockRaw[:], logical)
}

func (fs *FS) classicBlockNumber(blockRaw []byte, logical uint64) (uint64, bool, error) {
	ptrs := make([]uint32, 15)
	for i := 0; i < 15; i++ {
		ptrs[i] = binary.LittleEndian.Uint32(blockRaw[i*4 : i*4+4])
	}

	entriesPerBlock := uint64(fs.sb.BlockSize / 4)
	if logical < 12 {
		p := ptrs[logical]
		if p == 0 {
			return 0, false, nil
		}
		return uint64(p), true, nil
	}

	logical -= 12
	if logical < entriesPerBlock {
		if ptrs[12] == 0 {
			return 0, false, nil
		}
		p, err := fs.readPointerFromBlock(uint64(ptrs[12]), logical)
		if err != nil {
			return 0, false, err
		}
		if p == 0 {
			return 0, false, nil
		}
		return uint64(p), true, nil
	}

	logical -= entriesPerBlock
	doubleSpan := entriesPerBlock * entriesPerBlock
	if logical < doubleSpan {
		if ptrs[13] == 0 {
			return 0, false, nil
		}
		l1 := logical / entriesPerBlock
		l2 := logical % entriesPerBlock
		p1, err := fs.readPointerFromBlock(uint64(ptrs[13]), l1)
		if err != nil {
			return 0, false, err
		}
		if p1 == 0 {
			return 0, false, nil
		}
		p2, err := fs.readPointerFromBlock(uint64(p1), l2)
		if err != nil {
			return 0, false, err
		}
		if p2 == 0 {
			return 0, false, nil
		}
		return uint64(p2), true, nil
	}

	logical -= doubleSpan
	tripleSpan := entriesPerBlock * entriesPerBlock * entriesPerBlock
	if logical >= tripleSpan {
		return 0, false, nil
	}
	if ptrs[14] == 0 {
		return 0, false, nil
	}

	l1 := logical / (entriesPerBlock * entriesPerBlock)
	rem := logical % (entriesPerBlock * entriesPerBlock)
	l2 := rem / entriesPerBlock
	l3 := rem % entriesPerBlock

	p1, err := fs.readPointerFromBlock(uint64(ptrs[14]), l1)
	if err != nil {
		return 0, false, err
	}
	if p1 == 0 {
		return 0, false, nil
	}
	p2, err := fs.readPointerFromBlock(uint64(p1), l2)
	if err != nil {
		return 0, false, err
	}
	if p2 == 0 {
		return 0, false, nil
	}
	p3, err := fs.readPointerFromBlock(uint64(p2), l3)
	if err != nil {
		return 0, false, err
	}
	if p3 == 0 {
		return 0, false, nil
	}
	return uint64(p3), true, nil
}

func (fs *FS) readPointerFromBlock(block uint64, idx uint64) (uint32, error) {
	blk, err := fs.readBlock(block)
	if err != nil {
		return 0, err
	}
	max := uint64(len(blk) / 4)
	if idx >= max {
		return 0, nil
	}
	off := idx * 4
	return binary.LittleEndian.Uint32(blk[off : off+4]), nil
}

func (fs *FS) parseExtentTree(root []byte) ([]extentRecord, error) {
	if len(root) < 12 {
		return nil, ErrUnsupportedLayout
	}
	if le16(root, 0) != 0xF30A {
		return nil, ErrUnsupportedLayout
	}
	return fs.parseExtentNode(root)
}

func (fs *FS) parseExtentNode(node []byte) ([]extentRecord, error) {
	if len(node) < 12 {
		return nil, ErrUnsupportedLayout
	}
	entries := int(le16(node, 2))
	depth := int(le16(node, 6))
	if entries < 0 || entries > 340 {
		return nil, ErrUnsupportedLayout
	}

	if depth == 0 {
		recs := make([]extentRecord, 0, entries)
		for i := 0; i < entries; i++ {
			off := 12 + i*12
			if off+12 > len(node) {
				return nil, ErrUnsupportedLayout
			}
			logical := le32(node, off)
			lenFlags := le16(node, off+4)
			unwritten := (lenFlags & 0x8000) != 0
			lenBlocks := uint32(lenFlags & 0x7FFF)
			if unwritten && lenBlocks == 0 {
				lenBlocks = uint32(lenFlags - 0x8000)
			}
			if lenBlocks == 0 {
				continue
			}
			startHi := le16(node, off+6)
			startLo := le32(node, off+8)
			start := (uint64(startHi) << 32) | uint64(startLo)
			recs = append(recs, extentRecord{
				logicalStart: logical,
				length:       lenBlocks,
				physical:     start,
				unwritten:    unwritten,
			})
		}
		return recs, nil
	}

	recs := make([]extentRecord, 0, entries*8)
	for i := 0; i < entries; i++ {
		off := 12 + i*12
		if off+12 > len(node) {
			return nil, ErrUnsupportedLayout
		}
		leafLo := le32(node, off+4)
		leafHi := le16(node, off+8)
		leaf := (uint64(leafHi) << 32) | uint64(leafLo)
		child, err := fs.readBlock(leaf)
		if err != nil {
			return nil, fmt.Errorf("read extent node block %d: %w", leaf, err)
		}
		leafRecs, err := fs.parseExtentNode(child)
		if err != nil {
			return nil, err
		}
		recs = append(recs, leafRecs...)
	}
	return recs, nil
}
