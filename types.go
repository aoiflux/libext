package libext

import "time"

const (
	superblockOffset = 1024
	superblockSize   = 1024
	extMagic         = 0xEF53

	// maxBlockGroups bounds the group count derived from superblock geometry.
	// 2^24 groups is roughly 2 PB at a 4 KiB block size with 32768 blocks per
	// group; anything larger indicates a corrupt or crafted superblock.
	maxBlockGroups = 1 << 24
)

type FSKind string

const (
	FSKindUnknown FSKind = "unknown"
	FSKindExt1    FSKind = "ext1"
	FSKindExt2    FSKind = "ext2"
	FSKindExt3    FSKind = "ext3"
	FSKindExt4    FSKind = "ext4"
)

const (
	inodeModeTypeMask = 0xF000
	inodeTypeFIFO     = 0x1000
	inodeTypeChar     = 0x2000
	inodeTypeDir      = 0x4000
	inodeTypeBlock    = 0x6000
	inodeTypeRegular  = 0x8000
	inodeTypeSymlink  = 0xA000
	inodeTypeSocket   = 0xC000
)

const (
	inodeFlagExtents    = 0x00080000
	inodeFlagInlineData = 0x10000000
)

const (
	featureCompatHasJournal = 0x0004
	featureCompatExtAttr    = 0x0008
	featureCompatDirIndex   = 0x0020
	featureCompatOrphanFile = 0x1000

	featureIncompatRecover  = 0x0004
	featureIncompatFileType = 0x0002
	featureIncompatExtents  = 0x0040
	featureIncompat64Bit    = 0x0080
	featureIncompatCSumSeed = 0x2000

	featureRoCompatSparseSuper = 0x0001
	featureRoCompatGDTChecksum = 0x0010
	featureRoCompatBigalloc    = 0x0200
	featureRoCompatMetadataCS  = 0x0400
)

// Superblock contains the subset of EXT superblock fields needed by this parser.
type Superblock struct {
	InodesCount       uint32
	BlocksCount       uint64
	ReservedBlocks    uint64
	FreeBlocks        uint64
	FreeInodes        uint32
	FirstDataBlock    uint32
	LogBlockSize      uint32
	BlockSize         uint32
	BlocksPerGroup    uint32
	InodesPerGroup    uint32
	Magic             uint16
	RevisionLevel     uint32
	InodeSize         uint16
	FirstInode        uint32
	FeatureCompat     uint32
	FeatureIncompat   uint32
	FeatureROCompat   uint32
	DescSize          uint16
	UUID              [16]byte
	VolumeName        string
	LastMounted       string
	MountTime         time.Time
	WriteTime         time.Time
	MountCount        uint16
	MaxMountCount     uint16
	State             uint16
	ErrorsBehavior    uint16
	CreatorOS         uint32
	JournalInode      uint32
	JournalDevice     uint32
	LastOrphan        uint32
	ChecksumSeed      uint32 // s_checksum_seed; meaningful only with CSUM_SEED
	OrphanFileInode   uint32 // s_orphan_file_inum; meaningful only with ORPHAN_FILE
	GroupsCount       uint32
	GroupDescSize     uint16
	GroupDescTableOff uint64
}

// Block group descriptor flags.
const (
	// GroupInodeUninit marks a group whose inode bitmap and table have never
	// been initialised. Its inode table holds whatever was on the disk before,
	// so scanning it yields phantom entries rather than deleted files.
	GroupInodeUninit uint16 = 0x1
	// GroupBlockUninit marks a group whose block bitmap is not initialised.
	GroupBlockUninit uint16 = 0x2
	// GroupInodeZeroed marks a group whose inode table has been zeroed.
	GroupInodeZeroed uint16 = 0x4
)

// GroupDescriptor represents one block group descriptor.
type GroupDescriptor struct {
	Group            uint32
	BlockBitmapBlock uint64
	InodeBitmapBlock uint64
	InodeTableBlock  uint64
	FreeBlocksCount  uint32
	FreeInodesCount  uint32
	UsedDirsCount    uint32
	Flags            uint16

	// ItableUnused counts inodes at the tail of this group's inode table that
	// have never been used. Everything from InodesPerGroup-ItableUnused onward
	// is uninitialised, so a deleted-inode scan must stop there.
	ItableUnused uint32

	// Checksum is the descriptor's own checksum; BlockBitmapChecksum and
	// InodeBitmapChecksum cover the two bitmaps this group owns.
	Checksum            uint16
	BlockBitmapChecksum uint32
	InodeBitmapChecksum uint32
}

// InodeUninit reports whether this group's inode table is uninitialised.
func (g GroupDescriptor) InodeUninit() bool { return g.Flags&GroupInodeUninit != 0 }

