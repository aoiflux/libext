package libext_test

// External API surface lock.
//
// This file is in package libext_test rather than libext, so it can only reach
// exported identifiers — the same view an integrator has. Its job is to fail to
// compile when the public surface changes incompatibly: a removed function, a
// renamed field, an altered signature. Behavioural assertions belong in the
// in-package tests; what is pinned here is shape.
//
// Adding to the surface is fine and will not break this file. Removing from it,
// or changing a signature, will.

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/aoiflux/libext"
)

// TestPublicSurfaceCompiles references every exported identifier the library is
// expected to keep. It is deliberately not run against a real image: the point
// is that this compiles, not that any particular call succeeds.
func TestPublicSurfaceCompiles(t *testing.T) {
	// --- constructors --------------------------------------------------------
	var (
		_ func(io.ReaderAt) (*libext.FS, error)                 = libext.Open
		_ func(io.ReaderAt, libext.Options) (*libext.FS, error) = libext.OpenWithOptions
		_ func(io.ReaderAt, uint64) (*libext.FS, error)         = libext.OpenWithSize
		_ func(string) (*libext.FS, error)                      = libext.OpenFile
		_ func(string, libext.Options) (*libext.FS, error)      = libext.OpenFileWithOptions
	)

	// --- options and diagnostics --------------------------------------------
	opts := libext.Options{
		ImageSize:       0,
		BaseOffset:      0,
		Permissive:      true,
		VerifyChecksums: false,
		MaxExtents:      0,
	}

	var w libext.Warning
	_ = w.Code
	_ = w.Feature
	_ = w.Detail
	_ = w.String()

	for _, code := range []libext.WarningCode{
		libext.WarnUnknownFeature,
		libext.WarnUnsupportedFeature,
		libext.WarnChecksumMismatch,
		libext.WarnTruncatedImage,
		libext.WarnDegradedRead,
	} {
		_ = code.String()
	}

	// An empty reader is refused; the call shapes are what matter here.
	fs, err := libext.OpenWithOptions(bytes.NewReader(make([]byte, 4096)), opts)
	if err == nil && fs != nil {
		defer fs.Close()
	}
	if fs == nil {
		fs = &libext.FS{} // exercise method shapes without a live image
	}

	// --- filesystem-level ----------------------------------------------------
	var (
		_ func() error                     = fs.Close
		_ func() bool                      = fs.IsClosed
		_ func() libext.FSKind             = fs.Kind
		_ func() libext.Superblock         = fs.Superblock
		_ func() libext.Options            = fs.Options
		_ func() []libext.Warning          = fs.Warnings
		_ func() []libext.GroupDescriptor  = fs.GroupDescriptors
		_ func() error                     = fs.CheckRequiredFeatures
		_ func() []string                  = fs.CheckOptionalFeatures
		_ func() string                    = fs.DescribeFeatures
		_ func(string) uint32              = fs.UnknownFeatureBits
		_ func() []libext.Feature          = fs.BlockingFeatures
	)

	// --- navigation ----------------------------------------------------------
	var (
		_ func(uint32) (*libext.File, error)                         = fs.Open
		_ func() (*libext.File, error)                               = fs.GetRootDirectory
		_ func(string) (*libext.File, error)                         = fs.OpenPath
		_ func(uint32) ([]libext.DirEntry, error)                    = fs.ListDir
		_ func(uint32, libext.DirOptions) ([]libext.DirEntry, error) = fs.ListDirEx
		_ func(uint32) ([]libext.DirEntry, error)                    = fs.EnhancedListDir
		_ func(string) (libext.DirEntry, error)                      = fs.LookupPath
		_ func(uint32, func(string, libext.DirEntry) error) error    = fs.WalkDir
	)

	// --- inodes and content --------------------------------------------------
	var (
		_ func(uint32) (libext.Inode, error) = fs.ReadInode
		_ func(uint32) ([]byte, error)       = fs.ReadFile
	)

	// --- block maps ----------------------------------------------------------
	var (
		_ func(uint32) ([]libext.Extent, error)                                = fs.Extents
		_ func(uint32, libext.ExtentOptions) ([]libext.Extent, error)          = fs.ExtentsWithOptions
		_ func(libext.Inode, libext.ExtentOptions) ([]libext.Extent, error)    = fs.InodeExtents
		_ func(uint32) ([]libext.ByteRange, error)                             = fs.DataRuns
		_ func(uint32) ([]uint64, error)                                       = fs.MetadataBlocks
	)

	// --- allocation ----------------------------------------------------------
	var (
		_ func(uint32) ([]byte, error) = fs.InodeBitmap
		_ func(uint32) ([]byte, error) = fs.BlockBitmap
		_ func(uint32) (bool, error)   = fs.InodeAllocated
		_ func(uint64) (bool, error)   = fs.BlockAllocated
	)

	// --- deleted data --------------------------------------------------------
	var (
		_ func() ([]libext.DeletedEntry, error)                                    = fs.DeletedEntries
		_ func(libext.DeletedScanOptions) ([]libext.DeletedEntry, error)           = fs.DeletedEntriesWithOptions
		_ func(libext.DeletedScanOptions, func(libext.DeletedEntry) error) error   = fs.ScanDeleted
		_ func() ([]uint32, error)                                                 = fs.OrphanInodes
		_ func() uint32                                                            = fs.OrphanFileInode
		_ func(uint32) ([]libext.DirSlackEntry, error)                             = fs.ScanDirSlack
		_ func(int) []uint32                                                       = fs.ScanForOrphanedInodes
	)

	// --- inline data and attributes -----------------------------------------
	var (
		_ func(libext.Inode) bool                        = fs.HasInlineData
		_ func(uint32) ([]byte, bool, error)             = fs.InlineData
		_ func(uint32) (libext.XAttrList, error)         = fs.GetXAttrs
		_ func(*libext.Inode) (libext.XAttrList, error)  = fs.GetInlineXAttrs
	)

	// --- journal -------------------------------------------------------------
	var (
		_ func() (*libext.JournalSuperblock, error)     = fs.JournalSuperblock
		_ func() ([]libext.JournalTransaction, error)   = fs.ListJournalTransactions
		_ func() uint32                                 = fs.GetJournalInode
		_ func() (uint32, bool, error)                  = fs.GetJournalLocation
		_ func() (string, error)                        = fs.DescribeJournalStatus
		_ func() map[string]bool                        = fs.GetJournalFeatures
		_ func(uint64) ([][]byte, error)                = fs.JournalBlockCopies
		_ func(uint32) ([]libext.Inode, error)          = fs.JournalInodeVersions
		_ func() ([]libext.FastCommitOp, error)         = fs.FastCommitOps
		_ func([]byte) (*libext.JournalSuperblock, error) = libext.ParseJournalSuperblock
	)

	// --- reporting -----------------------------------------------------------
	var (
		_ func(string) (libext.EXTReport, error)                        = fs.Report
		_ func(string) (libext.EXTReport, error)                        = fs.ReportDeep
		_ func(string, libext.ReportOptions) (libext.EXTReport, error)  = fs.ReportWithOptions
		_ func(string, io.Writer) error                                 = fs.WriteReport
		_ func(string, libext.ReportOptions, io.Writer) error           = fs.WriteReportWithOptions
	)

	// --- integrity -----------------------------------------------------------
	var (
		_ func() []libext.CorruptionReport                                     = fs.ValidateSuperblockIntegrity
		_ func(*libext.Inode) []libext.CorruptionReport                        = fs.ValidateInodeIntegrity
		_ func(uint32, *libext.GroupDescriptor) []libext.CorruptionReport      = fs.ValidateGroupDescriptorIntegrity
		_ func(uint32, []byte) error                                           = fs.VerifyBlockBitmapChecksum
		_ func(uint32, []byte) error                                           = fs.VerifyInodeBitmapChecksum
		_ func(uint32, int) (bool, error)                                      = fs.DetectCircularReferences
		_ func([]libext.CorruptionReport) string                               = libext.ReportCorruptions
	)

	_ = opts
}

