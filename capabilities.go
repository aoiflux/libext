package libext

// Capabilities reports what an ext volume can record, as distinct from what it
// happens to record.
//
// It exists so a consumer can tell "this filesystem does not keep that" from
// "that was absent here". Without it, a pass comparing two readings of a volume
// has no way to know that an ext2 volume keeps no birth time, and a report
// merging several filesystems would show every ext2 file as having lost a
// creation date it never had.
//
// Unlike the other filesystems this family covers, most of these fields are NOT
// format constants. ext2, ext3 and ext4 are one on-disk format governed by
// feature flags, so the answers are read from this volume's superblock and two
// volumes opened by the same library will disagree. Each field's doc comment
// says which kind it is: a fact about ext, or state read from this volume.
//
// Nothing here describes the contents of the volume. A true AccessTimes says
// the format has the field, not that any particular inode's atime is
// meaningful - it may have been mounted noatime for its whole life.
type Capabilities struct {
	// UnicodeNames reports that names are stored in a recorded character
	// encoding. ext does not: a name is an opaque byte string with no encoding
	// declared anywhere on the volume. Names are UTF-8 by convention on Linux,
	// but nothing on disk says so and nothing enforces it, so a consumer must
	// not treat one as validated Unicode. A fact about ext.
	UnicodeNames bool `json:"unicode_names"`
	// CaseSensitive reports whether name comparison distinguishes case. ext is
	// case-sensitive unless the volume carries the CASEFOLD feature, which lets
	// individual directories opt into case-insensitive lookup; this field goes
	// false for such a volume because some of its directories may fold, not
	// because all of them do. Volume state.
	CaseSensitive bool `json:"case_sensitive"`

	// CreationTimes reports whether inodes carry a birth time. ext4 stores
	// i_crtime in the extra inode area, so it exists only when the volume's
	// inodes are larger than the 128-byte base - which is why this is volume
	// state rather than a format constant. Inode.HasCrtime answers the same
	// question for one inode and is the authority: an inode large enough to
	// hold the field may still not have had it written.
	CreationTimes bool `json:"creation_times"`
	// ModificationTimes, AccessTimes and MetadataChangeTimes report the classic
	// mtime, atime and ctime. Every ext inode has all three. Facts about ext.
	ModificationTimes   bool `json:"modification_times"`
	AccessTimes         bool `json:"access_times"`
	MetadataChangeTimes bool `json:"metadata_change_times"`
	// SubSecondTimestamps reports that timestamps carry a fraction of a second.
	// The nanosecond words live in the same extra inode area as i_crtime, so
	// this follows the inode size exactly as CreationTimes does, and it is what
	// also lifts the 2038 limit on the three classic times. Volume state.
	SubSecondTimestamps bool `json:"sub_second_timestamps"`
	// TimezoneOffsets reports that a timestamp is stored beside the offset it
	// was recorded at. ext stores seconds since the Unix epoch and nothing
	// else, so every time this library reports is UTC and no local reading can
	// be recovered from the volume. A fact about ext.
	TimezoneOffsets bool `json:"timezone_offsets"`

	// POSIXPermissions reports that inodes carry owner, group and mode. Every
	// ext inode does. A fact about ext.
	POSIXPermissions bool `json:"posix_permissions"`
	// HardLinks reports that one inode can be named by several directory
	// entries, which is why a file's identity here is its inode number and not
	// its path. A fact about ext.
	HardLinks bool `json:"hard_links"`
	// SymbolicLinks reports that a target can be stored in place of file data,
	// either inline in the inode's block area for a short target or in
	// allocated blocks for a long one. A fact about ext.
	SymbolicLinks bool `json:"symbolic_links"`
	// ExtendedAttributes reports whether this volume uses extended attributes.
	// The format has supported them since ext2, but the EXT_ATTR compat flag
	// records whether any have actually been written, so this is volume state.
	// A false value does not stop the xattr calls from working; it says there
	// is nothing for them to find.
	ExtendedAttributes bool `json:"extended_attributes"`
	// SparseFiles reports that a file can have holes - logical blocks with no
	// allocation, which read as zeros. Both the block-mapped and the
	// extent-mapped layout can express them, and ByteRange.Sparse marks them.
	// A fact about ext.
	SparseFiles bool `json:"sparse_files"`
	// Compression reports that file data can be stored compressed. No ext
	// filesystem does. The COMPRESSION incompat bit was reserved for an
	// implementation that never landed, and libext refuses to open a volume
	// claiming it rather than report offsets it cannot honour - so this is
	// false on every volume this library can open. A fact about ext.
	Compression bool `json:"compression"`

	// StableFileIdentity reports that the volume records an identity for a file
	// which survives the slot being reused. ext does: the inode number names
	// the slot and i_generation counts how many times it has been handed out.
	// The pair is what lets a consumer diffing two readings tell a file that
	// was replaced at the same path from one that was never touched. A fact
	// about ext.
	StableFileIdentity bool `json:"stable_file_identity"`
	// IdentityReuseCounter reports that a reused identity can be recognised as
	// reused - i_generation, surfaced as Inode.Generation, DirEntry.Generation
	// and EXTFile.Generation. It is stated separately from StableFileIdentity
	// because the two are independent, and a consumer checking only for the
	// presence of an identity would otherwise not know whether it carries a
	// reuse count. Generation 0 is a legitimate value, not a marker for
	// "absent". A fact about ext.
	IdentityReuseCounter bool `json:"identity_reuse_counter"`

	// Journaled reports whether this volume has a journal libext can read. It
	// is true only when the HAS_JOURNAL compat flag is set AND the journal
	// lives on this volume in its own inode. A volume using an external journal
	// device keeps its journal somewhere this reader cannot reach and so
	// reports false: the journal exists, but not here.
	//
	// This is the most consequential field here for change detection. When it
	// is true, ListJournalTransactions reads a native log of which filesystem
	// blocks were written and JournalInodeVersions recovers prior inode states;
	// when it is false - every ext2 volume - neither is available, and the only
	// evidence of change is the inode table itself. Volume state.
	Journaled bool `json:"journaled"`

	// Extents reports whether file data is mapped by extent trees rather than
	// by the direct and indirect block lists. Both layouts are read here and
	// both produce the same ByteRange output, but only extents can record an
	// unwritten (preallocated) run, so ByteRange.Unwritten is never set on a
	// volume without this. Volume state.
	Extents bool `json:"extents"`
	// SixtyFourBit reports whether block numbers are 64-bit, which is what lets
	// a volume exceed 16 TiB and what widens the group descriptors. Volume
	// state.
	SixtyFourBit bool `json:"sixty_four_bit"`
	// DirectoryIndex reports whether directories may be stored as hashed
	// h-trees. The linear records remain behind the index, which is why
	// directory listing and slack recovery work either way. Volume state.
	DirectoryIndex bool `json:"directory_index"`
	// InlineData reports whether a small file's contents may be stored inside
	// its inode instead of in allocated blocks. It matters to a consumer
	// intersecting byte ranges: such a file has no data blocks at all, so it
	// has no ByteRange to intersect and a change to it is visible only in the
	// inode table. Volume state.
	InlineData bool `json:"inline_data"`
	// MetadataChecksums reports whether metadata carries CRC32C checksums, so a
	// torn or tampered structure can be detected rather than parsed. Without
	// it, Options.VerifyChecksums has almost nothing to verify. Volume state.
	MetadataChecksums bool `json:"metadata_checksums"`
	// Bigalloc reports whether space is allocated in multi-block clusters
	// rather than single blocks. libext refuses such a volume at Open unless
	// Options.Permissive is set, because it maps files in blocks and would
	// otherwise report confidently wrong offsets; on a permissive open this
	// field is what says so. Volume state.
	Bigalloc bool `json:"bigalloc"`
}

