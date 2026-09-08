package libext

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// The ext3/ext4 journal (JBD2).
//
// Every structure here is big-endian, unlike the rest of the filesystem.
//
// The journal matters for forensics beyond recovery: it holds copies of
// metadata blocks as they were *before* the change that superseded them. ext4
// zeroes an inode's extent tree when the file is unlinked, so the journalled
// copy of that inode table block is frequently the only place the deleted
// file's block map still exists.

// journalMagic is JBD2_MAGIC_NUMBER, the header magic on every journal block.
const journalMagic = 0xC03B3998

// Journal block types.
const (
	JournalBlockTypeDescriptor   = 1
	JournalBlockTypeCommit       = 2
	JournalBlockTypeSuperblockV1 = 3
	JournalBlockTypeSuperblockV2 = 4
	JournalBlockTypeRevoke       = 5

	// Retained for compatibility with earlier releases of this package. The
	// values do not correspond to JBD2 block types.
	JournalBlockTypeSuperblock = 0
	JournalBlockTypeEscaped    = 4
	JournalBlockTypeOrphan     = 3
)

// Journal tag flags.
const (
	JournalTagFlagEscaped = 0x1
	JournalTagFlagSameBuf = 0x2 // the tag reuses the previous UUID
	JournalTagFlagDeleted = 0x4
	JournalTagFlagLastTag = 0x8
)

// Journal feature flags.
const (
	journalIncompatRevoke      = 0x0001
	journalIncompat64Bit       = 0x0002
	journalIncompatAsyncCommit = 0x0004
	journalIncompatCSumV2      = 0x0008
	journalIncompatCSumV3      = 0x0010
	journalIncompatFastCommit  = 0x0020
)

const (
	journalHeaderSize = 12
	journalUUIDSize   = 16
)

// JournalSuperblock represents the journal superblock, which occupies the first
// block of the journal.
type JournalSuperblock struct {
	BlockSize        uint32
	MaxLen           uint32
	FirstBlock       uint32
	Sequence         uint32
	Start            uint32
	ErrCode          uint32
	CompatFeatures   uint32
	IncompatFeatures uint32
	RoCompatFeatures uint32
	UUID             [16]byte
	NumUsers         uint32
	DynSuperblock    uint32
	MaxTransaction   uint32
	MaxTransData     uint32
	ChecksumType     uint8
	FastCommitBlocks uint32
}

// Has64BitTags reports whether block numbers in tags carry a high word.
func (j JournalSuperblock) Has64BitTags() bool {
	return j.IncompatFeatures&journalIncompat64Bit != 0
}

// HasCSumV3 reports whether descriptor tags use the wider v3 layout.
func (j JournalSuperblock) HasCSumV3() bool {
	return j.IncompatFeatures&journalIncompatCSumV3 != 0
}

// HasFastCommit reports whether the journal reserves fast-commit blocks.
func (j JournalSuperblock) HasFastCommit() bool {
	return j.IncompatFeatures&journalIncompatFastCommit != 0
}

// JournalBlockTag names one filesystem block whose contents were journalled.
type JournalBlockTag struct {
	// FSBlock is the filesystem block this journalled copy belongs to.
	FSBlock uint64 `json:"fs_block"`
	// JournalBlock is where the copy sits inside the journal.
	JournalBlock uint64 `json:"journal_block"`
	Escaped      bool   `json:"escaped"`
	SameUUID     bool   `json:"same_uuid"`
	LastTag      bool   `json:"last_tag"`
}

// JournalTransaction represents a single transaction in the journal.
type JournalTransaction struct {
	Sequence   uint32 `json:"sequence"`
	StartBlock uint32 `json:"start_block"`
	Type       string `json:"type"`

	// Timestamp is the commit time, taken from the matching commit block. It is
	// zero for a transaction whose commit block was never written, which itself
	// says the transaction did not complete.
	//
	// It is omitted from the JSON rather than encoded as a zero time. A
	// transaction that never committed has no commit time, and emitting one
	// would invent the very event its absence is evidence against.
	Timestamp   time.Time `json:"timestamp,omitzero"`
	IsCommitted bool      `json:"is_committed"`
	BlockCount  uint32    `json:"block_count"`

	// Tags names the filesystem blocks this transaction carries copies of. A
	// revoke record carries none.
	Tags []JournalBlockTag `json:"tags"`
}

// GetJournalLocation returns the location of the journal device.
func (fs *FS) GetJournalLocation() (journalBlock uint32, isExternal bool, err error) {
	if (fs.sb.FeatureCompat & featureCompatHasJournal) == 0 {
		return 0, false, fmt.Errorf("journal feature not enabled")
	}
	if fs.sb.JournalInode != 0 {
		return 0, false, nil
	}
	return 0, true, nil
}