// TestPublicTypeFields pins the field sets integrators read. A removed or
// renamed field fails to compile here.
func TestPublicTypeFields(t *testing.T) {
	var e libext.Extent
	_, _, _, _ = e.LogicalBlock, e.PhysicalBlock, e.Blocks, e.Flags
	_, _, _, _ = e.Sparse(), e.Unwritten(), e.Inline(), e.End()
	_ = e.Flags.String()

	var br libext.ByteRange
	_, _, _, _, _ = br.FileOffset, br.DiskOffset, br.Length, br.Sparse, br.Unwritten

	var eo libext.ExtentOptions
	_, _, _ = eo.OmitSparse, eo.NoCoalesce, eo.MaxExtents

	var in libext.Inode
	_, _, _, _, _ = in.Number, in.Mode, in.UID, in.GID, in.Size
	_, _, _, _ = in.Atime, in.Mtime, in.Ctime, in.Dtime
	_, _, _ = in.Crtime, in.HasCrtime, in.DtimeRaw
	_, _, _, _ = in.LinksCount, in.Blocks512, in.Flags, in.Generation
	_, _, _, _ = in.FileACL, in.ExtraISize, in.ProjectID, in.BlockRaw
	_, _, _ = in.HasExtents, in.HasInline, in.HugeFile
	_, _, _ = in.IsDirectory, in.IsRegular, in.IsSymlink
	var ts libext.Timestamps = in.Timestamps()
	_, _, _, _, _ = ts.Atime, ts.Mtime, ts.Ctime, ts.Crtime, ts.Dtime
	_ = in.Deleted()

	var de libext.DirEntry
	_, _, _, _ = de.Name, de.Inode, de.RecLen, de.NameLen
	_, _, _ = de.FileType, de.IsDirectory, de.Size
	_, _, _, _, _ = de.Times, de.Mode, de.UID, de.GID, de.Deleted

	var do libext.DirOptions
	_, _ = do.WithInodeMetadata, do.IncludeDotEntries

	var gd libext.GroupDescriptor
	_, _, _, _ = gd.Group, gd.BlockBitmapBlock, gd.InodeBitmapBlock, gd.InodeTableBlock
	_, _, _, _ = gd.FreeBlocksCount, gd.FreeInodesCount, gd.UsedDirsCount, gd.Flags
	_, _ = gd.ItableUnused, gd.Checksum
	_, _ = gd.BlockBitmapChecksum, gd.InodeBitmapChecksum
	_, _ = gd.InodeUninit(), gd.BlockUninit()

	var sb libext.Superblock
	_, _, _, _ = sb.InodesCount, sb.BlocksCount, sb.BlockSize, sb.InodeSize
	_, _, _ = sb.FeatureCompat, sb.FeatureIncompat, sb.FeatureROCompat
	_, _, _ = sb.ChecksumSeed, sb.OrphanFileInode, sb.LastOrphan
	_, _, _ = sb.UUID, sb.VolumeName, sb.LastMounted

	var d libext.DeletedEntry
	_, _, _, _ = d.Inode, d.Name, d.ParentInode, d.Path
	_, _, _, _ = d.Source, d.Mode, d.Size, d.Times
	_, _, _, _ = d.UID, d.GID, d.Allocated, d.Extents
	_ = d.Recoverable
	_ = d.Source.String()
	_ = d.Recoverable.String()

	var dso libext.DeletedScanOptions
	_, _, _ = dso.SkipInodeTable, dso.SkipOrphanList, dso.SkipDirSlack
	_, _ = dso.IncludeUninit, dso.MaxResults

	var ds libext.DirSlackEntry
	_, _, _, _ = ds.ParentInode, ds.Name, ds.Inode, ds.FileType
	_, _ = ds.Offset, ds.ShadowsLive

	var jt libext.JournalTransaction
	_, _, _, _ = jt.Sequence, jt.StartBlock, jt.Type, jt.Timestamp
	_, _, _ = jt.IsCommitted, jt.BlockCount, jt.Tags

	var jbt libext.JournalBlockTag
	_, _, _, _, _ = jbt.FSBlock, jbt.JournalBlock, jbt.Escaped, jbt.SameUUID, jbt.LastTag

	var jsb libext.JournalSuperblock
	_, _, _, _ = jsb.BlockSize, jsb.MaxLen, jsb.FirstBlock, jsb.Sequence
	_, _, _ = jsb.IncompatFeatures, jsb.ChecksumType, jsb.FastCommitBlocks
	_, _, _ = jsb.Has64BitTags(), jsb.HasCSumV3(), jsb.HasFastCommit()

	var fc libext.FastCommitOp
	_, _, _, _, _ = fc.Tag, fc.Inode, fc.Parent, fc.Name, fc.Block
	_ = fc.Tag.String()

	var f libext.Feature
	_, _, _, _, _, _ = f.Name, f.Description, f.FlagType, f.FlagValue, f.Status, f.Blocking

	var xa libext.XAttr
	_, _ = xa.Name, xa.Value
	var xl libext.XAttrList
	_, _, _ = xl.Attrs, xl.Len(), xl.ListXAttrNames()
	_ = xl.GetXAttrValue("user.x")

	var rep libext.EXTReport
	_, _, _, _ = rep.Name, rep.StartOffset, rep.EndOffset, rep.Files
	_ = rep.Summary()
	_ = rep.DeletedFiles()
	_ = rep.FragmentedFiles()
	_ = rep.FilesByType("file")

	var frag libext.FileFragment
	_, _, _ = frag.StartOffset, frag.EndOffset, frag.Unwritten

	var ro libext.ReportOptions
	_, _ = ro.DeepScan, ro.IncludeUnwritten

	var _ time.Time = in.Mtime
}

