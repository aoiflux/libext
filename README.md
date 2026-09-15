# libext

libext is a pure-Go, read-only parser for EXT1, EXT2, EXT3, and EXT4 filesystem
images. It is designed for tooling, inspection, extraction, and forensic-style
workloads where you want a small Go API over raw filesystem structures without a
cgo dependency.

The package can open an image from any `io.ReaderAt`, detect the filesystem
kind, read superblock and group metadata, walk directories, open files by inode
or path, stream file contents, inspect journals, and read extended attributes.

## Overview

libext focuses on filesystem parsing rather than mounting. It gives you direct
access to EXT metadata and content through a Go-native API:

- Pure Go, read-only library
- EXT1/2/3/4 detection and parsing
- Works with files, memory-backed images, and custom `io.ReaderAt` sources
- Supports directory traversal, path lookup, file reads, symlink reads, xattrs,
  journal inspection, and integrity helpers
- Tracks parity with the TSK EXT parser in [PARITY_TSK.md](PARITY_TSK.md)

## Why libext

The package uses a `Volume`/`File` style API because that model is practical for
filesystem inspection tools:

- open a volume once
- open files by inode number or path
- use file-like reads for content access
- keep traversal and metadata access close to the opened filesystem handle

That makes it easier to port or design tooling that already expects a
filesystem-object model instead of lower-level block parsing primitives.

## Installation

```bash
go get github.com/aoiflux/libext
```

## Requirements

The toolchain requirement is declared in [go.mod](go.mod); use that Go release
or newer when building or testing the project.

## Quick Start

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/aoiflux/libext"
)

func main() {
	img, err := os.Open("disk.img")
	if err != nil {
		log.Fatal(err)
	}
	defer img.Close()

	vol, err := libext.Open(img)
	if err != nil {
		log.Fatal(err)
	}
	defer vol.Close()

	fmt.Printf("kind=%s block_size=%d\n", vol.Kind(), vol.Superblock().BlockSize)

	root, err := vol.GetRootDirectory()
	if err != nil {
		log.Fatal(err)
	}

	entries, err := root.ReadDir()
	if err != nil {
		log.Fatal(err)
	}

	for _, e := range entries {
		fmt.Println(e.Name)
	}
	}
