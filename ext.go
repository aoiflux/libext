// Package entrypoint.
//
// ext.go holds the ways into the library. Everything else hangs off the *FS a
// constructor here returns:
//
//	Opening        Open, OpenWithOptions, OpenWithSize, OpenFile, OpenFileWithOptions
//	Navigating     GetRootDirectory, Open, OpenPath, ListDir, ListDirEx, WalkDir
//	Reading        File.Read, File.ReadAt, File.ReadAll, ReadFile, File.ReadLink
//	Locating       Extents, DataRuns, MetadataBlocks  (see extent.go)
//	Metadata       ReadInode, Inode.Timestamps, GetXAttrs  (see inode.go, xattr.go)
//	Deleted data   DeletedEntries, ScanDeleted, OrphanInodes, ScanDirSlack
//	Allocation     InodeAllocated, BlockAllocated, InodeBitmap, BlockBitmap
//	Diagnostics    Warnings, CheckRequiredFeatures, Superblock, GroupDescriptors
//
// The library is strictly read-only: nothing here writes to the image.
package libext

import (
	"fmt"
	"io"
	"os"
)

const (
	// Author information
	Author = "libext contributors"
)

// Open creates an EXT parser from a random-access reader using default options.
//
// The reader is probed for a Size or Stat method to establish the bounds for
// every subsequent read. An io.SectionReader carved out of a larger disk image
// is the expected shape when a volume sits inside a partition, and it satisfies
// that probe.
func Open(r io.ReaderAt) (*FS, error) {
	return OpenWithOptions(r, Options{})
}

// OpenWithOptions creates an EXT parser from a random-access reader.
//
// When opts.ImageSize is 0 the reader is probed for a Size() int64 or Stat()
// method; if neither is present, reads are unbounded. Set opts.BaseOffset to
// the partition's start so that reported disk offsets are image-absolute.
func OpenWithOptions(r io.ReaderAt, opts Options) (*FS, error) {
	return openWithOptions(r, opts, true)
}

// OpenWithSize creates an EXT parser from a random-access reader.
// If imageSize is 0, bounds checks are skipped.
//
// Prefer OpenWithOptions, which probes the reader for its size instead of
// leaving reads unbounded.
func OpenWithSize(r io.ReaderAt, imageSize uint64) (*FS, error) {
	return openWithOptions(r, Options{ImageSize: imageSize}, false)
}

// OpenFile opens an image path and parses its EXT metadata. The returned FS owns
// the file handle; Close releases it.
func OpenFile(path string) (*FS, error) {
	return OpenFileWithOptions(path, Options{})
}

// OpenFileWithOptions opens an image path with explicit options.
func OpenFileWithOptions(path string, opts Options) (*FS, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if opts.ImageSize == 0 {
		opts.ImageSize = uint64(fi.Size())
	}
	fs, err := OpenWithOptions(f, opts)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	fs.closer = f
	return fs, nil
}

func openWithOptions(r io.ReaderAt, opts Options, probe bool) (*FS, error) {
	if r == nil {
		return nil, fmt.Errorf("reader is nil")
	}
	if opts.ImageSize == 0 && probe {
		opts.ImageSize = probeReaderSize(r)
	}

	fs := &FS{r: r, imageSize: opts.ImageSize, opts: opts}
	if err := fs.loadSuperblock(); err != nil {
		return nil, err
	}
	if err := fs.checkFeatures(); err != nil {
		return nil, err
	}
	if err := fs.loadGroupDescriptors(); err != nil {
		return nil, err
	}
	return fs, nil
}
