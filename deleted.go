package libext

import (
	"context"
	"errors"
	"path"
	"sort"
)

// Deleted and orphaned inode enumeration.
//
// Three independent sources are consulted, because on ext4 no single one is
// sufficient. The inode table retains metadata for unlinked inodes until the
// slot is reused. Directory slack retains the *name*, which the inode never
// held, and often survives after the inode itself has been recycled. The orphan
// list records inodes that were unlinked while still open, which a clean
// unmount would otherwise leave no trace of.

// DeletedSource identifies where evidence of a deletion was found.
type DeletedSource uint8

const (
	// DeletedSourceInodeTable is an inode whose table entry shows a deletion
	// time or no remaining links.
	DeletedSourceInodeTable DeletedSource = iota + 1
	// DeletedSourceOrphanList is an inode on the legacy s_last_orphan chain.
	DeletedSourceOrphanList
	// DeletedSourceOrphanFile is an inode listed in the ext4 orphan file.
	DeletedSourceOrphanFile
	// DeletedSourceDirSlack is a name recovered from the unused tail of a
	// directory record. The inode it names may since have been reused.
	DeletedSourceDirSlack
	// DeletedSourceJournal is a prior inode state recovered from the journal.
	DeletedSourceJournal
	// DeletedSourceFastCommit is an unlink recorded in the fast-commit area.
	DeletedSourceFastCommit
)

func (s DeletedSource) String() string {
	switch s {
	case DeletedSourceInodeTable:
		return "inode_table"
	case DeletedSourceOrphanList:
		return "orphan_list"
	case DeletedSourceOrphanFile:
		return "orphan_file"
	case DeletedSourceDirSlack:
		return "dir_slack"
	case DeletedSourceJournal:
		return "journal"
	case DeletedSourceFastCommit:
		return "fast_commit"
	default:
		return "unknown"
	}
}

// RecoveryConfidence rates how much of a deleted file's content is likely to
// still be on disk.
type RecoveryConfidence uint8

const (
	// RecoveryNone means no block map survives, so the content cannot be located
	// from metadata at all. ext4 zeroes the extent tree on unlink, which makes
	// this the common outcome for files deleted on a live ext4 filesystem.
	RecoveryNone RecoveryConfidence = iota
	// RecoveryPartial means a block map survives but some of its blocks have
	// been reallocated, so the recovered content is contaminated.
	RecoveryPartial
	// RecoveryLikely means a block map survives and none of its blocks have been
	// handed to another file.
	RecoveryLikely
)

func (c RecoveryConfidence) String() string {
	switch c {
	case RecoveryPartial:
		return "partial"
	case RecoveryLikely:
		return "likely"
	default:
		return "none"
	}
}

// DeletedEntry is one piece of evidence that something was deleted.
//
// The sources differ in what they can supply: an inode-table entry has metadata
// but no name, while directory slack has a name but metadata only if the inode
// it points at still holds it. Fields that a source cannot fill are zero.
type DeletedEntry struct {
	Inode       uint32
	Name        string // empty when only the inode survives
	ParentInode uint32 // 0 when the parent is unknown
	Path        string // best effort; empty when the parent is unknown
	Source      DeletedSource

	Mode  uint16
	Size  uint64
	UID   uint32
	GID   uint32
	Times Timestamps

	// Allocated reports whether the inode bitmap still marks this inode in use.
	// A deleted inode that is still marked allocated has not been reused.
	Allocated bool

	// Extents is the surviving block map, empty when it did not survive.
	Extents     []Extent
	Recoverable RecoveryConfidence
}

// DeletedScanOptions selects which sources a scan consults. The zero value
// consults all of them and skips uninitialised groups, which is the safe
// default: negative-sense fields keep that true for a caller who sets only
// MaxResults.
type DeletedScanOptions struct {
	SkipInodeTable bool
	SkipOrphanList bool
	SkipDirSlack   bool

	// IncludeUninit scans block groups whose inode tables have never been
	// initialised. Those hold whatever preceded the filesystem, so entries found
	// there are pre-existing disk contents rather than deleted files.
	IncludeUninit bool

	// MaxResults stops the scan after this many entries. 0 means no limit.
	MaxResults int
}

