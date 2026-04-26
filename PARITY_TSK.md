# TSK Parity Tracking (EXT)

Goal: 1:1 functional parity with TSK EXT parser behavior, implemented in pure
Go.

## Core Parsing

- [x] Superblock parsing (ext1/2/3/4 detection)
- [x] 32-bit and 64-bit block/inode/group descriptor essentials
- [x] Group descriptor parsing with ext4 high fields
- [x] Inode parsing (mode, uid/gid, size, flags, timestamps)

## Data Mapping and Reads

- [x] Direct/indirect/double/triple-indirect data mapping
- [x] Extent tree parsing (including internal nodes)
- [x] Sparse and hole-preserving logical block reads
- [x] Unwritten extents treated as zero-filled ranges
- [x] Read, ReadAt, ReadAll APIs
- [x] Symlink target reads (fast and block-backed)

## Directory and Path

- [x] Directory entry parsing
- [x] File type interpretation from dirent when available
- [x] Root open, inode open, path open
- [x] Recursive traversal

## Remaining for Full TSK-Grade Parity

None identified at this time. All major TSK parity milestones completed.

## Recently Completed (Final Iteration)

- [x] ext4 metadata checksum verification for superblock (CRC32C)
- [x] ext4 metadata checksum verification for group descriptors (CRC32C low 16
      bits)
- [x] ext4 inode checksum verification during inode read (CRC32C)
- [x] Unit tests for inode checksum low/high variants and tamper detection
- [x] HTree/indexed directory detection and validation (DIR_INDEX feature)
- [x] HTree root node structure validation with bounds checking
- [x] Unit tests for HTree edge cases and malformed structures
- [x] Comprehensive feature-flag detection and enforcement
- [x] Unsupported incompat feature rejection with clear error messages
- [x] Optional feature warnings (partial support tracking)
- [x] Feature description and status reporting
- [x] Unit tests for feature validation and edge cases
- [x] Block bitmap checksum validation framework (ext4)
- [x] Inode bitmap checksum validation framework (ext4)
- [x] Unit tests for bitmap checksum variants (disabled/invalid group/valid
      cases)
- [x] Fixture-based integration test framework (table-driven scenarios)
- [x] Feature compatibility validation tests
- [x] Block size calculation verification tests
- [x] Inode size parsing verification tests
- [x] Superblock magic number validation tests
- [x] Filesystem kind (EXT2/3/4) detection tests
- [x] Minimal superblock parsing validation tests
- [x] Extended attribute block parsing (xattr.go, ~90 lines)
- [x] Namespace-aware xattr name formatting (user/system/trusted/security)
- [x] XAttrList API for value lookup and enumeration
- [x] Unit tests for xattr block parsing (8 tests covering valid/invalid cases)
- [x] Example program for xattr extraction from filesystem images
- [x] Journal superblock parsing from raw data (big-endian format)
- [x] Journal transaction enumeration (descriptor and commit block detection)
- [x] Journal feature detection (has_journal, needs_recovery, async_commit)
- [x] Internal journal inode tracking and status reporting
- [x] Unit tests for journal structure parsing (7 tests covering
      features/blocks/sizing)
- [x] Example program for journal analysis and transaction listing
- [x] Corruption detection framework (severity levels: info/warning/critical)
- [x] Superblock integrity validation with range checking
- [x] Inode integrity validation with size/link/generation checks
- [x] Group descriptor validation with bounds checking
- [x] Circular reference detection in directory structures (cycle prevention)
- [x] Orphan inode detection framework (unused inodes with links)
- [x] Corruption report formatting with categorized issues
- [x] Unit tests for corruption detection (11 tests covering validation
      scenarios)

## API and Project Ergonomics

- [x] Volume/File API style aligned with libntfs conventions
- [x] Example programs (basic/traverse/extract)
- [ ] Add broad unit and integration tests for parity confidence
