package main

import (
	"fmt"
	"log"
	"os"

	"github.com/aoiflux/libext"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("Usage: %s <ext_volume_or_image> <output_json> [mode]\n", os.Args[0])
		fmt.Println("Modes: standard, deep")
		os.Exit(1)
	}

	imgPath := os.Args[1]
	outPath := os.Args[2]

	mode := "standard"
	if len(os.Args) > 3 {
		mode = os.Args[3]
	}

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

	out, err := os.Create(outPath)
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	reportName := "ext-report"
	switch mode {
	case "standard":
		err = vol.WriteReport(reportName, out)
	case "deep":
		err = vol.WriteReportDeep(reportName, out)
	default:
		log.Fatalf("invalid mode %q (expected: standard or deep)", mode)
	}
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Wrote %s report to %s\n", mode, outPath)
}
