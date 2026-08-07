// Package libext provides a pure-Go, read-only parser for EXT1/2/3/4 images.
//
// Basic usage:
//
//	img, err := os.Open("disk.img")
//	if err != nil {
//		// handle error
//	}
//	defer img.Close()
//
//	vol, err := libext.Open(img)
//	if err != nil {
//		// handle error
//	}
//	defer vol.Close()
//
//	sb := vol.Superblock()
//	_ = sb.BlockSize
//
//	root, err := vol.GetRootDirectory()
//	if err != nil {
//		// handle error
//	}
//
//	entries, err := root.ReadDir()
//	if err != nil {
//		// handle error
//	}
//	_ = entries
//
// # Layering
//
// The package is arranged in three layers, and code stays within its own:
//
//	binary decoding    util.go, layout.go   — offsets, endianness, bounds
//	structure decoding superblock.go, group.go, inode.go, dir.go, blockmap.go,
//	                   xattr.go, journal.go — on-disk structures to Go types
//	analysis           extent.go, deleted.go, dirslack.go, orphan.go,
//	                   report.go            — questions asked of those types
//
// Entry points live in ext.go. On-disk structures are plain data: they carry no
// behaviour beyond small accessors over their own fields, so decoding can be
// tested without a filesystem and analysis can be tested without an image.
//
// # Concurrency
//
// An *FS is safe for concurrent use by any number of goroutines. Everything a
// read touches is either immutable after Open — the superblock, the group
// descriptors, the options — or individually synchronised: the closed flag is
// atomic, the warning list is mutex-guarded, and the allocation bitmap cache
// uses an RWMutex over entries that never change once read. Close may be called
// while reads are in flight; those reads return io.ErrClosedPipe rather than
// reading through a closed handle.
//
// A *File is not safe for concurrent use. It carries a seek offset and caches
// its block map, so sharing one would be sharing mutable state for no gain.
// Open the inode once per goroutine instead.
//
// The library creates no goroutines unless asked. Setting Options.Parallelism
// above 1, or to ParallelismAuto, lets whole-image scans overlap their reads:
//
//	vol, err := libext.OpenFileWithOptions("disk.img", libext.Options{
//		Parallelism: libext.ParallelismAuto,
//	})
//
// Only scans with genuinely independent units parallelise — presently the
// inode-table sweep behind DeletedEntries and ScanDeleted, which divides across
// block groups. Everything else is sequential regardless of the setting.
//
// Three properties hold whatever the worker count, and each is covered by a
// test rather than only asserted here:
//
//   - Results are identical. Work is reassembled in input order, so parallelism
//     changes timing and nothing else.
//   - Callbacks stay single-threaded. ScanDeleted invokes its callback only from
//     the calling goroutine, so callers need no locking of their own.
//   - Goroutines are bounded and joined. The pool size is fixed at the
//     configured count regardless of how many items there are, and every worker
//     is joined before the call returns.
//
// The ...Context variants accept cancellation. A full inode-table scan over a
// large image is slow enough to want interrupting:
//
//	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
//	defer cancel()
//	err := vol.ScanDeletedContext(ctx, libext.DeletedScanOptions{}, handle)
//
// # Reading damaged images
//
// The parser refuses features it would otherwise answer wrongly about, such as
// BIGALLOC and META_BG, where the block addressing it assumes does not hold.
// Options.Permissive downgrades those refusals to warnings for cases where a
// partial answer beats none. Whatever the setting, Warnings reports where the
// answer may be incomplete.
package libext