```

## Feature Support

Current support is strongest in core read paths and ext4-era metadata commonly
needed for inspection tools.

Implemented or available now:

- EXT1/2/3/4 kind detection
- Superblock, group descriptor, and inode parsing
- Direct, indirect, double-indirect, and triple-indirect block mapping
- Extent-based file reads
- Sparse and hole-aware reads
- Root, inode-number, and path-based opens
- Directory parsing and recursive traversal
- Symlink target reads
- HTree/indexed directory detection and validation
- ext4 metadata checksum verification for superblocks, group descriptors,
  inodes, block bitmaps, and inode bitmaps
- Extended attribute parsing
- Journal status reporting and transaction enumeration
- Journalled block and inode recovery, directly or through a `JournalIndex`
- Fast commit record parsing, including the unlink records that often hold the
  only surviving name of a recently deleted file
- Inline data: files and directories stored inside the inode and its extended
  attribute area
- Corruption and integrity helper APIs

Not fully supported yet:

- Compression
- External journal devices (detected, not read)
- Encryption
- Snapshot support
- Some partially supported optional and recovery-related features

For detailed parity tracking against the TSK EXT parser, see
[PARITY_TSK.md](PARITY_TSK.md).

## API Highlights

Constructors and volume access:

- `Open(r io.ReaderAt) (*FS, error)`
- `OpenWithSize(r io.ReaderAt, imageSize uint64) (*FS, error)`
- `OpenFile(path string) (*FS, error)`
- `(*FS).Close() error`
- `(*FS).Kind() FSKind`
- `(*FS).Superblock() Superblock`
- `(*FS).GroupDescriptors() []GroupDescriptor`
- `(*FS).Capabilities() Capabilities`

`Capabilities` says what this volume can record, which is not the same question
as what it does record: an ext2 volume has no journal and no birth times at all,
so a consumer that cannot see the difference reports every file as having lost a
creation date. Most fields are read from the superblock's feature flags rather
than fixed by the format, so an ext2 volume and an ext4 volume answer
differently — `Journaled`, `Extents`, `CreationTimes` and
`SubSecondTimestamps` among them. Each field's doc comment says whether it is a
fact about ext or state read from the volume in hand.

Opening and traversal:

- `(*FS).GetRootDirectory() (*File, error)`
- `(*FS).Open(inodeNum uint32) (*File, error)`
- `(*FS).OpenPath(path string) (*File, error)`
- `(*FS).ListDir(inodeNum uint32) ([]DirEntry, error)`
- `(*FS).LookupPath(p string) (DirEntry, error)`
- `(*FS).WalkDir(startInode uint32, fn func(p string, entry DirEntry) error) error`
- `(*FS).WalkDirContext(ctx context.Context, startInode uint32, fn func(p string, entry DirEntry) error) error`
- `(*FS).WalkDirWithInode(startInode uint32, fn func(p string, entry DirEntry, inode Inode) error) error`
- `(*FS).WalkDirWithInodeContext(ctx context.Context, startInode uint32, fn func(p string, entry DirEntry, inode Inode) error) error`

Entries from a walk carry `Generation` and `ParentInode` alongside the rest of
their inode metadata, because the walk reads each child's inode anyway to decide
whether to descend.

Naming an inode:

- `(*FS).PathFor(inodeNum uint32) (string, error)`
- `(*FS).PathForContext(ctx context.Context, inodeNum uint32) (string, error)`
- `(*FS).BuildPathIndex() (*PathIndex, error)`
- `(*FS).BuildPathIndexContext(ctx context.Context) (*PathIndex, error)`
- `(*PathIndex).PathFor(inodeNum uint32) (string, error)`
- `(*PathIndex).PathsFor(inodeNum uint32) []string`
- `(*PathIndex).ParentOf(inodeNum uint32) (uint32, bool)`
- `(*PathIndex).Len() int`
- `(*PathIndex).Truncated() bool`

File access:

- `(*File).Name() string`
- `(*File).InodeNumber() uint32`
- `(*File).IsDirectory() bool`
- `(*File).Size() int64`
- `(*File).Read(p []byte) (int, error)`
- `(*File).ReadAt(p []byte, off int64) (int, error)`
- `(*File).ReadAll() ([]byte, error)`
- `(*File).ReadLink() (string, error)`
- `(*File).ReadDir() ([]DirEntry, error)`
- `(*FS).ReadFile(inodeNum uint32) ([]byte, error)`

Feature, xattr, journal, and integrity helpers:

- `(*FS).CheckRequiredFeatures() error`
- `(*FS).CheckOptionalFeatures() []string`
- `(*FS).DescribeFeatures() string`
- `(*FS).Report(name string) (EXTReport, error)`
- `(*FS).ReportDeep(name string) (EXTReport, error)`
- `(*FS).ReportWithOptions(name string, opts ReportOptions) (EXTReport, error)`
- `(*FS).WriteReport(name string, w io.Writer) error`
- `(*FS).WriteReportDeep(name string, w io.Writer) error`
- `(*FS).WriteReportWithOptions(name string, opts ReportOptions, w io.Writer) error`
- `(EXTReport).Summary() EXTReportSummary`
- `(EXTReport).FilterFiles(func(EXTFile) bool) []EXTFile`
- `(EXTReport).FilesByType(t string) []EXTFile`
- `(EXTReport).DeletedFiles() []EXTFile`
- `(EXTReport).FragmentedFiles() []EXTFile`
- `(*FS).GetXAttrs(inodeNum uint32) (XAttrList, error)`
- `(*FS).GetInlineXAttrs(inode *Inode) (XAttrList, error)`
- `(*FS).DescribeJournalStatus() (string, error)`
- `(*FS).GetJournalInode() uint32`
- `(*FS).ListJournalTransactions() ([]JournalTransaction, error)`
- `(*FS).ListJournalTransactionsContext(ctx context.Context) ([]JournalTransaction, error)`
- `(*FS).JournalBlockCopies(fsBlock uint64) ([][]byte, error)`
- `(*FS).JournalInodeVersions(inodeNum uint32) ([]Inode, error)`
- `(*FS).BuildJournalIndex() (*JournalIndex, error)`
- `(*FS).BuildJournalIndexContext(ctx context.Context) (*JournalIndex, error)`
- `(*JournalIndex).BlockCopies(fsBlock uint64) ([][]byte, error)`
- `(*JournalIndex).InodeVersions(inodeNum uint32) ([]Inode, error)`
- `(*JournalIndex).HasBlock(fsBlock uint64) bool`
- `(*JournalIndex).CopyCount(fsBlock uint64) int`
- `(*JournalIndex).Transactions() []JournalTransaction`
- `(*JournalIndex).Len() int`
- `(*FS).ValidateSuperblockIntegrity() []CorruptionReport`
- `(*FS).ValidateInodeIntegrity(inode *Inode) []CorruptionReport`
- `(*FS).ValidateGroupDescriptorIntegrity(groupNum uint32, gd *GroupDescriptor) []CorruptionReport`

### Naming journal-discovered inodes

The journal reports what changed as block and inode numbers, never as paths.
`BuildPathIndex` walks the tree once so that any number of those numbers can be
named from a map:

```go
ctx := context.Background()