// DeletedEntries enumerates deleted and orphaned inodes from every source.
func (fs *FS) DeletedEntries() ([]DeletedEntry, error) {
	return fs.DeletedEntriesWithOptions(DeletedScanOptions{})
}

// DeletedEntriesWithOptions enumerates deleted inodes from the selected sources.
//
// Prefer ScanDeleted on large images: this collects every result in memory, and
// a multi-terabyte volume has hundreds of millions of inode table entries.
func (fs *FS) DeletedEntriesWithOptions(opts DeletedScanOptions) ([]DeletedEntry, error) {
	var out []DeletedEntry
	err := fs.ScanDeleted(opts, func(e DeletedEntry) error {
		out = append(out, e)
		return nil
	})
	return out, err
}

// errScanStop unwinds a scan once MaxResults is reached.
var errScanStop = errors.New("scan complete")

// ScanDeleted streams deleted-inode evidence to fn.
//
// Returning an error from fn stops the scan and propagates that error. This is
// the form to use on large images: nothing is accumulated.
//
// fn is always called from the calling goroutine, in a stable order, even when
// Options.Parallelism has the underlying reads running concurrently. Callers
// need no synchronisation of their own.
func (fs *FS) ScanDeleted(opts DeletedScanOptions, fn func(DeletedEntry) error) error {
	return fs.ScanDeletedContext(context.Background(), opts, fn)
}

// ScanDeletedContext is ScanDeleted with cancellation.
//
// A full inode table scan on a large image takes a long time; cancelling the
// context stops it at the next checkpoint and returns the context's error. Work
// already handed to the callback is not retracted.
func (fs *FS) ScanDeletedContext(ctx context.Context, opts DeletedScanOptions, fn func(DeletedEntry) error) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if fn == nil {
		return errors.New("scan callback is nil")
	}

	sc := &deletedScan{
		fs:      fs,
		ctx:     ctx,
		opts:    opts,
		seen:    make(map[uint32]bool),
		slack:   make(map[uint32]slackName),
		claimed: make(map[uint32]bool),
	}

	err := sc.run(fn)
	if errors.Is(err, errScanStop) {
		return nil
	}
	return err
}

// deletedScan carries the state one scan needs. Nothing here is shared with
// another scan, and the maps are only touched from the calling goroutine: the
// parallel phase reads them but never writes.
type deletedScan struct {
	fs   *FS
	ctx  context.Context
	opts DeletedScanOptions

	// seen suppresses duplicate inode-table reports when an inode is also found
	// on the orphan list.
	seen map[uint32]bool

	// slack maps an inode to the name recovered for it from directory slack;
	// claimed records which of those names were attached to an emitted entry.
	slack    map[uint32]slackName
	claimed  map[uint32]bool
	dirPaths map[uint32]string

	count int
}

func (sc *deletedScan) emit(fn func(DeletedEntry) error, e DeletedEntry) error {
	if err := fn(e); err != nil {
		return err
	}
	sc.count++
	if sc.opts.MaxResults > 0 && sc.count >= sc.opts.MaxResults {
		return errScanStop
	}
	return nil
}

func (sc *deletedScan) run(fn func(DeletedEntry) error) error {
	// Directory slack is indexed before anything is emitted. The inode table
	// knows a deleted file's metadata but never its name, and slack knows the
	// name but nothing else; indexing first lets a single entry carry both
	// instead of reporting the same deletion twice from two angles.
	if !sc.opts.SkipDirSlack {
		sc.indexSlack()
	}

	// Orphans next: they are few, and knowing them lets the inode table pass
	// report the more specific source.
	if !sc.opts.SkipOrphanList {
		if err := sc.scanOrphans(fn); err != nil {
			return err
		}
	}
	if !sc.opts.SkipInodeTable {
		if err := sc.scanInodeTable(fn); err != nil {
			return err
		}
	}
	// Whatever slack named that the inode table did not account for: the name
	// outlived its inode, which has since been reused or reallocated.
	if !sc.opts.SkipDirSlack {
		if err := sc.emitUnclaimedSlack(fn); err != nil {
			return err
		}
	}
	return nil
}

