package libext

import (
	"context"
	"encoding/binary"
	"errors"
)

// The journal index.
//
// Asking the journal what a block looked like before means walking every
// descriptor block in it and collecting the tags that name that block. The walk
// is the entire cost, and none of it depends on which block is being asked
// about - so asking about a thousand blocks the direct way walks the journal a
// thousand times, over a structure that is 128 MB by default and may be a
// gigabyte.
//
// JournalIndex walks once and keeps the tag table. That table is small: a few
// tens of bytes per journalled block, against the block's own contents. The
// contents stay on disk and are read per query, because holding them would mean
// holding the whole journal in memory.
//
// This is the same shape as PathIndex in path.go and exists for the same
// reason. Like PathIndex it belongs to the caller rather than to the FS: it is
// built when it is wanted, and the FS keeps no unbounded state of its own.

// JournalIndex answers questions about journalled block contents from a single
// walk of the journal.
//
// It is safe for concurrent use once built, since nothing writes to it
// afterwards. Its queries read through the FS it was built from, so that FS
// must still be open; queries on a closed FS fail the way any other read does.
//
// The index reflects the journal as it stood when the walk ran. Nothing in this
// library writes to an image, so for a file that is not being modified
// underneath it that is simply the journal.
type JournalIndex struct {
	fs *FS

	// txs is the transaction list the index was built from, in the order
	// ListJournalTransactions produced it.
	txs []JournalTransaction

	// blocks maps a journal block number to the filesystem block holding it,
	// kept so a query resolves against exactly the block map the tags were read
	// with rather than re-resolving it.
	blocks []uint64

	// byBlock maps a filesystem block number to the journalled copies of it,
	// newest first.
	byBlock map[uint64][]journalCopy
}

// journalCopy locates one journalled copy of a filesystem block.
type journalCopy struct {
	journalBlock uint64
	escaped      bool
}

// BuildJournalIndex walks the journal once and returns an index over it.
//
// Use it whenever more than a couple of blocks or inodes will be asked about:
// JournalBlockCopies and JournalInodeVersions each perform this walk per call,
// which is what makes them unsuitable for a loop.
//
// A filesystem with no journal is an error rather than an empty index, so that
// "nothing was journalled" and "there is nothing to journal into" stay
// distinguishable.
func (fs *FS) BuildJournalIndex() (*JournalIndex, error) {
	return fs.BuildJournalIndexContext(context.Background())
}

// BuildJournalIndexContext is BuildJournalIndex with cancellation.
func (fs *FS) BuildJournalIndexContext(ctx context.Context) (*JournalIndex, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	txs, err := fs.ListJournalTransactionsContext(ctx)
	if err != nil {
		return nil, err
	}
	blocks, err := fs.journalBlocks()
	if err != nil {
		return nil, err
	}

	ix := &JournalIndex{
		fs:      fs,
		txs:     txs,
		blocks:  blocks,
		byBlock: make(map[uint64][]journalCopy, len(txs)),
	}

	// Transactions are visited from the end of the list backwards, and each
	// transaction's tags in order, so that every block's copies come out newest
	// first. That is the order JournalBlockCopies has always returned, and it
	// takes the transaction list's own order as age order - which is journal
	// block order, not sequence order, and so is approximate across the point
	// where a circular journal wraps. Preserved deliberately: silently changing
	// which copy a recovery treats as most recent would be a worse fault than
	// the approximation.
	for i := len(txs) - 1; i >= 0; i-- {
		if (len(txs)-1-i)%cancellationCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for _, tag := range txs[i].Tags {
			if tag.JournalBlock >= uint64(len(blocks)) {
				continue
			}
			ix.byBlock[tag.FSBlock] = append(ix.byBlock[tag.FSBlock], journalCopy{
				journalBlock: tag.JournalBlock,
				escaped:      tag.Escaped,
			})
		}
	}
	return ix, nil
}

// Transactions returns the transactions the index was built from, in journal
// order.
//
// The outer slice is a copy, so appending to it or reordering it cannot disturb
// the index. The Tags slices inside are shared with it and must not be written
// to.
func (ix *JournalIndex) Transactions() []JournalTransaction {
	return append([]JournalTransaction(nil), ix.txs...)
}

