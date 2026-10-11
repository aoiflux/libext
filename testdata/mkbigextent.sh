#!/bin/bash
#
# Build the reference image holding one written extent of exactly 32768
# blocks, the longest the kernel writes (EXT_INIT_MAX_LEN), whose ee_len is
# 0x8000.
#
# mke2fs -d and debugfs "write" stop each run at 32767 blocks, so the extent is
# inserted with debugfs's extent commands instead. Every structure is still
# written and checksummed by e2fsprogs, and e2fsck passes the result:
#
#   - 4 KiB blocks, so 32768 blocks is a 128 MiB file
#   - sparse_super2 with no backups and flex_bg metadata in group 0, so the run
#     can cross into group 1 without meeting a backup superblock
#   - the file's first block filled with 0xA5 and its last with 0x5A; the
#     blocks between are zero, which keeps the compressed image under 1 KiB
#
# Usage:
#   ./mkbigextent.sh          # writes e2fs_bigextent.img.bz2 beside this script
#
# The image is committed; rerun only to change it.

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

for c in mke2fs debugfs e2fsck bzip2; do
	command -v "$c" >/dev/null || { echo "missing $c" >&2; exit 1; }
done

# Fixed times make the image reproducible. e2fsck stamps s_wtime and
# s_lastcheck from its own clock, which only E2FSCK_TIME pins.
export E2FSPROGS_FAKE_TIME=1700000000
export E2FSCK_TIME=1700000000

IMG="$WORK/e2fs_bigextent.img"
FIRST=64           # physical block of the file's first block
LEN=32768          # ee_len 0x8000
LAST=$((FIRST + LEN - 1))

mke2fs -q -F -t ext4 -b 4096 -N 64 -I 256 \
	-O metadata_csum,64bit,^has_journal,^resize_inode,sparse_super2 \
	-E num_backup_sb=0,root_owner=0:0,hash_seed=66666666-7777-8888-9999-aaaaaaaaaaaa \
	-U 11111111-2222-3333-4444-555555555555 \
	-L libext-big "$IMG" $((LAST + 9))

debugfs -w -f - "$IMG" >/dev/null 2>&1 <<EOF
write /dev/null big.bin
sif big.bin mode 0100644
sif big.bin size $((LEN * 4096))
setb $FIRST $LEN
eo big.bin
insert_node 0 $LEN $FIRST
ec
sif big.bin blocks $((LEN * 8))
EOF

# setb leaves the free-block counts stale; e2fsck -y recomputes them and exits
# 1 for "fixed". The second run must find nothing.
e2fsck -fy "$IMG" >/dev/null 2>&1 || [ $? -eq 1 ]
e2fsck -fn "$IMG" >/dev/null

head -c 4096 /dev/zero | tr '\0' '\245' | dd of="$IMG" bs=4096 seek=$FIRST conv=notrunc status=none
head -c 4096 /dev/zero | tr '\0' '\132' | dd of="$IMG" bs=4096 seek=$LAST conv=notrunc status=none

debugfs -R "ex big.bin" "$IMG" 2>/dev/null

bzip2 -9 -c "$IMG" >"$HERE/e2fs_bigextent.img.bz2"
ls -l "$HERE/e2fs_bigextent.img.bz2"
