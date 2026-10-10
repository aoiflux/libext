#!/bin/bash
#
# Build the metadata_csum reference image the codec tests check against.
#
# The point is a fixed image whose every checksum was written by e2fsprogs, so
# agreeing with it is agreement with the reference implementation rather than
# with this package's own reading of the format. The tree is chosen to put one
# of each checksummed structure on disk:
#
#   - 8 block groups at 1 KiB blocks, so group descriptors past group 0 and
#     superblock backups (groups 1, 3, 5 and 7) exist
#   - 64bit, so descriptors are 64 bytes and bitmap checksums are 32 bits
#   - a sparse file of six separated data runs: more extents than the inode's
#     four slots hold, which forces an extent tree block and its tail
#   - a directory of 60 entries, which spans more than one block
#   - symlinks with 59- and 60-byte targets, either side of the inline limit
#
# Usage:
#   ./mkcsumimg.sh            # writes e2fs_csum.img.gz beside this script
#
# The image is committed; rerun only to change it, and update the expected
# values in codec_realimage_test.go from the dumpe2fs output this prints.

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

command -v mke2fs >/dev/null || { echo "missing mke2fs" >&2; exit 1; }

# Fixed times make the image reproducible: mke2fs -d copies each source file's
# timestamps, and E2FSPROGS_FAKE_TIME pins the ones mke2fs invents itself.
export E2FSPROGS_FAKE_TIME=1700000000
STAMP=@1700000000

T="$WORK/tree"
mkdir -p "$T/dir" "$T/many"
printf 'hello, ext4\n' >"$T/hello.txt"
: >"$T/empty"
head -c 70000 /dev/zero | tr '\0' 'x' >"$T/dir/contig.bin"
for i in $(seq -w 1 60); do
	printf 'entry %s\n' "$i" >"$T/many/file-with-a-longish-name-$i"
done

# Six 1 KiB data runs at 8 KiB strides; the gaps stay holes.
for i in 0 1 2 3 4 5; do
	head -c 1024 /dev/zero | tr '\0' "$((i + 1))" |
		dd of="$T/sparse.bin" bs=1024 seek=$((i * 8)) conv=notrunc status=none
done

ln -s "$(printf 'a%.0s' $(seq 1 59))" "$T/link59"
ln -s "$(printf 'b%.0s' $(seq 1 60))" "$T/link60"

find "$T" -exec touch -h -d "$STAMP" {} +

IMG="$WORK/e2fs_csum.img"
mke2fs -q -F -t ext4 -b 1024 -g 1024 -N 256 -I 256 \
	-O metadata_csum,64bit,^has_journal,^resize_inode \
	-U 11111111-2222-3333-4444-555555555555 \
	-E hash_seed=66666666-7777-8888-9999-aaaaaaaaaaaa,root_owner=0:0 \
	-L libext-csum -d "$T" "$IMG" 8M

e2fsck -fn "$IMG" >/dev/null
dumpe2fs "$IMG" 2>/dev/null | grep -E '^(Group|  Primary|  Backup|Default directory hash|Minimum extra|Desired extra|Checksum type|Filesystem flags|Flex block group|Filesystem created)'
for p in /hello.txt /sparse.bin /link59 /link60 /many /dir/contig.bin; do
	debugfs -R "stat $p" "$IMG" 2>/dev/null | sed -n '1p;/Generation/p;/EXTENTS/,+3p'
done

gzip -9 -n -c "$IMG" >"$HERE/e2fs_csum.img.gz"
ls -l "$HERE/e2fs_csum.img.gz"
