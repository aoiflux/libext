package libext

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// FS is a read-only parser for EXT1/2/3/4 filesystem images.
//
// # Concurrency
//
// Read methods on an FS are safe for concurrent use by any number of
// goroutines. Nothing they touch is mutated after Open returns: the superblock,
// the group descriptors and the options are written once during construction
// and only read afterwards, and io.ReaderAt is contractually safe for
// concurrent calls. The three pieces of state that do change during a read —
// the closed flag, the warning list, and the bitmap cache — are each
// synchronised individually and documented at their declaration.
//
// Close is also safe to call concurrently with reads. A read racing a Close
// either completes or returns io.ErrClosedPipe; it cannot observe a torn state.
//
// A *File is deliberately not safe for concurrent use: it carries a seek offset
// and caches its block map, so sharing one across goroutines would be sharing
// mutable state to no benefit. Open the same inode once per goroutine instead,
// which costs one inode read and keeps each block map private.
type FS struct {
	// Immutable after Open. These are the reason read methods need no locking.
	r         io.ReaderAt
	closer    io.Closer
	imageSize uint64
	opts      Options
	sb        Superblock
	kind      FSKind
	groups    []GroupDescriptor

	// closed is atomic because Close may run while reads are in flight; every
	// read consults it, so a plain bool would be a data race on every image.
	closed atomic.Bool

	// warnMu guards warnings, which read paths append to concurrently.
	warnMu   sync.Mutex
	warnings []Warning

	// bitmapMu guards the allocation bitmap cache. Bitmaps are immutable once
	// read, so readers share them freely under an RWMutex; only the first read
	// of a given group takes the write lock.
	bitmapMu    sync.RWMutex
	blockBitmap map[uint32][]byte
	inodeBitmap map[uint32][]byte
}

// Close releases the underlying file handle, if this FS owns one.
//
// It is idempotent and safe to call while reads are in flight; those reads fail
// with io.ErrClosedPipe rather than reading from a closed handle.
func (fs *FS) Close() error {
	// Swap rather than test-then-set: two concurrent Close calls must not both
	// reach the closer.
	if fs.closed.Swap(true) {
		return nil
	}
	if fs.closer == nil {
		return nil
	}
	return fs.closer.Close()
}

// IsClosed reports whether Close has been called.
func (fs *FS) IsClosed() bool {
	return fs.closed.Load()
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
	fs.warnMu.Lock()
	defer fs.warnMu.Unlock()

	out := make([]Warning, len(fs.warnings))
	copy(out, fs.warnings)
	return out
}

func (fs *FS) warn(code WarningCode, feature, detail string) {
	fs.warnMu.Lock()
	defer fs.warnMu.Unlock()

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
	if fs.closed.Load() {
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
