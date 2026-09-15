package libext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ReportSchemaVersion is the schema every EXTReport this package produces
// declares in its SchemaVersion field.
//
// It is incremented when a field is removed, renamed, or changes meaning -
// anything that could make a consumer written against an older version read a
// document wrongly. Adding a field does not increment it, because a consumer
// that ignores the new field still reads the document correctly.
const ReportSchemaVersion = 1

// LibraryVersion is the release of libext that produced a report, recorded on
// every EXTReport so a document can be traced back to the code that wrote it.
//
// Nothing enforces this against the repository's tags: it must be updated in
// the same commit that moves the tag, or it will claim a version that was never
// released.
const LibraryVersion = "v0.3.0"

// EXTReport captures a filesystem-level view similar to common forensic report formats.
//
// # Reading an old report
//
// SchemaVersion, LibraryVersion and Generated exist because a report is
// evidence, and evidence outlives the tool that produced it. A consumer should
// check SchemaVersion against ReportSchemaVersion and refuse a document it does
// not understand, rather than silently reading fields that may have changed
// meaning.
type EXTReport struct {
	// SchemaVersion is ReportSchemaVersion, the schema this document follows.
	SchemaVersion int `json:"schema_version"`
	// LibraryVersion is the release of libext that produced this document, and
	// Generated is when it was produced. Both are facts about the report, not
	// about the volume - in particular Generated is not a timestamp read from
	// the filesystem.
	LibraryVersion string    `json:"library_version"`
	Generated      time.Time `json:"generated"`

	Name string `json:"name"`

	// StartOffset and EndOffset bound the volume within the image, inclusive of
	// EndOffset. Both include Options.BaseOffset and are therefore
	// image-absolute, matching FileFragment and ByteRange: every offset this
	// library reports is measured from the same origin.
	StartOffset int64   `json:"start_offset"`
	EndOffset   int64   `json:"end_offset"`
	Filesystem  EXTMeta `json:"ext_meta"`

	// Capabilities records what this volume's format can hold. It travels with
	// the report so that a consumer reading the document later can tell a field
	// the filesystem never records from one that was absent here - an ext2
	// volume has no birth times at all, and without this a reader would see
	// only that every crtime is missing.
	Capabilities Capabilities `json:"capabilities"`

	Files []EXTFile `json:"files"`
}

// EXTReportSummary provides common aggregate counters for a report.
type EXTReportSummary struct {
	Total       int            `json:"total"`
	Deleted     int            `json:"deleted"`
	Fragmented  int            `json:"fragmented"`
	TypeCounts  map[string]int `json:"type_counts"`
	TotalBlocks int64          `json:"total_blocks"`
}

// EXTMeta contains top-level metadata for an EXT filesystem image.
type EXTMeta struct {
	Type      string `json:"type"`
	BlockSize int    `json:"block_size"`

	// Offset is where the volume begins in the image: Options.BaseOffset, and
	// so 0 for a reader already scoped to the volume. It is the origin every
	// other offset in this report is measured from.
	Offset int64 `json:"offset"`
}

// EXTFile describes one reachable inode-backed path in the filesystem tree.
type EXTFile struct {
	Filename string `json:"filename"`

	// InodeNumber and Generation are the entry's identity. Diffing two reports by
	// filename alone conflates a file that was replaced at the same path with one
	// that was never touched; the inode number separates those, and the
	// generation separates two files that occupied the same inode slot at
	// different times.
	//
	// Neither is omitted when zero. Generation 0 is legitimate, so omitting it
	// would make "generation zero" indistinguishable from "not reported", and a
	// report row is a thing that gets diffed, which wants a stable key set.
	InodeNumber uint32 `json:"inode_number"`
	Generation  uint32 `json:"generation"`

	// ParentInode is the directory holding this name, and 0 when it is not known
	// - which is what a deep scan reports for an inode the tree no longer
	// reaches.
	ParentInode uint32 `json:"parent_inode"`

	Type         string `json:"type"`
	IsFragmented bool   `json:"is_fragmented"`
	IsDeleted    bool   `json:"is_deleted"`
	Size         int64  `json:"size"`

	// Times is the inode's full MACB set. Times the inode did not record are
	// absent from the JSON rather than encoded as a zero date.
	Times Timestamps `json:"times"`

	Fragments []FileFragment `json:"fragments"`
}

