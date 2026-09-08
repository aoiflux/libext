// Naming inodes.
//
// path.go answers the question the rest of the library keeps raising and could
// not previously answer: given an inode number, what is it called? The journal
// yields inode and block numbers and never names; so does the inode table. A
// finding reported as "inode 8213 changed" is not usable evidence, and turning
// it into "/var/log/auth.log changed" is what this file is for.
//
// The direction of the ext filesystem is what makes this awkward. A directory
// can be named cheaply, by following ".." upward to the root. Nothing else can:
// a file's inode carries no reference back to the directories naming it, so the
// only way to name one is to walk the tree downward until a record pointing at
// it turns up. PathIndex pays for that walk once and answers from a map
// afterwards; (*FS).PathFor is the single-shot convenience, and says plainly in
// its own documentation what it costs.
package libext

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
)

// errWalkStop unwinds an index build once the inode it was looking for has been
// named. It never escapes buildPathIndex.
var errWalkStop = errors.New("walk complete")

// PathIndex is a snapshot of the reverse mapping from inode number to path,
// produced by one walk of the directory tree.
//
// Build it once and query it many times; that is the whole point. Each query is
// a map lookup, while each (*FS).PathFor for a non-directory costs a walk.
// Journal and inode-table analysis both yield inode numbers in bulk, which is
// the case this exists for.
//
// It is safe for concurrent use once built: nothing writes to it after
// BuildPathIndex returns. It is deliberately not cached on the FS. An index
// over a multi-million-inode volume is large, and holding one for the lifetime
// of the FS - built invisibly by whichever call happened to need it first -
// would put unbounded, caller-invisible state behind a read-only handle. Owning
// it explicitly means the cost is visible, cancellable, and releasable.
//
// It names what the live tree reaches, and nothing else. An unlinked inode has
// no record pointing at it and so has no path here; whatever survives of its
// last name comes from ScanDirSlack or DeletedEntry.Path instead.
type PathIndex struct {
	// paths maps an inode to the first path the walk reached it by. The whole
	// path is stored per inode rather than reconstructed from parents on
	// demand: the report's deep scan names every inode in the table, and
	// rebuilding each path from the tree would make that O(inodes x depth)
	// instead of O(inodes).
	paths map[uint32]string

	// parents maps an inode to the directory holding the link recorded in
	// paths. The walk already holds that directory, so recording it is free.
	parents map[uint32]uint32

	// dirs is the subset of paths belonging to directories - the subset the
	// walk had to identify anyway in order to descend. Recording it saves the
	// deleted scan a ReadInode for every reachable inode just to ask which of
	// them are directories.
	dirs map[uint32]string

	// extra holds the second and further links to an inode. Nearly every inode
	// has exactly one, so this stays empty on an ordinary filesystem.
	extra map[uint32][]pathLink

	// truncated records that the walk stopped short: a directory it could not
	// read, a cycle, or the depth cap. A miss then means "this walk did not
	// reach it" rather than "the tree does not contain it"; Warnings says which.
	truncated bool
}

// pathLink is one further place an inode is linked from.
type pathLink struct {
	parent uint32
	path   string
}

// BuildPathIndex walks the directory tree and returns the inode-to-path mapping
// it reaches.
//
// This is the way to name inodes in bulk. One walk answers any number of
// subsequent queries, where (*FS).PathFor would walk again for every
// non-directory it is asked about.
//
// The index belongs to the caller, deliberately. Caching one on the FS would
// make the first PathFor of a volume's lifetime silently pay for a full walk,
// with no context to cancel it and no way to release the map afterwards - over
// a multi-million-inode volume that is a large allocation pinned for as long as
// the FS is open, which is more than this package's concurrency contract offers
// to hold on a caller's behalf.
//
// There is likewise no BuildPathIndexFrom(startInode). "Reachable from the
// root" is what gives ErrPathNotFound its meaning: an inode absent from an
// index rooted at the root is genuinely unreferenced by the live tree, while
// one absent from a subtree index is merely elsewhere. A caller wanting a
// subtree can walk it with WalkDir.
func (fs *FS) BuildPathIndex() (*PathIndex, error) {
	return fs.BuildPathIndexContext(context.Background())
}

// BuildPathIndexContext is BuildPathIndex with cancellation.
func (fs *FS) BuildPathIndexContext(ctx context.Context) (*PathIndex, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	return fs.buildPathIndex(ctx, 0)
}

// PathFor returns the path recorded for an inode.
//
// It returns a path, not the path: an inode with several links is recorded
// under the first the walk reached, which - because the walk is breadth-first -
// is the shallowest. PathsFor returns all of them.
func (p *PathIndex) PathFor(inodeNum uint32) (string, error) {
	if s, ok := p.paths[inodeNum]; ok {
		return s, nil
	}
	return "", notReachable(inodeNum, p.truncated)
}