ix, err := vol.BuildPathIndexContext(ctx)
if err != nil {
	// handle error
}

txs, err := vol.ListJournalTransactionsContext(ctx)
if err != nil {
	// handle error
}

for _, tx := range txs {
	for _, tag := range tx.Tags {
		// tag.FSBlock names a filesystem block whose contents were journalled.
		_ = tag.FSBlock
	}
}

// Given an inode number from any source — the journal, the inode table, a
// deleted-entry scan — the index names it without walking again.
path, err := ix.PathFor(inodeNum)
if err != nil {
	// errors.Is(err, libext.ErrPathNotFound) means nothing in the live tree
	// links that inode, which is the ordinary answer for a deleted one.
}
_ = path
```

`(*FS).PathFor` is the single-shot form. It is cheap for a directory, which it
names by following `..` upward, but for anything else it walks the tree on every
call — so naming inodes in bulk should go through the index.

### Recovering what the journal remembers

`JournalBlockCopies` and `JournalInodeVersions` each walk the whole journal, so
they answer one question well and a thousand questions badly. `JournalIndex`
walks once and keeps the tag table; the block contents stay on disk and are read
per query.

```go
jx, err := vol.BuildJournalIndexContext(ctx)
if err != nil {
	// A filesystem with no journal reports that here rather than returning an
	// empty index.
}

// The cheap question first: most blocks were never journalled, and HasBlock
// answers from the map without reading anything.
for _, block := range changedBlocks {
	if !jx.HasBlock(block) {
		continue
	}
	copies, err := jx.BlockCopies(block) // newest first
	if err != nil {
		// handle error
	}
	_ = copies
}

// An unlink zeroes the extent tree in the live inode, but a journalled copy of
// the same inode-table block from before the unlink still carries it.
versions, err := jx.InodeVersions(inodeNum)
if err != nil {
	// handle error
}
for _, prior := range versions {
	_ = prior.Size // the inode as it stood when that transaction was written
}
```

Pair it with `BuildPathIndex` to turn the inode numbers the journal reports into
names: one walk of the tree and one walk of the journal answer every question
about both.

## Error Handling

The package returns ordinary Go errors and uses exported sentinel errors for the
main failure modes:

- `ErrInvalidSuperblock`
- `ErrChecksumMismatch`
- `ErrUnsupportedLayout`
- `ErrInvalidInode`
- `ErrNotDirectory`
- `ErrNotRegularFile`
- `ErrNotSymlink`
- `ErrPathNotFound`

Use `errors.Is` when you want to branch on a known failure:

```go
f, err := vol.OpenPath("/does/not/exist")
if err != nil {
	if errors.Is(err, libext.ErrPathNotFound) {
		log.Printf("missing path")
		return
	}
	log.Fatal(err)
}
_ = f
```

The library also wraps some errors with additional context, such as the inode or
path involved in the failure.

## Report Consumption

Use struct-based report APIs when you want to process data in memory instead of
writing JSON.

```go
report, err := vol.ReportDeep("ext-report")
if err != nil {
	log.Fatal(err)
}