// slackName is a name recovered from directory slack, with its location.
type slackName struct {
	name        string
	parentInode uint32
	path        string
}

// indexSlack walks reachable directories and records the names it recovers.
func (sc *deletedScan) indexSlack() {
	fs := sc.fs
	sc.slack = make(map[uint32]slackName)
	sc.dirPaths = fs.collectReachablePathsByInode()

	for dirInode, dirPath := range sc.dirPaths {
		inode, err := fs.ReadInode(dirInode)
		if err != nil || !inode.IsDirectory {
			continue
		}
		entries, err := fs.ScanDirSlack(dirInode)
		if err != nil {
			continue
		}
		for _, s := range entries {
			// A record that duplicates a live entry is left over from rewriting
			// the directory, not evidence that anything was deleted.
			if s.ShadowsLive {
				continue
			}
			if _, exists := sc.slack[s.Inode]; exists {
				continue
			}
			sc.slack[s.Inode] = slackName{
				name:        s.Name,
				parentInode: dirInode,
				path:        path.Join(dirPath, s.Name),
			}
		}
	}
}

// nameFor attaches a recovered name to an entry, if slack supplied one.
func (sc *deletedScan) nameFor(e *DeletedEntry) {
	if s, ok := sc.slack[e.Inode]; ok {
		e.Name = s.name
		e.ParentInode = s.parentInode
		e.Path = s.path
		sc.claimed[e.Inode] = true
	}
}

// emitUnclaimedSlack reports names whose inode no longer shows the deletion,
// because the slot has been reused or reallocated to a live file.
func (sc *deletedScan) emitUnclaimedSlack(fn func(DeletedEntry) error) error {
	// Iterate deterministically: map order would make output unstable.
	nums := make([]uint32, 0, len(sc.slack))
	for num := range sc.slack {
		if !sc.claimed[num] {
			nums = append(nums, num)
		}
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })

	for _, num := range nums {
		s := sc.slack[num]
		entry := DeletedEntry{
			Inode:       num,
			Name:        s.name,
			ParentInode: s.parentInode,
			Path:        s.path,
			Source:      DeletedSourceDirSlack,
		}
		if allocated, err := sc.fs.InodeAllocated(num); err == nil {
			entry.Allocated = allocated
		}
		if err := sc.emit(fn, entry); err != nil {
			return err
		}
	}
	return nil
}

