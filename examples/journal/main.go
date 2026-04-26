package main

import (
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

	txns, err := vol.ListJournalTransactions()
	if err != nil {
		fmt.Printf("Could not list transactions: %v\n", err)
		return
	}

	fmt.Printf("Journal Transactions: %d found\n", len(txns))
	for i, txn := range txns {
		committed := "uncommitted"
		if txn.IsCommitted {
			committed = "committed"
		}
		fmt.Printf("  [%d] seq=%d block=%d type=%s %s\n",
			i, txn.Sequence, txn.StartBlock, txn.Type, committed)
	}
}
