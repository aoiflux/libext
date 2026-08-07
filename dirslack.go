package libext

// Directory slack recovery.
//
// Unlinking a file does not erase its directory record. The preceding record's
// rec_len is simply extended to swallow it, leaving the old record intact in
// the gap between where the live entry's name ends and where its rec_len now
// reaches. ext4 zeroes an inode's extent tree on unlink, so for many deleted
// files this slack holds the only surviving evidence that the name ever
// existed.

// DirSlackEntry is a directory record recovered from the unused tail of a live
// record, or from a record whose inode field was cleared.
type DirSlackEntry struct {
	ParentInode uint32
	Name        string
	Inode       uint32
	FileType    uint8

	// Offset is the byte position of the recovered record within the directory's
	// data stream, which is where it can be re-examined on the image.
	Offset int64

	// ShadowsLive marks a record that duplicates a live entry in the same
	// directory: same name, same inode. Rewriting a directory leaves such copies
	// behind, and they are evidence of nothing having been deleted. Callers
	// looking for deletions should skip them; callers auditing the on-disk state
	// may still want to see them.
	ShadowsLive bool
}

// ScanDirSlack recovers deleted directory records from one directory.
//
// Returned names are candidates: the record is real, but the inode it points at
// may since have been reused by an unrelated file. Cross-check Inode against the
// inode table before treating a hit as a recovered file.
func (fs *FS) ScanDirSlack(dirInode uint32) ([]DirSlackEntry, error) {
	inode, err := fs.ReadInode(dirInode)
	if err != nil {
		return nil, err
	}
	if !inode.IsDirectory {
		return nil, ErrNotDirectory
	}
	if inode.HasInline {
		// Inline directories use a different layout with no slack to mine.
		return nil, nil
	}

	data, err := fs.readInodeData(inode)
	if err != nil {
		return nil, err
	}

	blockSize := int(fs.sb.BlockSize)
	if blockSize <= 0 {
		return nil, ErrUnsupportedLayout
	}

	var out []DirSlackEntry
	// Records never straddle a block boundary, so each block is scanned on its
	// own; that also stops damage in one block from derailing the rest.
	for start := 0; start < len(data); start += blockSize {
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		out = append(out, fs.scanDirBlockSlack(dirInode, data[start:end], int64(start))...)
	}

	fs.markShadowedSlack(data, out)
	return out, nil
}

// markShadowedSlack flags recovered records that merely duplicate a live entry.
func (fs *FS) markShadowedSlack(data []byte, slack []DirSlackEntry) {
	if len(slack) == 0 {
		return
	}
	live, err := fs.parseDirEntries(data)
	if err != nil && len(live) == 0 {
		return
	}

	type key struct {
		inode uint32
		name  string
	}
	seen := make(map[key]bool, len(live))
	for _, e := range live {
		seen[key{e.Inode, e.Name}] = true
	}
	for i := range slack {
		if seen[key{slack[i].Inode, slack[i].Name}] {
			slack[i].ShadowsLive = true
		}
	}
}

// scanDirBlockSlack mines one directory block.
func (fs *FS) scanDirBlockSlack(dirInode uint32, block []byte, blockOffset int64) []DirSlackEntry {
	var out []DirSlackEntry

	for off := 0; off+8 <= len(block); {
		recLen := int(le16(block, off+4))
		if recLen < 8 || recLen%dirEntryAlign != 0 || off+recLen > len(block) {
			break
		}

		nameLen := int(block[off+6])
		if (fs.sb.FeatureIncompat & featureIncompatFileType) == 0 {
			nameLen = int(le16(block, off+6))
		}

		// The live entry occupies its header plus its name, padded to alignment.
		used := 8 + nameLen
		if used > recLen {
			used = recLen
		}
		used = (used + dirEntryAlign - 1) &^ (dirEntryAlign - 1)

		// A record whose inode field is zero is itself a deletion: the name may
		// still be sitting in place.
		if le32(block, off) == 0 && nameLen > 0 {
			if e, ok := fs.recoverSlackRecord(dirInode, block, off, recLen, blockOffset); ok {
				out = append(out, e.DirSlackEntry)
			}
		}

		for probe := off + used; probe+8 <= off+recLen; probe += dirEntryAlign {
			if e, ok := fs.recoverSlackRecord(dirInode, block, probe, off+recLen-probe, blockOffset); ok {
				out = append(out, e.DirSlackEntry)
				// Skip past the record just recovered rather than re-reading its
				// name bytes as another candidate.
				if adv := int(e.RecLenHint); adv >= dirEntryAlign {
					probe += adv - dirEntryAlign
				}
			}
		}

		off += recLen
	}
	return out
}

// RecLenHint carries the recovered record's own length so the scanner can step
// over it. It is not part of the public entry.
type slackHint = uint16

// recoverSlackRecord validates a candidate directory record at off.
func (fs *FS) recoverSlackRecord(dirInode uint32, block []byte, off, limit int, blockOffset int64) (dirSlackCandidate, bool) {
	if off+8 > len(block) || limit < 8 {
		return dirSlackCandidate{}, false
	}

	inode := le32(block, off)
	recLen := int(le16(block, off+4))
	nameLen := int(block[off+6])
	fileType := block[off+7]
	if (fs.sb.FeatureIncompat & featureIncompatFileType) == 0 {
		nameLen = int(le16(block, off+6))
		fileType = 0
	}

	if inode == 0 || inode > fs.sb.InodesCount {
		return dirSlackCandidate{}, false
	}
	if nameLen == 0 || 8+nameLen > limit || off+8+nameLen > len(block) {
		return dirSlackCandidate{}, false
	}
	// A stale record keeps a sane length; garbage rarely does.
	if recLen < 8+nameLen || recLen%dirEntryAlign != 0 || recLen > limit {
		return dirSlackCandidate{}, false
	}
	if fileType > dirEntryFileTypeMax {
		return dirSlackCandidate{}, false
	}

	name := string(block[off+8 : off+8+nameLen])
	if !plausibleFileName(name) {
		return dirSlackCandidate{}, false
	}

	return dirSlackCandidate{
		DirSlackEntry: DirSlackEntry{
			ParentInode: dirInode,
			Name:        name,
			Inode:       inode,
			FileType:    fileType,
			Offset:      blockOffset + int64(off),
		},
		RecLenHint: uint16(recLen),
	}, true
}

// dirSlackCandidate is a recovered record plus the length used to step over it.
type dirSlackCandidate struct {
	DirSlackEntry
	RecLenHint slackHint
}

// plausibleFileName rejects byte runs that cannot be a directory entry name.
// Slack is full of file data and stale metadata, so without this the scan
// reports noise as recovered filenames.
func plausibleFileName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == 0 || c == '/' {
			return false
		}
		// Control characters do not appear in names written by any normal tool.
		if c < asciiSpace || c == asciiDelete {
			return false
		}
	}
	return true
}