// GetJournalInode returns the inode number of the journal.
func (fs *FS) GetJournalInode() uint32 {
	if (fs.sb.FeatureCompat & featureCompatHasJournal) == 0 {
		return 0
	}
	return fs.sb.JournalInode
}

// ParseJournalSuperblock parses a journal superblock from raw data.
func ParseJournalSuperblock(data []byte) (*JournalSuperblock, error) {
	if len(data) < 256 {
		return nil, fmt.Errorf("journal superblock too small")
	}
	if magic := binary.BigEndian.Uint32(data[0:4]); magic != journalMagic {
		return nil, fmt.Errorf("%w: journal magic 0x%08x", ErrUnsupportedLayout, magic)
	}

	// Fields follow the 12-byte journal header. Reading them from the start of
	// the block instead shifts every value by two words.
	jsb := &JournalSuperblock{
		BlockSize:        binary.BigEndian.Uint32(data[12:16]),
		MaxLen:           binary.BigEndian.Uint32(data[16:20]),
		FirstBlock:       binary.BigEndian.Uint32(data[20:24]),
		Sequence:         binary.BigEndian.Uint32(data[24:28]),
		Start:            binary.BigEndian.Uint32(data[28:32]),
		ErrCode:          binary.BigEndian.Uint32(data[32:36]),
		CompatFeatures:   binary.BigEndian.Uint32(data[36:40]),
		IncompatFeatures: binary.BigEndian.Uint32(data[40:44]),
		RoCompatFeatures: binary.BigEndian.Uint32(data[44:48]),
		NumUsers:         binary.BigEndian.Uint32(data[64:68]),
		DynSuperblock:    binary.BigEndian.Uint32(data[68:72]),
		MaxTransaction:   binary.BigEndian.Uint32(data[72:76]),
		MaxTransData:     binary.BigEndian.Uint32(data[76:80]),
		ChecksumType:     data[80],
		FastCommitBlocks: binary.BigEndian.Uint32(data[84:88]),
	}
	copy(jsb.UUID[:], data[48:64])
	return jsb, nil
}

// JournalSuperblock reads and parses the journal's own superblock.
func (fs *FS) JournalSuperblock() (*JournalSuperblock, error) {
	blocks, err := fs.journalBlocks()
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("journal has no blocks")
	}
	data, err := fs.readBlock(blocks[0])
	if err != nil {
		return nil, fmt.Errorf("read journal superblock: %w", err)
	}
	return ParseJournalSuperblock(data)
}

// journalBlocks returns the physical blocks holding the journal, resolving them
// on first use and sharing the result afterwards.
//
// The returned slice is shared, not copied: every caller in this package reads
// it and none retains or mutates it. It is not exposed.
func (fs *FS) journalBlocks() ([]uint64, error) {
	fs.journalMu.RLock()
	if fs.journalResolved {
		blocks, err := fs.journalBlockSet, fs.journalErr
		fs.journalMu.RUnlock()
		return blocks, err
	}
	fs.journalMu.RUnlock()

	fs.journalMu.Lock()
	defer fs.journalMu.Unlock()
	// Another goroutine may have resolved it between the two locks.
	if fs.journalResolved {
		return fs.journalBlockSet, fs.journalErr
	}
	fs.journalBlockSet, fs.journalErr = fs.resolveJournalBlocks()
	fs.journalResolved = true
	return fs.journalBlockSet, fs.journalErr
}

// resolveJournalBlocks maps the journal inode to the physical blocks holding it.
//
// The journal is read block by block through this list rather than loaded whole:
// a 1 GiB journal is normal on a large filesystem.
func (fs *FS) resolveJournalBlocks() ([]uint64, error) {
	num := fs.GetJournalInode()
	if num == 0 {
		return nil, fmt.Errorf("no journal inode found")
	}
	inode, err := fs.ReadInode(num)
	if err != nil {
		return nil, fmt.Errorf("read journal inode: %w", err)
	}

	exts, err := fs.InodeExtents(inode, ExtentOptions{OmitSparse: true})
	if err != nil {
		return nil, fmt.Errorf("journal block map: %w", err)
	}

	var blocks []uint64
	for _, e := range exts {
		if e.Sparse() || e.Inline() {
			continue
		}
		for b := uint64(0); b < e.Blocks; b++ {
			blocks = append(blocks, e.PhysicalBlock+b)
		}
	}
	return blocks, nil
}

