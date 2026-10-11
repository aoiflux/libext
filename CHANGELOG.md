# Changelog

## v0.4.1

### Fixed

- **Superblock times wrapped in 2106.** `MountTime` and `WriteTime` read only
  the 32-bit low word and ignored `s_mtime_hi` and `s_wtime_hi`. They are now
  decoded as the kernel's `ext4_get_tstamp` does: the low word, unsigned, plus
  the high byte shifted above it. For example, `s_wtime` of `0x00000010` with
  `s_wtime_hi` of 1 is 2106-02-07T06:28:32Z, where v0.4.0 returned
  1970-01-01T00:00:16Z. Times with a zero high byte are unchanged.
- **A Strict directory-tail error re-read the directory's block map.** To name
  the physical block in the error, libext resolved the directory's extent map
  a second time. It now uses the map the directory was just read through. The
  error's check, inode, block and detail are unchanged.

### Added

- `Superblock.MkfsTime` and `Superblock.LastCheck`, decoded from
  `s_mkfs_time` and `s_lastcheck` with their high bytes.
- A reference image with one written extent of exactly 32768 blocks, which is
  `ee_len` `0x8000` on disk (`testdata/e2fs_bigextent.img.bz2`, built by
  `testdata/mkbigextent.sh`). The v0.4.0 fix for such extents was tested only
  on synthetic images. mke2fs `-d` stops runs at 32767 blocks, so the script
  inserts the extent with debugfs, and e2fsck passes the result. The image is
  668 bytes; the test reads it without holding the 128 MiB in memory.

### Changed

- `LibraryVersion`, recorded on every `EXTReport`, is now `v0.4.1`.

## v0.4.0

### Fixed

- **Timestamps from 2038 to 2106 were decoded 136 years too late.** When an
  inode carries epoch bits, its 32-bit base was read as unsigned and the epoch
  OR-ed above it. The kernel instead reads the base as signed and adds the
  epoch (`ext4_decode_extra_time`). So 2038-01-19T03:14:08Z, stored as
  `0x80000000` with epoch 1, decoded as 2174. Any timestamp with nonzero epoch
  bits and the base's high bit set was affected: 2038 to 2106, 2174 to 2242
  and 2310 to 2378. Dates from 1901 to 2038 were not.
- **A symlink with a 60-byte target returned its block map as the target.**
  The kernel stores a target inline only when it is 59 bytes or shorter
  (`ext4_symlink`). A 60-byte target lives in a data block, but libext read
  `i_block` instead and returned the extent header (`0x0A 0xF3...`). libext now
  decides the way the kernel's `ext4_inode_is_fast_symlink` does: a link that
  owns no blocks beyond its xattr block is fast, provided its target is shorter
  than 60 bytes.
- **A written extent of exactly 32768 blocks read back as a hole.** `ee_len`
  was split into a flag bit and a 15-bit length, so `0x8000` became "unwritten,
  0 blocks" and was dropped. The kernel writes extents of exactly that length,
  so it can affect any file with a contiguous run of 128 MiB or more on 4 KiB
  blocks. The kernel's rule is now used (`EXT_INIT_MAX_LEN`):
  - `ee_len` of `0x8000` or less is a written extent of that many blocks.
  - `0x8001` to `0xFFFF` is an unwritten extent of `ee_len - 0x8000` blocks.

### Added

- `Options.Strict`, which verifies a well-formed image and implies
  `VerifyChecksums`. It adds:
  - extent tree block and directory block checksum tails;
  - refusal of holes, unwritten runs and short maps in data being read;
  - refusal of extents past the end of the filesystem;
  - refusal of duplicate names;
  - refusal of unlinked or deleted named inodes;
  - refusal of block-mapped inodes on an extents filesystem.

  Failures are `*StrictError` values (`Check`, `Inode`, `Block`, `Detail`)
  that wrap the new `ErrStrict`. A checksum failure also wraps
  `ErrChecksumMismatch`. Behaviour is unchanged when `Strict` is off.
- Pure codecs:
  - `CRC32C`, `MetadataSeed`
  - `SuperblockChecksum`, `GroupDescChecksum`, `InodeChecksum`,
    `BitmapChecksum`, `ExtentTailChecksum`, `DirTailChecksum`
  - `EncodeTime`, `DecodeTime`, `MinTime`, `MaxTime`
  - `HasSuperBackup`, `DirRecLen`

  The checksum codecs are checked against an image written by e2fsprogs
  (`testdata/e2fs_csum.img.gz`, built by `testdata/mkcsumimg.sh`).
- Exported on-disk constants:
  - every feature bit, typed `uint32`;
  - inode flags, mode bits and directory file types;
  - extent and directory layout sizes;
  - superblock field values;
  - an offset for every field of the superblock, group descriptor and inode
    (`SbOff*`, `GdOff*`, `InodeOff*`).

  The superblock table now covers all of `struct ext4_super_block`.
- `InodeFlagEAInode` is honoured when deciding whether a symlink is fast.

### Changed

- With `INCOMPAT_CSUM_SEED` set, a stored `s_checksum_seed` of zero is now used
  as the seed, as the kernel does. Before, libext fell back to the UUID-derived
  seed.
- `LibraryVersion`, recorded on every `EXTReport`, is now `v0.4.0`.
- `Inode` documentation now states that a zero time field means both on-disk
  words were zero. Behaviour is unchanged. `DecodeTime` returns the kernel's
  reading of that case, 1970-01-01.
