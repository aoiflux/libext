#!/bin/bash
#
# Capture e2fsprogs' answers for a corpus image, in a form a Go test can diff
# against.
#
# The point is to make cross-validation repeatable. An ad-hoc comparison run
# once proves the code was right that afternoon; a committed oracle proves it
# still is, and fails the build when it stops being.
#
# Usage:
#   ./mkoracle.sh <image> > image.oracle
#
# Output format — one record per line, sorted, stable across runs:
#   geom  <blocksize> <blockcount> <inodecount> <inodesize> <groups>
#   inode <n> size=<bytes> links=<n> mtime=<sec>.<nsec> crtime=<sec>.<nsec>
#   ext   <n> <logical_start>-<logical_end>:<physical_start>-<physical_end>
#   blocks <n> <count>             # data + indirect blocks, as debugfs counts them
#   del   <n>                      # inode with a deletion time
#   slack <parent> <inode> <name>  # deleted directory record
#   xattr <n> <name>=<value>
#   jrnl  <sequence> <type> <block>

set -uo pipefail

IMG="${1:?usage: mkoracle.sh <image>}"
command -v debugfs >/dev/null || { echo "missing debugfs" >&2; exit 1; }

df() { debugfs -R "$*" "$IMG" 2>/dev/null; }

# --- geometry ----------------------------------------------------------------
dumpe2fs -h "$IMG" 2>/dev/null | awk '
/^Block size:/       { bs=$3 }
/^Block count:/      { bc=$3 }
/^Inode count:/      { ic=$3 }
/^Inode size:/       { is=$3 }
END { printf "geom %s %s %s %s\n", bs, bc, ic, is }'

# The inode range worth walking: everything the filesystem can address, capped
# so a large corpus image does not take minutes.
MAXINO=$(dumpe2fs -h "$IMG" 2>/dev/null | awk '/^Inode count:/ {print $3}')
[ -z "$MAXINO" ] && MAXINO=0
[ "$MAXINO" -gt 512 ] && MAXINO=512

# --- per-inode facts ---------------------------------------------------------
for ino in $(seq 2 "$MAXINO"); do
	stat=$(df "stat <$ino>")
	[ -z "$stat" ] && continue
	# Skip inodes that were never allocated.
	echo "$stat" | grep -q '^Inode: ' || continue
	mode=$(echo "$stat" | sed -n 's/^Inode: [0-9]*   Type: \([a-z]*\).*/\1/p')
	[ "$mode" = "bad type" ] && continue

	size=$(echo "$stat" | sed -n 's/.*Size: \([0-9]*\).*/\1/p' | head -1)
	links=$(echo "$stat" | sed -n 's/^Links: \([0-9]*\).*/\1/p' | head -1)
	[ -z "$size" ] && continue

	# A large inode prints " mtime: 0xSEC:EXTRA"; a 128-byte one has no extra
	# word at all and prints "mtime: 0xSEC". Matching only the first shape
	# silently records every ext2/ext3 timestamp as zero.
	mtime=$(echo "$stat" | sed -n 's/^ *mtime: 0x\([0-9a-f]*\):\([0-9a-f]*\).*/\1.\2/p' | head -1)
	[ -z "$mtime" ] && mtime=$(echo "$stat" | sed -n 's/^ *mtime: 0x\([0-9a-f]*\).*/\1.0/p' | head -1)
	crtime=$(echo "$stat" | sed -n 's/^ *crtime: 0x\([0-9a-f]*\):\([0-9a-f]*\).*/\1.\2/p' | head -1)
	[ -z "$mtime" ] && mtime="0.0"
	[ -z "$crtime" ] && crtime="-"

	echo "inode $ino size=$size links=${links:-0} mtime=$mtime crtime=$crtime"

	# Extents, as debugfs prints them: "(0-97):1465-1562" or "(0):1567".
	echo "$stat" | sed -n '/^EXTENTS:/,$p' | tail -n +2 | tr ',' '\n' | while read -r run; do
		run=$(echo "$run" | tr -d ' ')
		[ -z "$run" ] && continue
		case "$run" in
			\(ETB*) continue ;;   # extent tree block, reported separately as meta
		esac
		echo "ext $ino $run"
	done

	# "blocks" lists data blocks plus the indirect/extent-tree blocks that map
	# them; comparing the total checks both halves of the block map at once.
	#
	# Only ask when the inode actually owns blocks. debugfs prints the block
	# pointer area verbatim, so for a fast symlink — whose target is stored
	# there — it emits the characters of the path as block numbers. Blockcount
	# is the reliable signal, and it is 0 for such an inode.
	blockcount=$(echo "$stat" | sed -n 's/.*Blockcount: \([0-9]*\).*/\1/p' | head -1)
	if [ "${blockcount:-0}" -gt 0 ]; then
		total=$(df "blocks <$ino>" | tr ' ' '\n' | grep -c '^[0-9]')
		[ "$total" -gt 0 ] && echo "blocks $ino $total"
	fi
done

# --- deleted inodes ----------------------------------------------------------
df "lsdel" | awk '/^ *[0-9]+ +[0-9]+ +[0-9]+ / && $1 > 0 {print "del " $1}'

# --- deleted directory records ----------------------------------------------
for dir in $(df "ls -l /" | awk '$3 ~ /^4/ {print $NF}' | grep -v '^\.\.*$'; echo "/"); do
	df "ls -d $dir" \
	 | grep -oE '<[0-9]+> +\([0-9]+\) +[^ ]+' \
	 | sed -E "s|<([0-9]+)> +\([0-9]+\) +(.*)|slack $dir \1 \2|" \
	 | grep -v ' 0 ' || true
done

# --- extended attributes -----------------------------------------------------
for f in $(df "ls -l /" | awk '$3 ~ /^1/ {print $NF}'); do
	df "ea_list /$f" | sed -n 's/^  \(.*\) ([0-9]*) = "\(.*\)"$/xattr \1 \2/p' \
	 | sed "s|^xattr |xattr /$f |"
done

# --- journal transactions ----------------------------------------------------
df "logdump -O" \
 | sed -n 's/^Found expected sequence \([0-9]*\), type \([0-9]*\) (\([a-z ]*\)) at block \([0-9]*\)/jrnl \1 \2 \4/p'