// ListJournalTransactions lists transactions in the journal.
//
// Each descriptor block starts a transaction and names the filesystem blocks
// whose copies follow it; the matching commit block supplies the time.
//
// Every block of the journal is read. That is a quarter of a million reads for
// the 1 GiB journal a large filesystem carries, so prefer the Context form
// where the caller may want to give up.
func (fs *FS) ListJournalTransactions() ([]JournalTransaction, error) {
	return fs.ListJournalTransactionsContext(context.Background())
}

// ListJournalTransactionsContext is ListJournalTransactions with cancellation.
func (fs *FS) ListJournalTransactionsContext(ctx context.Context) ([]JournalTransaction, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	blocks, err := fs.journalBlocks()
	if err != nil {
		return nil, err
	}
	jsb, err := fs.JournalSuperblock()
	if err != nil {
		return nil, err
	}

	var transactions []JournalTransaction
	// A transaction can span several blocks — descriptors and revokes alike —
	// and one commit block completes all of them, so the sequence maps to every
	// entry awaiting it rather than to just the last.
	pending := make(map[uint32][]int)

	// scanned counts from zero independently of i, which starts at 1: pacing off
	// the loop index would put the first check at block 1024 and leave a short
	// journal uncancellable.
	scanned := 0
	for i := 1; i < len(blocks); i++ {
		if scanned%cancellationCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		scanned++

		data, err := fs.readBlock(blocks[i])
		if err != nil {
			fs.warn(WarnDegradedRead, "journal",
				fmt.Sprintf("journal block %d unreadable: %v", i, err))
			continue
		}
		if len(data) < journalHeaderSize {
			continue
		}
		if binary.BigEndian.Uint32(data[0:4]) != journalMagic {
			// Data blocks carry no header; only tagged blocks do.
			continue
		}

		blockType := binary.BigEndian.Uint32(data[4:8])
		sequence := binary.BigEndian.Uint32(data[8:12])

		switch blockType {
		case JournalBlockTypeDescriptor:
			tags := parseJournalTags(data, jsb, uint64(i))
			transactions = append(transactions, JournalTransaction{
				Sequence:   sequence,
				StartBlock: uint32(i),
				Type:       "descriptor",
				BlockCount: uint32(len(tags)),
				Tags:       tags,
			})
			pending[sequence] = append(pending[sequence], len(transactions)-1)

		case JournalBlockTypeCommit:
			commitTime := parseCommitTime(data)
			for _, idx := range pending[sequence] {
				transactions[idx].IsCommitted = true
				transactions[idx].Timestamp = commitTime
			}
			delete(pending, sequence)

		case JournalBlockTypeRevoke:
			transactions = append(transactions, JournalTransaction{
				Sequence:   sequence,
				StartBlock: uint32(i),
				Type:       "revoke",
			})
			pending[sequence] = append(pending[sequence], len(transactions)-1)
		}
	}

	return transactions, nil
}

// parseCommitTime reads the commit timestamp from a commit block.
//
// It sits after the checksum array, not immediately after the header.
func parseCommitTime(data []byte) time.Time {
	const (
		commitSecOffset  = 48
		commitNsecOffset = 56
	)
	if len(data) < commitNsecOffset+4 {
		return time.Time{}
	}
	sec := binary.BigEndian.Uint64(data[commitSecOffset : commitSecOffset+8])
	nsec := binary.BigEndian.Uint32(data[commitNsecOffset : commitNsecOffset+4])
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), int64(nsec)).UTC()
}

// parseJournalTags walks the tag array of a descriptor block.
//
// Three layouts exist and the journal's own feature flags select between them,
// so guessing produces plausible but wrong block numbers.
func parseJournalTags(data []byte, jsb *JournalSuperblock, descriptorBlock uint64) []JournalBlockTag {
	tagSize := 8
	if jsb.HasCSumV3() {
		tagSize = 16
	} else if jsb.Has64BitTags() {
		tagSize = 12
	}

	var (
		tags      []JournalBlockTag
		off       = journalHeaderSize
		dataBlock = descriptorBlock
	)

	for off+tagSize <= len(data) {
		var (
			blockLo uint32
			blockHi uint32
			flags   uint32
		)

		if jsb.HasCSumV3() {
			blockLo = binary.BigEndian.Uint32(data[off : off+4])
			flags = binary.BigEndian.Uint32(data[off+4 : off+8])
			blockHi = binary.BigEndian.Uint32(data[off+8 : off+12])
		} else {
			blockLo = binary.BigEndian.Uint32(data[off : off+4])
			flags = uint32(binary.BigEndian.Uint16(data[off+6 : off+8]))
			if jsb.Has64BitTags() {
				blockHi = binary.BigEndian.Uint32(data[off+8 : off+12])
			}
		}

		dataBlock++
		tags = append(tags, JournalBlockTag{
			FSBlock:      uint64(blockHi)<<32 | uint64(blockLo),
			JournalBlock: dataBlock,
			Escaped:      flags&JournalTagFlagEscaped != 0,
			SameUUID:     flags&JournalTagFlagSameBuf != 0,
			LastTag:      flags&JournalTagFlagLastTag != 0,
		})

		off += tagSize
		// A tag without SAME_UUID is followed by the 16-byte UUID it refers to.
		if flags&JournalTagFlagSameBuf == 0 {
			off += journalUUIDSize
		}
		if flags&JournalTagFlagLastTag != 0 {
			break
		}
	}
	return tags
}

