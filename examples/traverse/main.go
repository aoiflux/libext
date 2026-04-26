package main

import (
	"fmt"
	"log"
	"os"
	"path"

	"github.com/aoiflux/libext"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("Usage: %s <ext_volume_or_image> <start_path>\n", os.Args[0])
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

	start, err := vol.OpenPath(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	if !start.IsDirectory() {
		log.Fatalf("%s is not a directory", os.Args[2])
	}

	fmt.Printf("Traversing %s\n", os.Args[2])
	_ = vol.WalkDir(start.InodeNumber(), func(p string, e libext.DirEntry) error {
		kind := "FILE"
		if e.IsDirectory {
			kind = "DIR "
		}
		fmt.Printf("[%s] %s\n", kind, path.Clean(p))
		return nil
	})
}