summary := report.Summary()
fmt.Printf("total=%d deleted=%d fragmented=%d\n",
	summary.Total,
	summary.Deleted,
	summary.Fragmented,
)

regular := report.FilesByType("file")
largeDeleted := report.FilterFiles(func(f libext.EXTFile) bool {
	return f.IsDeleted && f.Size > 1<<20
})

_ = regular
_ = largeDeleted
```

Every report carries `schema_version`, `library_version` and `generated` at its
root, plus the volume's `capabilities`. A report is evidence, and evidence
outlives the tool that produced it: a consumer should compare `schema_version`
against `ReportSchemaVersion` and refuse a document it does not understand
rather than read fields whose meaning may have moved. The version is incremented
when a field is removed, renamed, or changes meaning; adding a field does not
increment it, because a consumer that ignores the addition still reads the
document correctly.

All offsets in a report share one origin. `start_offset`, `end_offset`,
`ext_meta.offset` and every fragment offset include `Options.BaseOffset`, so a
report of a partition opened from a whole-disk reader describes that partition
where it actually sits.

## Platform Notes

- The library is pure Go and does not require cgo.
- It is read-only. It does not mount, modify, or repair filesystems.
- Any platform supported by Go can use the library as long as an `io.ReaderAt`
  source is available.
- `OpenFile` is convenient for regular image files. `Open` and `OpenWithSize`
  are better when you already have a file handle, memory mapping, or a custom
  reader implementation.
- The `tsk/` directory is reference and parity material; the library itself is
  implemented in Go.

## Examples

The repository includes runnable examples:

- `go run ./examples/basic <filesystem_image>`
- `go run ./examples/traverse <filesystem_image> <start_path>`
- `go run ./examples/extract <filesystem_image> <file_path> <output_path>`
- `go run ./examples/report <filesystem_image> <output_json> <standard|deep>`
- `go run ./examples/report_struct <filesystem_image> <standard|deep>`
- `go run ./examples/journal <filesystem_image>`
- `go run ./examples/xattr <filesystem_image> [path]`

They cover opening an image, walking directories, extracting file contents,
inspecting the journal, generating filesystem reports, and reading extended
attributes.

For report usage, `examples/report` demonstrates JSON output, while
`examples/report_struct` demonstrates direct struct consumption with summary and
filtering logic.

## Performance Notes

libext is optimized for direct parsing of on-disk structures rather than for a
kernel-style mounted filesystem interface.

- File content is read lazily through block mapping.
- `ReadAt` is a good fit for targeted extraction and random access.
- `ReadAll` is convenient, but it allocates the full file size.
- The parser preserves sparse regions and zero-filled holes during reads.
- For large images and repeated access patterns, reuse a single open volume
  instead of reopening the image.

If you need exact numbers for a workload, benchmark with your image sizes,
directory shapes, and access patterns. The dominant costs are usually image I/O,
directory fanout, and extent or indirect-block traversal depth.

## Development

Common development commands:

```bash
go test ./...
```

```bash
gofmt -w *.go examples/*/*.go
```

The test suite covers core parsing, feature validation, checksums, HTree logic,
journal parsing, xattrs, and corruption helpers. Project-level parity goals are
tracked in [PARITY_TSK.md](PARITY_TSK.md).
