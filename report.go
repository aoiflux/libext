package libext

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
)

// EXTReport captures a filesystem-level view similar to common forensic report formats.
type EXTReport struct {
	Name        string    `json:"name"`
	StartOffset int64     `json:"start_offset"`
	EndOffset   int64     `json:"end_offset"`
	Filesystem  EXTMeta   `json:"ext_meta"`
	Files       []EXTFile `json:"files"`
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
	Offset    int64  `json:"offset"`
}

// EXTFile describes one reachable inode-backed path in the filesystem tree.
type EXTFile struct {
	Filename     string         `json:"filename"`
	Type         string         `json:"type"`
	IsFragmented bool           `json:"is_fragmented"`
	IsDeleted    bool           `json:"is_deleted"`
	Size         int64          `json:"size"`
	Fragments    []FileFragment `json:"fragments"`
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
	sb := fs.Superblock()
	imageEnd := fs.computeImageEndOffset()
	report := EXTReport{
		Name:        name,
		StartOffset: 0,
		EndOffset:   imageEnd,
		Filesystem: EXTMeta{
			Type:      string(fs.Kind()),
			BlockSize: int(sb.BlockSize),
			Offset:    0,
		},
		Files: make([]EXTFile, 0, 128),
	}

	if opts.DeepScan {
		paths := fs.collectReachablePathsByInode()
		for inodeNum := uint32(1); inodeNum <= fs.sb.InodesCount; inodeNum++ {
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

			name := paths[inodeNum]
			if name == "" {
				name = fmt.Sprintf("inode:%d", inodeNum)
			}
			report.Files = append(report.Files, EXTFile{
				Filename:     name,
				Type:         inodeTypeName(inode.Mode),
				IsFragmented: len(fragments) > 1,
				IsDeleted:    inodeDeleted(inode),
				Size:         int64(inode.Size),
				Fragments:    fragments,
			})
		}
		return report, nil
	}

	err := fs.WalkDir(RootInode, func(p string, entry DirEntry) error {
		inode, err := fs.ReadInode(entry.Inode)
		if err != nil {
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
			Type:         inodeTypeName(inode.Mode),
			IsFragmented: len(fragments) > 1,
			IsDeleted:    inodeDeleted(inode),
			Size:         int64(inode.Size),
			Fragments:    fragments,
		})
		return nil
	})
	if err != nil {
		return EXTReport{}, err
	}

	return report, nil
}

func (fs *FS) collectReachablePathsByInode() map[uint32]string {
	paths := map[uint32]string{
		RootInode: "/",
	}
	type dirItem struct {
		inode uint32
		p     string
	}
	queue := []dirItem{{inode: RootInode, p: "/"}}
	seenDirs := map[uint32]bool{RootInode: true}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		entries, err := fs.ListDir(item.inode)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Name == "." || e.Name == ".." {
				continue
			}
			childPath := path.Join(item.p, e.Name)
			if _, ok := paths[e.Inode]; !ok {
				paths[e.Inode] = childPath
			}

			child, err := fs.ReadInode(e.Inode)
			if err != nil || !child.IsDirectory {
				continue
			}
			if seenDirs[e.Inode] {
				continue
			}
			seenDirs[e.Inode] = true
			queue = append(queue, dirItem{inode: e.Inode, p: childPath})
		}
	}

	return paths
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
	rep, err := fs.ReportWithOptions(name, opts)
	if err != nil {
		return err
	}
	if w == nil {
		return fmt.Errorf("writer is nil")
	}
	enc := json.NewEncoder(w)
	return enc.Encode(rep)
}