// JournalBlockCopies returns every journalled copy of a filesystem block,
// newest first.
//
// Each copy is the block's content at the moment the transaction was written,
// so this exposes prior states of metadata that the live filesystem has since
// overwritten.
func (fs *FS) JournalBlockCopies(fsBlock uint64) ([][]byte, error) {
	return fs.JournalBlockCopiesContext(context.Background(), fsBlock)
}

// JournalBlockCopiesContext is JournalBlockCopies with cancellation.
//
// It walks the whole journal to find the copies, so it inherits the cost of
// ListJournalTransactions - and pays it again on the next call. Asking about
// more than a couple of blocks should go through BuildJournalIndex, which pays
// it once.
func (fs *FS) JournalBlockCopiesContext(ctx context.Context, fsBlock uint64) ([][]byte, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	ix, err := fs.BuildJournalIndexContext(ctx)
	if err != nil {
		return nil, err
	}
	return ix.BlockCopies(fsBlock)
}

// JournalInodeVersions returns prior on-disk states of an inode, recovered from
// journalled copies of the inode table block that holds it.
//
// This is what can resurrect a deleted file's extent tree: unlink zeroes the
// tree in the live inode, but a journalled copy of the same block from before
// the unlink still carries it.
//
// Each call walks the whole journal. Recovering more than a handful of inodes
// should go through BuildJournalIndex, which walks it once.
func (fs *FS) JournalInodeVersions(inodeNum uint32) ([]Inode, error) {
	return fs.JournalInodeVersionsContext(context.Background(), inodeNum)
}

// JournalInodeVersionsContext is JournalInodeVersions with cancellation.
//
// Recovering the versions of many inodes this way costs one whole-journal walk
// per inode, because each call re-reads the journal to find the copies of the
// one inode-table block it needs. Use BuildJournalIndex and
// JournalIndex.InodeVersions for more than a handful.
func (fs *FS) JournalInodeVersionsContext(ctx context.Context, inodeNum uint32) ([]Inode, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	// Validated before the journal walk rather than after it, so an inode number
	// that was never valid costs nothing to reject.
	if _, _, err := fs.inodeTableLocation(inodeNum); err != nil {
		return nil, err
	}
	ix, err := fs.BuildJournalIndexContext(ctx)
	if err != nil {
		return nil, err
	}
	return ix.InodeVersions(inodeNum)
}

// DescribeJournalStatus returns human-readable journal status.
func (fs *FS) DescribeJournalStatus() (string, error) {
	journalInode := fs.GetJournalInode()
	if journalInode == 0 {
		return "Journal disabled or external", nil
	}

	inode, err := fs.ReadInode(journalInode)
	if err != nil {
		return "", fmt.Errorf("failed to read journal inode: %w", err)
	}

	journalSize := inode.Size
	return fmt.Sprintf("Journal: internal, inode %d, size %d bytes (%d blocks)",
		journalInode, journalSize, journalSize/uint64(fs.sb.BlockSize)), nil
}

// GetJournalFeatures returns journal-related features.
func (fs *FS) GetJournalFeatures() map[string]bool {
	features := map[string]bool{
		"has_journal":    fs.sb.FeatureCompat&featureCompatHasJournal != 0,
		"needs_recovery": fs.sb.FeatureIncompat&featureIncompatRecover != 0,
	}

	// The journal's own feature flags live in its superblock, not the
	// filesystem's; reading them from the filesystem reports the wrong thing.
	if jsb, err := fs.JournalSuperblock(); err == nil {
		features["journal_async_commit"] = jsb.IncompatFeatures&journalIncompatAsyncCommit != 0
		features["journal_revoke"] = jsb.IncompatFeatures&journalIncompatRevoke != 0
		features["journal_checksum_v2"] = jsb.IncompatFeatures&journalIncompatCSumV2 != 0
		features["journal_checksum_v3"] = jsb.IncompatFeatures&journalIncompatCSumV3 != 0
		features["journal_64bit"] = jsb.Has64BitTags()
		features["journal_fast_commit"] = jsb.HasFastCommit()
	}
	return features
}
