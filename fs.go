package libext

import (
	"fmt"
	"io"
	"io/fs"
	"os"
)

// FS is a read-only parser for EXT1/2/3/4 filesystem images.
type FS struct {
	r         io.ReaderAt
	closer    io.Closer
	imageSize uint64
	closed    bool

	sb     Superblock
	kind   FSKind
	groups []GroupDescriptor
}

// Open creates an EXT parser from a random-access reader.
// If reader supports Stat, the size is used for bounds checks.
func Open(r io.ReaderAt) (*FS, error) {
	if r == nil {
		return nil, fmt.Errorf("reader is nil")
	}

	var imageSize uint64
	type statReader interface {
		Stat() (fs.FileInfo, error)
	}
	if sr, ok := r.(statReader); ok {
		info, err := sr.Stat()
		if err == nil && !info.IsDir() {
			imageSize = uint64(info.Size())
		}
	}

	return OpenWithSize(r, imageSize)
}

// OpenWithSize creates an EXT parser from a random-access reader.
// If imageSize is 0, bounds checks are skipped.
func OpenWithSize(r io.ReaderAt, imageSize uint64) (*FS, error) {
	fs := &FS{r: r, imageSize: imageSize}
	if err := fs.loadSuperblock(); err != nil {
		return nil, err
	}
	// Check for unsupported incompat features
	if err := fs.CheckRequiredFeatures(); err != nil {
		return nil, err
	}
	if err := fs.loadGroupDescriptors(); err != nil {
		return nil, err
	}
	return fs, nil
}

// OpenFile opens an image path and parses its EXT metadata.
func OpenFile(path string) (*FS, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	fs, err := OpenWithSize(f, uint64(fi.Size()))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	fs.closer = f
	return fs, nil
}

func (fs *FS) Close() error {
	if fs.closed {
		return nil
	}
	fs.closed = true
	if fs.closer == nil {
		return nil
	}
	return fs.closer.Close()
}

func (fs *FS) IsClosed() bool {
	return fs.closed
}

func (fs *FS) Kind() FSKind {
	return fs.kind
}

func (fs *FS) Superblock() Superblock {
	return fs.sb
}

func (fs *FS) GroupDescriptors() []GroupDescriptor {
	out := make([]GroupDescriptor, len(fs.groups))
	copy(out, fs.groups)
	return out
}

func (fs *FS) blockOffset(block uint64) uint64 {
	return block * uint64(fs.sb.BlockSize)
}

func (fs *FS) readAt(off uint64, buf []byte) error {
	if fs.closed {
		return io.ErrClosedPipe
	}
	if err := checkBounds(off, uint64(len(buf)), fs.imageSize); err != nil {
		return err
	}
	n, err := fs.r.ReadAt(buf, int64(off))
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (fs *FS) readBlock(block uint64) ([]byte, error) {
	if block == 0 {
		return make([]byte, fs.sb.BlockSize), nil
	}
	buf := make([]byte, fs.sb.BlockSize)
	if err := fs.readAt(fs.blockOffset(block), buf); err != nil {
		return nil, fmt.Errorf("read block %d: %w", block, err)
	}
	return buf, nil
}
