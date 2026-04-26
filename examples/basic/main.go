package main

import (
	"fmt"
	"log"
	"os"

	"github.com/aoiflux/libext"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <ext_volume_or_image>\n", os.Args[0])
		os.Exit(1)
	}

	img, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer img.Close()

	vol, err := libext.Open(img)
	if err != nil {
		log.Fatal(err)
	}
	defer vol.Close()

	fmt.Printf("Kind: %s\n", vol.Kind())
	sb := vol.Superblock()
	fmt.Printf("Block Size: %d\n", sb.BlockSize)
	fmt.Printf("Inodes: %d\n", sb.InodesCount)

	root, err := vol.GetRootDirectory()
	if err != nil {
		log.Fatal(err)
	}
	entries, err := root.ReadDir()
	if err != nil {
		log.Fatal(err)
	}

	for _, e := range entries {
		kind := "FILE"
		if e.IsDirectory {
			kind = "DIR "
		}
		fmt.Printf("[%s] %s (%d bytes)\n", kind, e.Name, e.Size)
	}
}