// PathsFor returns every path the walk found naming an inode, shallowest first.
//
// It returns nil for an inode the walk did not reach. A directory has exactly
// one path in a valid filesystem, so more than one for a directory is itself
// evidence of a damaged tree.
func (p *PathIndex) PathsFor(inodeNum uint32) []string {
	first, ok := p.paths[inodeNum]
	if !ok {
		return nil
	}
	rest := p.extra[inodeNum]
	out := make([]string, 0, 1+len(rest))
	out = append(out, first)
	for _, l := range rest {
		out = append(out, l.path)
	}
	return out
}

// ParentOf returns the directory holding the link recorded in PathFor, and
// whether the inode was reached at all.
//
// The root is its own parent.
func (p *PathIndex) ParentOf(inodeNum uint32) (uint32, bool) {
	parent, ok := p.parents[inodeNum]
	return parent, ok
}

// Len is the number of inodes the index names.
func (p *PathIndex) Len() int { return len(p.paths) }

// Truncated reports whether the walk stopped short of the whole tree, because a
// directory could not be read, a cycle was found, or the depth cap was hit.
//
// When it is true, an inode missing from the index may still be linked
// somewhere the walk could not reach. Warnings says where the walk gave up.
func (p *PathIndex) Truncated() bool { return p.truncated }

// PathFor returns a path naming inodeNum in the live directory tree.
//
// For a directory this follows ".." upward to the root, which costs a couple of
// directory reads per level and nothing else. For anything else there is no
// upward link to follow - a file's inode carries no reference to the
// directories naming it - so the tree is walked downward until a record
// pointing at the inode is found.
//
// That downward walk happens on every call. Naming more than a handful of
// non-directory inodes should go through BuildPathIndex, which pays for the
// walk once: a journal analysis produces inode numbers by the thousand, and
// this method in that loop is one whole-filesystem walk per inode.
//
// It returns ErrPathNotFound when nothing in the live tree references the
// inode, which is the ordinary answer for an unlinked or orphaned one, and
// ErrInvalidInode for a number outside the filesystem.
func (fs *FS) PathFor(inodeNum uint32) (string, error) {
	return fs.PathForContext(context.Background(), inodeNum)
}

// PathForContext is PathFor with cancellation.
func (fs *FS) PathForContext(ctx context.Context, inodeNum uint32) (string, error) {
	if ctx == nil {
		return "", errors.New("context is nil")
	}
	// Checked up front because the directory case below is an upward walk with
	// no loop long enough to carry a paced check; without this, a cancelled
	// context would still be answered.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if inodeNum == 0 || inodeNum > fs.sb.InodesCount {
		return "", fmt.Errorf("%w: %d", ErrInvalidInode, inodeNum)
	}
	if inodeNum == RootInode {
		return "/", nil
	}

	inode, err := fs.ReadInode(inodeNum)
	if err != nil {
		return "", err
	}
	if inode.IsDirectory {
		return fs.pathForDir(inodeNum)
	}

	// No upward link exists for a non-directory, so the tree has to be walked.
	// stopAt ends that walk as soon as the inode is named, which on average
	// halves it.
	ix, err := fs.buildPathIndex(ctx, inodeNum)
	if err != nil {
		return "", err
	}
	return ix.PathFor(inodeNum)
}

// pathForDir names a directory by following ".." to the root.
//
// Two listings per level: one of the directory to read its "..", and one of
// that parent to find the directory's own record and so its name.
func (fs *FS) pathForDir(inodeNum uint32) (string, error) {
	var parts []string
	seen := map[uint32]bool{inodeNum: true}
	cur := inodeNum

	for depth := 0; cur != RootInode; depth++ {
		if depth > maxWalkDepth {
			return "", fmt.Errorf("%w: directory nesting above inode %d exceeds %d levels",
				ErrUnsupportedLayout, inodeNum, maxWalkDepth)
		}
		parent, err := fs.parentOfDir(cur)
		if err != nil {
			return "", err
		}

		// A ".." chain that revisits a directory never reaches the root. This is
		// checked before the name is looked up so that a cycle is reported as a
		// cycle: a self-referential ".." would otherwise fail first in nameInDir,
		// as a directory not named in its own parent, which describes the symptom
		// rather than the fault.
		if seen[parent] {
			return "", fmt.Errorf("%w: parent chain above inode %d revisits inode %d",
				ErrUnsupportedLayout, inodeNum, parent)
		}
		seen[parent] = true

		name, err := fs.nameInDir(parent, cur)
		if err != nil {
			return "", err
		}
		parts = append(parts, name)
		cur = parent
	}

	// Collected leaf-first; the path reads root-first.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + path.Join(parts...), nil
}

