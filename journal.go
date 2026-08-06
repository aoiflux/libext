package libext

import (
	"encoding/binary"
	"fmt"
	"time"
)

// JournalSuperblock represents the journal superblock (typically at block 0 of journal).
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
}

// JournalTransaction represents a single transaction in the journal.
type JournalTransaction struct {
	Sequence   uint32
	StartBlock uint32
	Type       string

	// Timestamp is the transaction commit time. It is currently always zero:
	// the value lives in the commit block header, which this version does not
	// parse. Check IsZero before using it.
	Timestamp   time.Time
	IsCommitted bool
	BlockCount  uint32
}

// Journal block types
const (
	JournalBlockTypeSuperblock = 0
	JournalBlockTypeDescriptor = 1
	JournalBlockTypeCommit     = 2
	JournalBlockTypeOrphan     = 3
	JournalBlockTypeEscaped    = 4
)

// Journal tag flags
const (
	JournalTagFlagEscaped = 1
	JournalTagFlagSameBuf = 2
	JournalTagFlagDeleted = 4
	JournalTagFlagLastTag = 8
)

// Journal magic number
const journalMagic = 0xC0B1A001

// GetJournalLocation returns the location of the journal device.
func (fs *FS) GetJournalLocation() (journalBlock uint32, isExternal bool, err error) {
	// Check if journal feature is enabled
	if (fs.sb.FeatureCompat & 0x0004) == 0 {
		return 0, false, fmt.Errorf("journal feature not enabled")
	}

	// For internal journal
	if fs.sb.JournalInode != 0 {
		return 0, false, nil
	}

	return 0, true, nil
}

// GetJournalInode returns the inode number of the journal.
func (fs *FS) GetJournalInode() uint32 {
	if (fs.sb.FeatureCompat & 0x0004) == 0 {
		return 0
	}
	return fs.sb.JournalInode
}

// ListJournalTransactions lists transactions in the journal (recovery-oriented).
func (fs *FS) ListJournalTransactions() ([]JournalTransaction, error) {
	var transactions []JournalTransaction

	journalInode := fs.GetJournalInode()
	if journalInode == 0 {
		return nil, fmt.Errorf("no journal inode found")
	}

	inode, err := fs.ReadInode(journalInode)
	if err != nil {
		return nil, fmt.Errorf("failed to read journal inode: %w", err)
	}

	journalSize := inode.Size
	if journalSize == 0 {
		return nil, fmt.Errorf("journal size is zero")
	}

	journalData, err := fs.ReadFile(journalInode)
	if err != nil {
		return nil, fmt.Errorf("failed to read journal file: %w", err)
	}

	blockSize := uint64(fs.sb.BlockSize)
	var offset uint64
	dataLen := uint64(len(journalData))

	for offset < journalSize && offset < dataLen {
		if offset+12 > dataLen {
			break
		}

		// Parse block header (journal uses big-endian)
		magic := binary.BigEndian.Uint32(journalData[offset : offset+4])
		blockType := binary.BigEndian.Uint32(journalData[offset+4 : offset+8])
		sequence := binary.BigEndian.Uint32(journalData[offset+8 : offset+12])

		if magic == journalMagic {
			switch blockType {
			case JournalBlockTypeDescriptor:
				// Timestamp is deliberately left zero. A transaction's real time
				// is h_commit_sec in the matching commit block; stamping the
				// wall clock here fabricates a forensic artifact that looks
				// authoritative. Populated once commit blocks are parsed.
				transactions = append(transactions, JournalTransaction{
					Sequence:   sequence,
					StartBlock: uint32(offset / blockSize),
					Type:       "descriptor",
				})

			case JournalBlockTypeCommit:
				if len(transactions) > 0 {
					transactions[len(transactions)-1].IsCommitted = true
				}
			}
		}

		offset += blockSize
	}

	return transactions, nil
}

// ParseJournalSuperblock parses a journal superblock from raw data.
func ParseJournalSuperblock(data []byte) (*JournalSuperblock, error) {
	if len(data) < 256 {
		return nil, fmt.Errorf("journal superblock too small")
	}

	jsb := &JournalSuperblock{
		BlockSize:        binary.BigEndian.Uint32(data[4:8]),
		MaxLen:           binary.BigEndian.Uint32(data[8:12]),
		FirstBlock:       binary.BigEndian.Uint32(data[12:16]),
		Sequence:         binary.BigEndian.Uint32(data[16:20]),
		Start:            binary.BigEndian.Uint32(data[20:24]),
		ErrCode:          binary.BigEndian.Uint32(data[24:28]),
		CompatFeatures:   binary.BigEndian.Uint32(data[28:32]),
		IncompatFeatures: binary.BigEndian.Uint32(data[32:36]),
		RoCompatFeatures: binary.BigEndian.Uint32(data[36:40]),
		NumUsers:         binary.BigEndian.Uint32(data[96:100]),
		DynSuperblock:    binary.BigEndian.Uint32(data[100:104]),
	}

	copy(jsb.UUID[:], data[40:56])
	return jsb, nil
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
	journalSizeBlocks := journalSize / uint64(fs.sb.BlockSize)

	status := fmt.Sprintf(
		"Journal: internal, inode %d, size %d bytes (%d blocks)",
		journalInode,
		journalSize,
		journalSizeBlocks,
	)

	return status, nil
}

// GetJournalFeatures returns journal-related features.
func (fs *FS) GetJournalFeatures() map[string]bool {
	features := make(map[string]bool)

	hasJournal := (fs.sb.FeatureCompat & 0x0004) != 0
	features["has_journal"] = hasJournal

	needsRecovery := (fs.sb.FeatureCompat & 0x0001) != 0
	features["needs_recovery"] = needsRecovery

	hasAsyncCommit := (fs.sb.FeatureIncompat & 0x0200) != 0
	features["journal_async_commit"] = hasAsyncCommit

	return features
}