// FileFragment represents a contiguous on-disk byte span for a file.
// Offsets are inclusive of EndOffset and include Options.BaseOffset.
type FileFragment struct {
	StartOffset int64 `json:"start_offset"`
	EndOffset   int64 `json:"end_offset"`

	// Unwritten marks a preallocated span, which reads as zeros through the
	// file interface but may still hold prior contents on disk. Only present
	// when ReportOptions.IncludeUnwritten is set.
	Unwritten bool `json:"unwritten,omitempty"`
}

// ReportOptions controls report collection behavior.
type ReportOptions struct {
	// DeepScan includes inode-table scanning to surface unlinked/deleted entries.
	DeepScan bool

	// IncludeUnwritten adds preallocated spans to each file's fragments. They
	// read as zeros but may still hold prior contents on disk. Off by default,
	// so fragments describe written data only.
	IncludeUnwritten bool
}

// Summary returns aggregate counters useful for UI and analytics workflows.
func (r EXTReport) Summary() EXTReportSummary {
	s := EXTReportSummary{
		TypeCounts: make(map[string]int),
	}
	for _, f := range r.Files {
		s.Total++
		if f.IsDeleted {
			s.Deleted++
		}
		if f.IsFragmented {
			s.Fragmented++
		}
		s.TypeCounts[f.Type]++
		s.TotalBlocks += int64(len(f.Fragments))
	}
	return s
}

// FilterFiles returns files that satisfy the provided predicate.
func (r EXTReport) FilterFiles(fn func(EXTFile) bool) []EXTFile {
	if fn == nil {
		out := make([]EXTFile, len(r.Files))
		copy(out, r.Files)
		return out
	}
	out := make([]EXTFile, 0, len(r.Files))
	for _, f := range r.Files {
		if fn(f) {
			out = append(out, f)
		}
	}
	return out
}

// FilesByType returns report entries matching a type label (for example: file, directory, symlink).
func (r EXTReport) FilesByType(t string) []EXTFile {
	return r.FilterFiles(func(f EXTFile) bool {
		return f.Type == t
	})
}

// DeletedFiles returns all entries flagged as deleted or unlinked.
func (r EXTReport) DeletedFiles() []EXTFile {
	return r.FilterFiles(func(f EXTFile) bool {
		return f.IsDeleted
	})
}

// FragmentedFiles returns all entries split into multiple physical fragments.
func (r EXTReport) FragmentedFiles() []EXTFile {
	return r.FilterFiles(func(f EXTFile) bool {
		return f.IsFragmented
	})
}

// Report builds an EXT-focused report of reachable files from the root directory.
func (fs *FS) Report(name string) (EXTReport, error) {
	return fs.ReportWithOptions(name, ReportOptions{})
}

// ReportDeep builds a report that scans the full inode table.
func (fs *FS) ReportDeep(name string) (EXTReport, error) {
	return fs.ReportWithOptions(name, ReportOptions{DeepScan: true})
}

// ReportWithOptions builds an EXT report with configurable scan depth.
func (fs *FS) ReportWithOptions(name string, opts ReportOptions) (EXTReport, error) {
	return fs.ReportWithOptionsContext(context.Background(), name, opts)
}