// BlockUninit reports whether this group's block bitmap is uninitialised.
func (g GroupDescriptor) BlockUninit() bool { return g.Flags&GroupBlockUninit != 0 }

// Inode is a normalized inode view.
type Inode struct {
	Number uint32
	Mode   uint16
	UID    uint32
	GID    uint32
	Size   uint64

	// Atime, Ctime and Mtime carry nanosecond precision and dates beyond 2038
	// when the inode is large enough to hold the extra words; check ExtraISize.
	Atime time.Time
	Ctime time.Time
	Mtime time.Time

	// Dtime is the deletion time, and is always whole seconds. It is zero for a
	// live inode, so a non-zero Dtime is itself evidence of deletion.
	//
	// One exception matters: while an inode sits on the legacy orphan list, ext4
	// reuses this field to hold the *inode number* of the next orphan rather
	// than a timestamp. DtimeRaw preserves the undecoded value for that case.
	Dtime    time.Time
	DtimeRaw uint32

	// Crtime is the creation ("birth") time. It exists only on ext4 inodes large
	// enough to store it; HasCrtime reports whether it was present.
	Crtime    time.Time
	HasCrtime bool

	LinksCount uint16

	// Blocks512 counts 512-byte sectors unless HugeFile is set, in which case it
	// counts filesystem blocks.
	Blocks512  uint64
	Flags      uint32
	Generation uint32
	FileACL    uint64
	ExtraISize uint16
	ProjectID  uint32
	BlockRaw   [60]byte

	HasExtents  bool
	HasInline   bool
	HugeFile    bool
	IsDirectory bool
	IsRegular   bool
	IsSymlink   bool
}

// Timestamps groups an inode's times. Dtime is zero unless the inode was
// deleted, and Crtime is zero unless the inode was large enough to record it.
//
// The JSON encoding omits a zero time rather than emitting one. A timestamp the
// inode never recorded must not appear in a report as 0001-01-01: absent says
// "not recorded", which is the truth, while a rendered zero looks like an
// answer. This needs omitzero rather than omitempty, which does nothing for a
// struct field.
type Timestamps struct {
	Atime  time.Time `json:"atime,omitzero"`
	Mtime  time.Time `json:"mtime,omitzero"`
	Ctime  time.Time `json:"ctime,omitzero"`
	Crtime time.Time `json:"crtime,omitzero"`
	Dtime  time.Time `json:"dtime,omitzero"`
}

// Timestamps returns the inode's full MACB set.
func (i Inode) Timestamps() Timestamps {
	return Timestamps{
		Atime:  i.Atime,
		Mtime:  i.Mtime,
		Ctime:  i.Ctime,
		Crtime: i.Crtime,
		Dtime:  i.Dtime,
	}
}

// Deleted reports whether the inode carries evidence of deletion: a deletion
// time, or no remaining links.
func (i Inode) Deleted() bool {
	return !i.Dtime.IsZero() || (i.LinksCount == 0 && i.Mode != 0)
}

// DirEntry is a parsed directory entry.
type DirEntry struct {
	Name        string
	Inode       uint32
	RecLen      uint16
	NameLen     uint8
	FileType    uint8
	IsDirectory bool
	Size        uint64

	// ParentInode is the directory this record was read from - the directory that
	// holds the name, not the parent of the inode the name points at. For an
	// ordinary entry the distinction does not arise. For the ".." record it does:
	// that record's Inode is the containing directory's parent, while its
	// ParentInode is the containing directory itself. For "." the two are equal.
	//
	// Read it as "where this name lives". Joining the containing directory's path
	// with Name therefore always produces this entry's path, "." and ".."
	// included.
	//
	// Every listing call fills it, including plain ListDir, because the containing
	// inode number is something the caller already passed in rather than anything
	// that has to be read. It is 0 only where no containing directory is known: an
	// entry a caller built itself.
	ParentInode uint32

	// The fields below are populated only by the calls that read each entry's
	// inode: ListDirEx and ReadDirEx with WithInodeMetadata, ReadDir, and WalkDir,
	// which reads the inode anyway to decide whether to descend. They are zero
	// from ListDir, which reads no inodes at all.
	Times Timestamps
	Mode  uint16
	UID   uint32
	GID   uint32

	// Generation is the inode's reuse counter. It is what distinguishes two files
	// that occupied the same inode slot at different times, and so is half of the
	// identity needed to match an entry against a report or a journalled copy
	// taken at another moment; the inode number alone is not enough, because ext
	// reuses slots.
	//
	// Zero is a legitimate value - mke2fs leaves many inodes at 0 - so it cannot
	// be read as "not populated". Use the call you made to know that.
	Generation uint32

	Deleted bool
}
