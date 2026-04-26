package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/aoiflux/libext"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Printf("Usage: %s <ext_volume_or_image> <file_path> <output_path>\n", os.Args[0])
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

	f, err := vol.OpenPath(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	if f.IsDirectory() {
		log.Fatalf("%s is a directory", os.Args[2])
	}

	out, err := os.Create(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	if _, err := io.Copy(out, f); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Extracted %s to %s\n", os.Args[2], os.Args[3])
}
