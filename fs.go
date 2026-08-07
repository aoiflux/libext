package libext

import (
	"fmt"
	"io"
	"sync"
)

// FS is a read-only parser for EXT1/2/3/4 filesystem images.
//
// Read methods on an FS are safe for concurrent use by multiple goroutines:
// every read is stateless and io.ReaderAt is required to support concurrent
// calls. Close must not run concurrently with them. A *File is not safe for
// concurrent use, because it carries a seek offset.
type FS struct {
	r         io.ReaderAt
	closer    io.Closer
	imageSize uint64
	closed    bool

	opts Options

	// mu guards warnings, which read paths append to concurrently.
	mu       sync.Mutex
	warnings []Warning

	sb     Superblock
	kind   FSKind
	groups []GroupDescriptor
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

// Options returns the effective options this FS was opened with, including any
// image size resolved by probing the reader.
func (fs *FS) Options() Options {
	return fs.opts
}

// maxWarnings bounds the recorded warnings. A deep scan over a damaged image can
// produce one warning per inode, so the list is capped and the overflow reported
// once rather than growing without limit.
const maxWarnings = 256

// Warnings returns the non-fatal conditions recorded so far. A non-empty result
// means some part of the answer may be incomplete or wrong; the codes say which
// part. Warnings accumulate as parsing proceeds, so read them after the calls
// you care about, not only after Open.
func (fs *FS) Warnings() []Warning {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	out := make([]Warning, len(fs.warnings))
	copy(out, fs.warnings)
	return out
}

func (fs *FS) warn(code WarningCode, feature, detail string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	switch {
	case len(fs.warnings) >= maxWarnings:
		return
	case len(fs.warnings) == maxWarnings-1:
		fs.warnings = append(fs.warnings, Warning{
			Code:   WarnDegradedRead,
			Detail: fmt.Sprintf("warning limit of %d reached; further warnings suppressed", maxWarnings),
		})
	default:
		fs.warnings = append(fs.warnings, Warning{Code: code, Feature: feature, Detail: detail})
	}
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
