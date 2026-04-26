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
		fmt.Fprintf(os.Stderr, "Usage: xattr <filesystem_image> [path]\n")
		fmt.Fprintf(os.Stderr, "Example: xattr disk.img /home/user\n")
		os.Exit(1)
	}

	fsPath := args[0]
	filePath := "/"
	if len(args) > 1 {
		filePath = args[1]
	}

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

	// Find the file/directory
	file, err := vol.OpenPath(filePath)
	if err != nil {
		log.Fatalf("Failed to open path: %v", err)
	}

	// Get inode details
	inodeNum := file.InodeNumber()

	// Get extended attributes
	xattrs, err := vol.GetXAttrs(inodeNum)
	if err != nil {
		log.Fatalf("Failed to get xattrs: %v", err)
	}

	// Display results
	fmt.Printf("Extended Attributes for %s (inode %d):\n", filePath, inodeNum)
	fmt.Printf("Total attributes: %d\n", xattrs.Len())

	if xattrs.Len() == 0 {
		fmt.Println("(no extended attributes)")
		return
	}

	fmt.Println()
	for _, attr := range xattrs.Attrs {
		fmt.Printf("  %s: ", attr.Name)
		// Display value as hex if not printable
		if isPrintable(attr.Value) {
			fmt.Printf("%q\n", string(attr.Value))
		} else {
			fmt.Printf("%x\n", attr.Value)
		}
	}
}

// isPrintable checks if a byte slice contains only printable ASCII characters.
func isPrintable(data []byte) bool {
	for _, b := range data {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}