// TestPublicConstants pins the exported constant sets.
func TestPublicConstants(t *testing.T) {
	_ = libext.RootInode
	_ = libext.SuperblockOffset
	_ = libext.Author

	for _, k := range []libext.FSKind{
		libext.FSKindUnknown, libext.FSKindExt1,
		libext.FSKindExt2, libext.FSKindExt3, libext.FSKindExt4,
	} {
		_ = k
	}

	for _, f := range []libext.ExtentFlags{
		libext.ExtentSparse, libext.ExtentUnwritten, libext.ExtentInline,
	} {
		_ = f
	}

	for _, s := range []libext.DeletedSource{
		libext.DeletedSourceInodeTable, libext.DeletedSourceOrphanList,
		libext.DeletedSourceOrphanFile, libext.DeletedSourceDirSlack,
		libext.DeletedSourceJournal, libext.DeletedSourceFastCommit,
	} {
		_ = s
	}

	for _, c := range []libext.RecoveryConfidence{
		libext.RecoveryNone, libext.RecoveryPartial, libext.RecoveryLikely,
	} {
		_ = c
	}

	for _, tag := range []libext.FastCommitTag{
		libext.FastCommitTagAddRange, libext.FastCommitTagDelRange,
		libext.FastCommitTagCreat, libext.FastCommitTagLink,
		libext.FastCommitTagUnlink, libext.FastCommitTagInode,
		libext.FastCommitTagPad, libext.FastCommitTagTail, libext.FastCommitTagHead,
	} {
		_ = tag
	}

	for _, g := range []uint16{
		libext.GroupInodeUninit, libext.GroupBlockUninit, libext.GroupInodeZeroed,
	} {
		_ = g
	}

	for _, s := range []libext.FeatureStatus{
		libext.FeatureStatusSupported, libext.FeatureStatusPartial,
		libext.FeatureStatusUnsupported,
	} {
		_ = s
	}

	for _, s := range []libext.CorruptionSeverity{
		libext.SeverityInfo, libext.SeverityWarning, libext.SeverityCritical,
	} {
		_ = s
	}

	// Sentinel errors callers match on.
	for _, err := range []error{
		libext.ErrInvalidSuperblock, libext.ErrChecksumMismatch,
		libext.ErrUnsupportedLayout, libext.ErrInvalidInode,
		libext.ErrNotDirectory, libext.ErrNotRegularFile,
		libext.ErrNotSymlink, libext.ErrPathNotFound,
	} {
		_ = err
	}

	_ = libext.AllFeatures
}

// TestFileSurface pins the *File method set.
func TestFileSurface(t *testing.T) {
	var f *libext.File
	var (
		_ func() string                                        = f.Name
		_ func() uint32                                        = f.InodeNumber
		_ func() bool                                          = f.IsDirectory
		_ func() int64                                         = f.Size
		_ func([]byte) (int, error)                            = f.Read
		_ func([]byte, int64) (int, error)                     = f.ReadAt
		_ func() ([]byte, error)                               = f.ReadAll
		_ func() (string, error)                               = f.ReadLink
		_ func() ([]libext.DirEntry, error)                    = f.ReadDir
		_ func(libext.DirOptions) ([]libext.DirEntry, error)   = f.ReadDirEx
		_ func() ([]libext.Extent, error)                      = f.Extents
		_ func() ([]libext.ByteRange, error)                   = f.DataRuns
		_ func() libext.Inode                                  = f.Inode
		_ func() libext.Timestamps                             = f.Timestamps
	)
}