// Capabilities returns what this volume's format records.
//
// Every field is either fixed by ext itself or, where the field says so, read
// from the superblock this volume was opened with. Nothing here reads the
// volume, so it is cheap and cannot fail.
func (fs *FS) Capabilities() Capabilities {
	if fs == nil {
		return Capabilities{}
	}
	sb := fs.sb

	// The extra inode area holds i_crtime and the nanosecond words. Any inode
	// larger than the 128-byte base can carry them; a 128-byte inode cannot,
	// and that single difference is most of what separates an ext2 volume from
	// an ext4 one in this struct.
	extraInodeArea := sb.InodeSize > 128

	return Capabilities{
		UnicodeNames:  false,
		CaseSensitive: sb.FeatureIncompat&featureIncompatCasefold == 0,

		CreationTimes: extraInodeArea,
		// The false values below are written out rather than left to the zero
		// value, because each one is an answer this type exists to give.
		ModificationTimes:   true,
		AccessTimes:         true,
		MetadataChangeTimes: true,
		SubSecondTimestamps: extraInodeArea,
		TimezoneOffsets:     false,

		POSIXPermissions:   true,
		HardLinks:          true,
		SymbolicLinks:      true,
		ExtendedAttributes: sb.FeatureCompat&featureCompatExtAttr != 0,
		SparseFiles:        true,
		Compression:        false,

		StableFileIdentity:   true,
		IdentityReuseCounter: true,

		// An external journal leaves s_journal_inum zero, so requiring the
		// inode answers both halves of the question in one test: the volume
		// claims a journal, and the journal is on this volume.
		Journaled: sb.FeatureCompat&featureCompatHasJournal != 0 && sb.JournalInode != 0,

		Extents:           sb.FeatureIncompat&featureIncompatExtents != 0,
		SixtyFourBit:      sb.FeatureIncompat&featureIncompat64Bit != 0,
		DirectoryIndex:    sb.FeatureCompat&featureCompatDirIndex != 0,
		InlineData:        sb.FeatureIncompat&featureIncompatInlineData != 0,
		MetadataChecksums: sb.FeatureROCompat&featureRoCompatMetadataCS != 0,
		Bigalloc:          sb.FeatureROCompat&featureRoCompatBigalloc != 0,
	}
}