// Len reports how many distinct filesystem blocks the journal holds copies of.
func (ix *JournalIndex) Len() int { return len(ix.byBlock) }

// HasBlock reports whether the journal holds any copy of a filesystem block.
//
// This is the cheap question, answered from the map. A changed-block analysis
// asks it of a great many blocks and reads only the ones that say yes.
func (ix *JournalIndex) HasBlock(fsBlock uint64) bool {
	return len(ix.byBlock[fsBlock]) > 0
}

// CopyCount reports how many journalled copies of a filesystem block the index
// holds, without reading any of them.
func (ix *JournalIndex) CopyCount(fsBlock uint64) int { return len(ix.byBlock[fsBlock]) }

// BlockCopies returns every journalled copy of a filesystem block, newest
// first.
//
// Each copy is the block's content at the moment its transaction was written,
// so this exposes prior states of metadata the live filesystem has since
// overwritten. A block the journal never carried yields nil and no error: not
// having been journalled is an ordinary fact about a block, not a failure.
//
// The reads are bounded by how many copies of this one block exist, which is
// small, so there is no Context form - the walk that was worth interrupting
// already happened at BuildJournalIndex.
//
// A copy whose block cannot be read is skipped rather than failing the call, on
// the same grounds the walk uses: the copies that survive are still evidence.
func (ix *JournalIndex) BlockCopies(fsBlock uint64) ([][]byte, error) {
	entries := ix.byBlock[fsBlock]
	if len(entries) == 0 {
		return nil, nil
	}
	var copies [][]byte
	for _, c := range entries {
		data, err := ix.fs.readBlock(ix.blocks[c.journalBlock])
		if err != nil {
			continue
		}
		if c.escaped && len(data) >= 4 {
			// An escaped block had its first word replaced because it happened
			// to start with the journal magic; restore it.
			data = append([]byte(nil), data...)
			binary.BigEndian.PutUint32(data[0:4], journalMagic)
		}
		copies = append(copies, data)
	}
	return copies, nil
}

// InodeVersions returns prior on-disk states of an inode, recovered from
// journalled copies of the inode table block that holds it.
//
// This is what can resurrect a deleted file's extent tree: unlink zeroes the
// tree in the live inode, but a journalled copy of the same block from before
// the unlink still carries it. Newest first, as BlockCopies.
func (ix *JournalIndex) InodeVersions(inodeNum uint32) ([]Inode, error) {
	block, offInBlock, err := ix.fs.inodeTableLocation(inodeNum)
	if err != nil {
		return nil, err
	}
	copies, err := ix.BlockCopies(block)
	if err != nil {
		return nil, err
	}
	return decodeInodeVersions(copies, inodeNum, offInBlock, uint64(ix.fs.sb.InodeSize)), nil
}

// decodeInodeVersions pulls one inode out of each journalled copy of the block
// that holds it.
//
// A copy too short to contain the entry is skipped: the journal records whole
// blocks, so a short one is a truncated read rather than a shorter inode.
func decodeInodeVersions(copies [][]byte, inodeNum uint32, offInBlock, inodeSize uint64) []Inode {
	var versions []Inode
	for _, data := range copies {
		if offInBlock+inodeSize > uint64(len(data)) {
			continue
		}
		raw := data[offInBlock : offInBlock+inodeSize]
		versions = append(versions, parseInode(raw, inodeNum))
	}
	return versions
}

// inodeTableLocation reports which filesystem block holds an inode's table
// entry, and the entry's offset within that block.
//
// It is the one statement of that arithmetic, so the live read and the
// journalled read cannot disagree about where an inode lives.
func (fs *FS) inodeTableLocation(inodeNum uint32) (block uint64, offInBlock uint64, err error) {
	if inodeNum == 0 || inodeNum > fs.sb.InodesCount {
		return 0, 0, ErrInvalidInode
	}
	group := (inodeNum - 1) / fs.sb.InodesPerGroup
	index := (inodeNum - 1) % fs.sb.InodesPerGroup
	if group >= uint32(len(fs.groups)) {
		return 0, 0, ErrInvalidInode
	}

	byteOff := uint64(index) * uint64(fs.sb.InodeSize)
	blockSize := uint64(fs.sb.BlockSize)
	return fs.groups[group].InodeTableBlock + byteOff/blockSize, byteOff % blockSize, nil
}
