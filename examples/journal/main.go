package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/aoiflux/libext"
)

func main() {
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: journal <filesystem_image>\n")
		fmt.Fprintf(os.Stderr, "Example: journal disk.img\n")
		os.Exit(1)
	}

	fsPath := args[0]

	// Open filesystem image
	f, err := os.Open(fsPath)
	if err != nil {
		log.Fatalf("Failed to open filesystem image: %v", err)
	}
	defer f.Close()

	// Create filesystem volume
	vol, err := libext.OpenWithSize(f, 0)
	if err != nil {
		log.Fatalf("Failed to open filesystem: %v", err)
	}

	// Display journal status
	status, err := vol.DescribeJournalStatus()
	if err != nil {
		log.Fatalf("Failed to get journal status: %v", err)
	}

	fmt.Printf("Journal Status: %s\n", status)
	fmt.Println()

	// Display journal features
	features := vol.GetJournalFeatures()
	fmt.Println("Journal Features:")
	for name, enabled := range features {
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		fmt.Printf("  %s: %s\n", name, status)
	}

	// Try to list transactions
	journalInode := vol.GetJournalInode()
	if journalInode == 0 {
		fmt.Println("\nNo journal or external journal detected.")
		return
	}

	fmt.Println()

	ctx := context.Background()

	// One walk of the journal, kept. ListJournalTransactions would walk it
	// again for the block queries below, and again for every inode after that.
	jx, err := vol.BuildJournalIndexContext(ctx)
	if err != nil {
		fmt.Printf("Could not read the journal: %v\n", err)
		return
	}
	txns := jx.Transactions()

	fmt.Printf("Journal Transactions: %d found (%d blocks have journalled copies)\n",
		len(txns), jx.Len())
	for i, txn := range txns {
		committed := "uncommitted"
		if txn.IsCommitted {
			committed = "committed"
		}
		fmt.Printf("  [%d] seq=%d block=%d type=%s %s\n",
			i, txn.Sequence, txn.StartBlock, txn.Type, committed)
	}

	// The journal names what changed by inode number, never by path. One walk of
	// the tree turns any number of those numbers into names; PathFor on the FS
	// would walk again for every inode asked about.
	ix, err := vol.BuildPathIndexContext(ctx)
	if err != nil {
		fmt.Printf("\nCould not index paths: %v\n", err)
		return
	}

	ops, err := vol.FastCommitOps()
	if err != nil || len(ops) == 0 {
		return
	}

	fmt.Printf("\nFast-commit operations: %d found\n", len(ops))
	for i, op := range ops {
		name := op.Name
		if name == "" {
			name = "(unnamed)"
		}

		// An inode the live tree no longer links has no path, which is the
		// ordinary answer for one the journal recorded being unlinked.
		path, err := ix.PathFor(op.Inode)
		switch {
		case errors.Is(err, libext.ErrPathNotFound):
			path = "(not linked in the live tree)"
		case err != nil:
			path = fmt.Sprintf("(error: %v)", err)
		}

		fmt.Printf("  [%d] %s inode=%d name=%s path=%s\n",
			i, op.Tag, op.Inode, name, path)
	}
}
