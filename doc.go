// Package libext provides a pure-Go, read-only parser for EXT1/2/3/4 images.
//
// Basic usage:
//
//	img, err := os.Open("disk.img")
//	if err != nil {
//		// handle error
//	}
//	defer img.Close()
//
//	vol, err := libext.Open(img)
//	if err != nil {
//		// handle error
//	}
//	defer vol.Close()
//
//	sb := vol.Superblock()
//	_ = sb.BlockSize
//
//	root, err := vol.GetRootDirectory()
//	if err != nil {
//		// handle error
//	}
//
//	entries, err := root.ReadDir()
//	if err != nil {
//		// handle error
//	}
//	_ = entries
package libext
