#!/bin/bash
#
# Build the ext corpus used for cross-validation against e2fsprogs.
#
# The images are not checked in: they are reproducible from this script, and
# regenerating is cheaper than storing them. Every image gets a fixed UUID and a
# fixed hash seed so that repeated runs produce identical bytes, which is what
# makes a diff against a previous run meaningful.
#
# Requires: mke2fs, debugfs, dumpe2fs (e2fsprogs). No root or mount needed —
# mke2fs -d populates from a directory and debugfs edits the image in place.
#
# Usage:
#   ./mkcorpus.sh [output-dir]        # default: ./corpus
#
# The companion oracle commands, for validating a parser against these images:
#   debugfs -R "stat <N>"          img   # extents, crtime, extra isize
#   debugfs -R "blocks <N>"        img   # data + indirect blocks
#   debugfs -R "dump <N> out.bin"  img   # byte-exact extraction
#   debugfs -R "ls -d /dir"        img   # deleted directory records
#   debugfs -R "lsdel"             img   # deleted inodes
#   debugfs -R "logdump -O"        img   # journal transactions
#   debugfs -R "ea_list /file"     img   # extended attributes
#   dumpe2fs -h                    img   # geometry and features

set -euo pipefail

OUT="${1:-./corpus}"
mkdir -p "$OUT"
cd "$OUT"

export SOURCE_DATE_EPOCH=1700000000

for tool in mke2fs debugfs dumpe2fs; do
	command -v "$tool" >/dev/null || { echo "missing $tool (install e2fsprogs)" >&2; exit 1; }
done

# tree_basic populates a directory with content that exercises the block mapper:
# a contiguous file, a sparse file, a fast symlink and a nested directory.
tree_basic() {
	local dir=$1
	rm -rf "$dir" && mkdir -p "$dir/sub"
	head -c 100000 /dev/urandom > "$dir/contig.bin"
	head -c 300000 /dev/urandom > "$dir/big.bin"
	dd if=/dev/urandom of="$dir/sparse.bin" bs=1024 count=2 status=none
	dd if=/dev/urandom of="$dir/sparse.bin" bs=1024 seek=20 count=2 conv=notrunc status=none
	echo hello > "$dir/sub/small.txt"
	ln -sf /etc/passwd "$dir/fastlink"
}

build() {
	local name=$1 uuid=$2 blocks=$3
	shift 3
	echo "  $name"
	rm -f "$name"
	mke2fs -q -F -U "$uuid" -E hash_seed=0f7b1c3d-5a2e-4b6f-8c9d-0e1f2a3b4c5d \
		"$@" "$name" "$blocks" >/dev/null 2>&1 || true
	# Some feature combinations need a larger filesystem than asked for. Leaving
	# a truncated image behind would look like a parser failure later, so drop it.
	if [ ! -s "$name" ]; then
		rm -f "$name"
		echo "    (skipped: mke2fs could not build this combination)"
	fi
}

echo "building corpus in $PWD"

# --- ext2: classic block maps, including double indirection ------------------
tree_basic t
head -c 1000000 /dev/urandom > t/double-indirect.bin   # spans direct+ind+dind
build ext2-1k.img 22222222-3333-4444-5555-666666666666 8192 -t ext2 -b 1024 -I 128 -d t

# --- ext4: extent trees, 64bit, metadata_csum --------------------------------
tree_basic t
build ext4-1k.img 11111111-2222-3333-4444-555555555555 16384 -t ext4 -b 1024 -I 256 -d t
build ext4-4k.img 66666666-7777-8888-9999-aaaaaaaaaaaa 16384 -t ext4 -b 4096 -I 256 -d t

# --- ext4 with an extent tree deep enough to need an index block -------------
rm -rf t && mkdir t
# Interleaved writes fragment the file, forcing more extents than fit inline.
for i in $(seq 1 60); do head -c 40000 /dev/urandom > "t/frag$i.bin"; done
build ext4-frag.img 77777777-8888-9999-aaaa-bbbbbbbbbbbb 16384 -t ext4 -b 1024 -I 256 -d t

# --- inline data: small files and directories stored inside the inode --------
rm -rf t && mkdir -p t/sub
echo -n 'tiny inline content' > t/small.txt
printf 'x%.0s' $(seq 1 55) > t/fits60.txt
head -c 5000 /dev/urandom > t/big.bin
echo hi > t/sub/nested.txt
build inline.img 44444444-5555-6666-7777-888888888888 8192 -t ext4 -O inline_data -b 1024 -I 256 -d t

# --- deletions: names in directory slack, inodes with a deletion time --------
rm -rf t && mkdir -p t/sub
for i in 1 2 3 4 5 6; do head -c 40000 /dev/urandom > "t/file$i.bin"; done
echo keepme > t/sub/keep.txt
build del.img 33333333-4444-5555-6666-777777777777 16384 -t ext4 -b 1024 -I 256 -d t
for f in file2.bin file4.bin file6.bin; do
	debugfs -w -R "rm /$f" del.img >/dev/null 2>&1
done

# --- extended attributes, including one large enough to need a block ---------
rm -rf t && mkdir t
echo hello > t/f1.txt
echo world > t/f2.txt
build xattr.img 55555555-6666-7777-8888-999999999999 8192 -t ext4 -b 1024 -I 256 -d t
debugfs -w -R 'ea_set /f1.txt security.selinux system_u:object_r:etc_t:s0' xattr.img >/dev/null 2>&1
debugfs -w -R 'ea_set /f1.txt user.comment hello-attr' xattr.img >/dev/null 2>&1
debugfs -w -R "ea_set /f2.txt user.big $(printf 'A%.0s' $(seq 1 64))" xattr.img >/dev/null 2>&1

# --- features that must be refused rather than answered wrongly --------------
rm -rf t && mkdir t && echo x > t/f
build bigalloc.img 88888888-9999-aaaa-bbbb-cccccccccccc 32768 -t ext4 -O bigalloc -C 16384 -b 1024 -I 256 -d t
build metabg.img 99999999-aaaa-bbbb-cccc-dddddddddddd 262144 -t ext4 -O meta_bg -b 1024 -I 256 -d t

rm -rf t

echo
echo "corpus:"
for img in *.img; do
	printf '  %-18s %s\n' "$img" "$(dumpe2fs -h "$img" 2>/dev/null | grep -i '^Filesystem features' | cut -c27-)"
done