// ReportWithOptionsContext is ReportWithOptions with cancellation.
//
// Both scan depths are whole-image operations - the deep scan reads every inode
// in the table - so both are slow enough on a large image to want interrupting.
func (fs *FS) ReportWithOptionsContext(ctx context.Context, name string, opts ReportOptions) (EXTReport, error) {
	if ctx == nil {
		return EXTReport{}, errors.New("context is nil")
	}
	sb := fs.Superblock()
	// computeImageEndOffset measures from the start of the volume, so
	// BaseOffset is added here rather than there: the clamp it performs is the
	// only place that needs to reason about the int64 ceiling.
	imageEnd := fs.computeImageEndOffset()
	report := EXTReport{
		SchemaVersion:  ReportSchemaVersion,
		LibraryVersion: LibraryVersion,
		Generated:      time.Now().UTC(),
		Name:           name,
		StartOffset:    fs.opts.BaseOffset,
		EndOffset:      imageEnd + fs.opts.BaseOffset,
		Filesystem: EXTMeta{
			Type:      string(fs.Kind()),
			BlockSize: int(sb.BlockSize),
			Offset:    fs.opts.BaseOffset,
		},
		Capabilities: fs.Capabilities(),
		Files:        make([]EXTFile, 0, 128),
	}

	if opts.DeepScan {
		// A deep report without paths is a list of numbers, so an index that could
		// not be built is fatal here rather than degraded.
		ix, err := fs.buildPathIndex(ctx, 0)
		if err != nil {
			return EXTReport{}, err
		}
		for inodeNum := uint32(1); inodeNum <= fs.sb.InodesCount; inodeNum++ {
			// Paced as elsewhere, and tested before the counter advances so an
			// already-cancelled context is caught on the first inode.
			if (inodeNum-1)%cancellationCheckInterval == 0 {
				if err := ctx.Err(); err != nil {
					return EXTReport{}, err
				}
			}
			inode, err := fs.ReadInode(inodeNum)
			if err != nil {
				continue
			}
			if !inodeInterestingForReport(inode) {
				continue
			}

			// A deep scan walks unallocated inode table entries, which hold
			// whatever was there before. One unreadable block map must not
			// discard the report; the entry is kept with no fragments.
			fragments, err := fs.inodeFragments(inode, opts.IncludeUnwritten)
			if err != nil {
				fs.warn(WarnDegradedRead, "", fmt.Sprintf(
					"inode %d block map is unreadable (%v); reported without fragments", inodeNum, err))
				fragments = nil
			}

			name := ix.paths[inodeNum]
			if name == "" {
				name = fmt.Sprintf("inode:%d", inodeNum)
			}
			report.Files = append(report.Files, EXTFile{
				Filename:     name,
				InodeNumber:  inodeNum,
				Generation:   inode.Generation,
				ParentInode:  ix.parents[inodeNum],
				Type:         inodeTypeName(inode.Mode),
				IsFragmented: len(fragments) > 1,
				IsDeleted:    inodeDeleted(inode),
				Size:         int64(inode.Size),
				Times:        inode.Timestamps(),
				Fragments:    fragments,
			})
		}
		return report, nil
	}

	// WalkDirWithInodeContext rather than WalkDirContext: the walk has already
	// read each child's inode to decide whether to descend, and inodeFragments
	// needs the whole inode rather than the summary DirEntry carries. Taking the
	// walk's copy halves this report's inode reads.
	err := fs.WalkDirWithInodeContext(ctx, RootInode, func(p string, entry DirEntry, inode Inode) error {
		if inode.Number == 0 {
			// The walk keeps going past an unreadable inode; a report does not,
			// because a row with no inode behind it would be a row of zeroes. Read
			// the one inode again purely to report why it failed.
			_, err := fs.ReadInode(entry.Inode)
			if err == nil {
				err = ErrInvalidInode
			}
			return fmt.Errorf("read inode for %s: %w", p, err)
		}

		fragments, err := fs.inodeFragments(inode, opts.IncludeUnwritten)
		if err != nil {
			fs.warn(WarnDegradedRead, "", fmt.Sprintf(
				"block map for %s is unreadable (%v); reported without fragments", p, err))
			fragments = nil
		}

		report.Files = append(report.Files, EXTFile{
			Filename:     p,
			InodeNumber:  entry.Inode,
			Generation:   inode.Generation,
			ParentInode:  entry.ParentInode,
			Type:         inodeTypeName(inode.Mode),
			IsFragmented: len(fragments) > 1,
			IsDeleted:    inodeDeleted(inode),
			Size:         int64(inode.Size),
			Times:        inode.Timestamps(),
			Fragments:    fragments,
		})
		return nil
	})
	if err != nil {
		return EXTReport{}, err
	}

	return report, nil
}

