package main

import (
	"fmt"
	"log"
	"os"

	"github.com/aoiflux/libext"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("Usage: %s <ext_volume_or_image> <standard|deep>\n", os.Args[0])
		os.Exit(1)
	}

	imgPath := os.Args[1]
	mode := os.Args[2]

	img, err := os.Open(imgPath)
	if err != nil {
		log.Fatal(err)
	}
	defer img.Close()

	vol, err := libext.Open(img)
	if err != nil {
		log.Fatal(err)
	}
	defer vol.Close()

	var report libext.EXTReport
	switch mode {
	case "standard":
		report, err = vol.Report("ext-report")
	case "deep":
		report, err = vol.ReportDeep("ext-report")
	default:
		log.Fatalf("invalid mode %q (expected: standard or deep)", mode)
	}
	if err != nil {
		log.Fatal(err)
	}

	summary := report.Summary()
	deleted := report.DeletedFiles()
	fragmented := report.FragmentedFiles()
	regular := report.FilesByType("file")

	fmt.Printf("Report Name: %s\n", report.Name)
	fmt.Printf("Filesystem: %s (block size=%d)\n", report.Filesystem.Type, report.Filesystem.BlockSize)
	fmt.Printf("Scan Mode: %s\n", mode)
	fmt.Printf("Entries: %d\n", summary.Total)
	fmt.Printf("Deleted: %d\n", len(deleted))
	fmt.Printf("Fragmented: %d\n", len(fragmented))
	fmt.Printf("Regular files: %d\n", len(regular))

	show := len(report.Files)
	if show > 10 {
		show = 10
	}
	fmt.Printf("\nFirst %d entries:\n", show)
	for i := 0; i < show; i++ {
		f := report.Files[i]
		fmt.Printf("- %s | type=%s size=%d deleted=%t fragmented=%t fragments=%d\n",
			f.Filename, f.Type, f.Size, f.IsDeleted, f.IsFragmented, len(f.Fragments))
	}
}