// parentOfDir reads a directory's ".." record.
func (fs *FS) parentOfDir(inodeNum uint32) (uint32, error) {
	entries, err := fs.ListDir(inodeNum)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.Name == ".." {
			return e.Inode, nil
		}
	}
	return 0, fmt.Errorf("%w: directory inode %d has no parent record", ErrUnsupportedLayout, inodeNum)
}

// nameInDir finds the name under which parent links child.
func (fs *FS) nameInDir(parent, child uint32) (string, error) {
	entries, err := fs.ListDir(parent)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		if e.Inode == child {
			return e.Name, nil
		}
	}
	return "", fmt.Errorf("%w: inode %d is not named in its parent %d", ErrPathNotFound, child, parent)
}

// buildPathIndex walks the reachable directory tree breadth-first from the root.
//
// Breadth-first rather than depth-first so that the path recorded for an inode
// with several links is the shallowest one, which is the one a person would
// name it by.
//
// stopAt, when non-zero, ends the walk as soon as that inode is named. It is
// what makes a single (*FS).PathFor cost half a walk on average rather than a
// whole one; a zero stopAt indexes the entire tree.
func (fs *FS) buildPathIndex(ctx context.Context, stopAt uint32) (*PathIndex, error) {
	ix := &PathIndex{
		paths:   map[uint32]string{RootInode: "/"},
		parents: map[uint32]uint32{RootInode: RootInode},
		dirs:    map[uint32]string{RootInode: "/"},
		extra:   map[uint32][]pathLink{},
	}

	type dirItem struct {
		inode uint32
		p     string
		depth int
	}
	queue := []dirItem{{inode: RootInode, p: "/"}}
	seenDirs := map[uint32]bool{RootInode: true}
	visited := 0

	err := func() error {
		for len(queue) > 0 {
			item := queue[0]
			queue = queue[1:]

			if item.depth > maxWalkDepth {
				ix.truncated = true
				fs.warn(WarnDegradedRead, "", fmt.Sprintf(
					"directory nesting at %s exceeds %d levels; not descending further", item.p, maxWalkDepth))
				continue
			}

			entries, err := fs.ListDir(item.inode)
			if err != nil {
				// One unreadable directory must not discard the whole index, but
				// it does mean a miss is no longer proof of absence.
				ix.truncated = true
				fs.warn(WarnDegradedRead, "", fmt.Sprintf(
					"directory %s is unreadable (%v); paths beneath it are not indexed", item.p, err))
				continue
			}

			for _, e := range entries {
				if e.Name == "." || e.Name == ".." {
					continue
				}

				// Paced as in dirWalk.descend, and checked before the counter is
				// incremented so an already-cancelled context is caught on the
				// first entry rather than the thousandth.
				if visited%cancellationCheckInterval == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				visited++

				childPath := path.Join(item.p, e.Name)
				if _, ok := ix.paths[e.Inode]; !ok {
					ix.paths[e.Inode] = childPath
					ix.parents[e.Inode] = item.inode
				} else {
					ix.extra[e.Inode] = append(ix.extra[e.Inode], pathLink{parent: item.inode, path: childPath})
				}
				if stopAt != 0 && e.Inode == stopAt {
					return errWalkStop
				}

				child, err := fs.ReadInode(e.Inode)
				if err != nil || !child.IsDirectory {
					continue
				}
				if seenDirs[e.Inode] {
					// A directory has one parent in a valid filesystem; a second
					// arrival is a crafted or damaged tree. Recorded in extra by the
					// block above, but dirs is left alone: it has to keep agreeing
					// with paths about which path names this directory.
					ix.truncated = true
					fs.warn(WarnDegradedRead, "", fmt.Sprintf(
						"directory inode %d is reachable more than once (at %s); not descending again",
						e.Inode, childPath))
					continue
				}
				seenDirs[e.Inode] = true
				ix.dirs[e.Inode] = childPath
				queue = append(queue, dirItem{inode: e.Inode, p: childPath, depth: item.depth + 1})
			}
		}
		return nil
	}()

	if err != nil && !errors.Is(err, errWalkStop) {
		return nil, err
	}
	return ix, nil
}

// sortedDirs returns the indexed directory inodes in ascending order.
//
// Callers that iterate directories and keep the first answer for an inode must
// not iterate the map directly: Go randomises map order, so which of two
// directories won would differ between runs of the same image.
func (p *PathIndex) sortedDirs() []uint32 {
	nums := make([]uint32, 0, len(p.dirs))
	for num := range p.dirs {
		nums = append(nums, num)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	return nums
}

// notReachable is the shared "no path" error, which says whether the walk was
// complete enough for the answer to be conclusive.
func notReachable(inodeNum uint32, truncated bool) error {
	if truncated {
		return fmt.Errorf("%w: inode %d was not reached (the walk was truncated; see Warnings)",
			ErrPathNotFound, inodeNum)
	}
	return fmt.Errorf("%w: inode %d is not reachable from the root", ErrPathNotFound, inodeNum)
}