func inodeInterestingForReport(inode Inode) bool {
	if inode.Mode != 0 {
		return true
	}
	if inode.LinksCount > 0 {
		return true
	}
	if inode.Size > 0 || inode.Blocks512 > 0 {
		return true
	}
	return !inode.Dtime.IsZero()
}

func (fs *FS) computeImageEndOffset() int64 {
	sbSize := uint64(fs.sb.BlockSize) * fs.sb.BlocksCount
	size := sbSize
	if fs.imageSize > size {
		size = fs.imageSize
	}
	if size == 0 {
		return 0
	}
	if size > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(size) - 1
}

func inodeDeleted(inode Inode) bool {
	return inode.LinksCount == 0 || !inode.Dtime.IsZero()
}

func inodeTypeName(mode uint16) string {
	switch mode & inodeModeTypeMask {
	case inodeTypeDir:
		return "directory"
	case inodeTypeRegular:
		return "file"
	case inodeTypeSymlink:
		return "symlink"
	case inodeTypeChar:
		return "char_device"
	case inodeTypeBlock:
		return "block_device"
	case inodeTypeFIFO:
		return "fifo"
	case inodeTypeSocket:
		return "socket"
	default:
		return "unknown"
	}
}

// inodeFragments returns the contiguous on-disk spans holding a file's written
// data, at block granularity.
//
// Fragments describe written data only: holes and preallocated (unwritten) runs
// are excluded, and both break a fragment, which is why a preallocated file can
// report as fragmented. Set ReportOptions.IncludeUnwritten to include
// preallocated runs, whose blocks may still hold whatever occupied them before.
// Use Extents or DataRuns for the complete map.
func (fs *FS) inodeFragments(inode Inode, includeUnwritten bool) ([]FileFragment, error) {
	if inode.Size == 0 || fs.sb.BlockSize == 0 {
		return nil, nil
	}

	exts, err := fs.InodeExtents(inode, ExtentOptions{OmitSparse: true})
	if err != nil {
		return nil, err
	}

	blockSize := uint64(fs.sb.BlockSize)
	fragments := make([]FileFragment, 0, len(exts))

	for _, e := range exts {
		if e.Sparse() || e.Inline() {
			continue
		}
		if e.Unwritten() && !includeUnwritten {
			continue
		}
		// Fragments are block-granular and the end offset is inclusive, matching
		// the shape this field has always had.
		start := e.PhysicalBlock * blockSize
		end := (e.PhysicalBlock+e.Blocks)*blockSize - 1
		fragments = append(fragments, FileFragment{
			StartOffset: int64(start) + fs.opts.BaseOffset,
			EndOffset:   int64(end) + fs.opts.BaseOffset,
			Unwritten:   e.Unwritten(),
		})
	}

	return fragments, nil
}

// WriteReport writes a JSON report to the provided writer.
func (fs *FS) WriteReport(name string, w io.Writer) error {
	return fs.WriteReportWithOptions(name, ReportOptions{}, w)
}

// WriteReportDeep writes a deep-scan JSON report to the provided writer.
func (fs *FS) WriteReportDeep(name string, w io.Writer) error {
	return fs.WriteReportWithOptions(name, ReportOptions{DeepScan: true}, w)
}

// WriteReportWithOptions writes a JSON report using the provided options.
func (fs *FS) WriteReportWithOptions(name string, opts ReportOptions, w io.Writer) error {
	return fs.WriteReportWithOptionsContext(context.Background(), name, opts, w)
}

// WriteReportWithOptionsContext is WriteReportWithOptions with cancellation.
func (fs *FS) WriteReportWithOptionsContext(ctx context.Context, name string, opts ReportOptions, w io.Writer) error {
	// The writer is checked before the scan rather than after it: a nil writer
	// makes the whole scan wasted work, and the caller learns immediately.
	if w == nil {
		return fmt.Errorf("writer is nil")
	}
	rep, err := fs.ReportWithOptionsContext(ctx, name, opts)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	return enc.Encode(rep)
}
