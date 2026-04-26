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

## Why libntfs

libext does not depend on libntfs. The comparison is about API shape.

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

## Go Version

The module currently declares Go `1.25` in [go.mod](go.mod). Use Go 1.25 or
newer when building or testing the project.

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
- Corruption and integrity helper APIs

Not fully supported yet:

- Fast commit
- Compression
- Journal devices
- Inline data
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

Opening and traversal:

- `(*FS).GetRootDirectory() (*File, error)`
- `(*FS).Open(inodeNum uint32) (*File, error)`
- `(*FS).OpenPath(path string) (*File, error)`
- `(*FS).ListDir(inodeNum uint32) ([]DirEntry, error)`
- `(*FS).LookupPath(p string) (DirEntry, error)`
- `(*FS).WalkDir(startInode uint32, fn func(p string, entry DirEntry) error) error`

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
- `(*FS).GetXAttrs(inodeNum uint32) (XAttrList, error)`
- `(*FS).GetInlineXAttrs(inode *Inode) (XAttrList, error)`
- `(*FS).DescribeJournalStatus() (string, error)`
- `(*FS).GetJournalInode() uint32`
- `(*FS).ListJournalTransactions() ([]JournalTransaction, error)`
- `(*FS).ValidateSuperblockIntegrity() []CorruptionReport`
- `(*FS).ValidateInodeIntegrity(inode *Inode) []CorruptionReport`
- `(*FS).ValidateGroupDescriptorIntegrity(groupNum uint32, gd *GroupDescriptor) []CorruptionReport`

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
- `go run ./examples/journal <filesystem_image>`
- `go run ./examples/xattr <filesystem_image> [path]`

They cover opening an image, walking directories, extracting file contents,
inspecting the journal, and reading extended attributes.

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