// scanInodeTable walks every initialised inode table entry looking for
// deletion evidence.
// scanInodeTable walks every initialised inode table entry looking for
// deletion evidence.
//
// Block groups are independent — each owns a disjoint span of the inode table —
// so they are examined in parallel when the caller asked for it. Emission stays
// sequential and in group order: the callback is invoked from the calling
// goroutine only, so a caller never needs to synchronise it, and the output of
// a parallel run is identical to a sequential one.
func (sc *deletedScan) scanInodeTable(fn func(DeletedEntry) error) error {
	fs := sc.fs
	paths := sc.dirPaths
	if paths == nil {
		paths = fs.collectReachablePathsByInode()
	}

	groups := len(fs.groups)
	if groups == 0 {
		return nil
	}

	// Each worker returns the entries it found; nothing is emitted from inside
	// a worker, which is what keeps the callback single-threaded.
	perGroup, err := parallelMap(sc.ctx, fs.effectiveParallelism(), groups,
		func(ctx context.Context, i int) ([]DeletedEntry, error) {
			return sc.scanGroup(ctx, uint32(i), paths)
		})
	if err != nil {
		return err
	}

	for _, entries := range perGroup {
		for _, entry := range entries {
			sc.nameFor(&entry)
			if err := sc.emit(fn, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// scanGroup examines one block group's slice of the inode table.
//
// It reads and decodes but never emits, and touches no scan-wide state beyond
// the read-only path index, so it is safe to run concurrently for distinct
// groups.
func (sc *deletedScan) scanGroup(ctx context.Context, g uint32, paths map[uint32]string) ([]DeletedEntry, error) {
	fs := sc.fs
	gd := fs.groups[g]

	usable := fs.usableInodesInGroup(gd)
	if sc.opts.IncludeUninit {
		usable = fs.sb.InodesPerGroup
	}
	if usable == 0 {
		return nil, nil
	}

	var (
		found []DeletedEntry
		base  = g * fs.sb.InodesPerGroup
	)
	for i := uint32(0); i < usable; i++ {
		// Cancellation is checked per batch rather than per inode: the check is
		// cheap but not free, and a group is already a bounded unit of work.
		if i%cancellationCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}

		num := base + i + 1
		if num < fs.sb.FirstInode || num > fs.sb.InodesCount {
			continue
		}
		if sc.seen[num] {
			continue
		}

		inode, err := fs.ReadInode(num)
		if err != nil {
			continue
		}
		if !inode.Deleted() {
			continue
		}
		// A reachable inode with links is live; only trust the deletion signal
		// for something the tree no longer references.
		if _, reachable := paths[num]; reachable && inode.LinksCount > 0 {
			continue
		}

		found = append(found, sc.describe(inode, DeletedSourceInodeTable))
	}
	return found, nil
}

// scanOrphans reports inodes on the legacy chain and in the orphan file.
func (sc *deletedScan) scanOrphans(fn func(DeletedEntry) error) error {
	orphans, sources, err := sc.fs.orphanInodesWithSource()
	if err != nil {
		return err
	}
	for i, num := range orphans {
		sc.seen[num] = true

		inode, err := sc.fs.ReadInode(num)
		if err != nil {
			continue
		}
		entry := sc.describe(inode, sources[i])
		// An inode on the orphan chain stores its successor in i_dtime, so the
		// decoded deletion time is meaningless for it.
		if sources[i] == DeletedSourceOrphanList {
			entry.Times.Dtime = zeroTime
		}
		sc.nameFor(&entry)
		if err := sc.emit(fn, entry); err != nil {
			return err
		}
	}
	return nil
}

var zeroTime = Timestamps{}.Dtime

// describe builds an entry from an inode, including a recoverability judgement.
func (sc *deletedScan) describe(inode Inode, source DeletedSource) DeletedEntry {
	entry := DeletedEntry{
		Inode:  inode.Number,
		Source: source,
		Mode:   inode.Mode,
		Size:   inode.Size,
		UID:    inode.UID,
		GID:    inode.GID,
		Times:  inode.Timestamps(),
	}

	if allocated, err := sc.fs.InodeAllocated(inode.Number); err == nil {
		entry.Allocated = allocated
	}

	// ext4 zeroes the extent tree on unlink, so a failure here is the expected
	// outcome rather than an error worth reporting.
	exts, err := sc.fs.InodeExtents(inode, ExtentOptions{OmitSparse: true})
	if err != nil || len(exts) == 0 {
		entry.Recoverable = RecoveryNone
		return entry
	}

	entry.Extents = exts
	entry.Recoverable = sc.judgeRecovery(exts)
	return entry
}

// judgeRecovery reports whether a surviving block map still points at blocks
// that nothing else has claimed.
func (sc *deletedScan) judgeRecovery(exts []Extent) RecoveryConfidence {
	reused := false
	for _, e := range exts {
		if e.Sparse() || e.Inline() {
			continue
		}
		for b := uint64(0); b < e.Blocks; b++ {
			allocated, err := sc.blockAllocated(e.PhysicalBlock + b)
			if err != nil {
				return RecoveryPartial
			}
			if allocated {
				reused = true
				break
			}
		}
		if reused {
			break
		}
	}
	if reused {
		return RecoveryPartial
	}
	return RecoveryLikely
}

// blockAllocated defers to the FS-level bitmap cache, which is shared across
// goroutines and so remains correct once a scan runs its groups in parallel.
func (sc *deletedScan) blockAllocated(block uint64) (bool, error) {
	return sc.fs.BlockAllocated(block)
}
