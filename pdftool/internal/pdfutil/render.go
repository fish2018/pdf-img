package pdfutil

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/gen2brain/go-fitz"
)

// RenderPages converts every page from the source PDF into a PNG image.
func RenderPages(pdfPath, destDir string) ([]string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	doc, err := fitz.New(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer doc.Close()

	total := doc.NumPage()
	if total == 0 {
		return nil, fmt.Errorf("pdf has no pages")
	}

	var paths []string
	for i := 0; i < total; i++ {
		img, err := doc.Image(i)
		if err != nil {
			return nil, fmt.Errorf("render page %d: %w", i+1, err)
		}
		outPath := filepath.Join(destDir, fmt.Sprintf("page-%03d.png", i+1))
		outFile, err := os.Create(outPath)
		if err != nil {
			return nil, fmt.Errorf("create image file: %w", err)
		}
		if err := png.Encode(outFile, img); err != nil {
			outFile.Close()
			return nil, fmt.Errorf("encode page %d: %w", i+1, err)
		}
		outFile.Close()
		paths = append(paths, outPath)
	}

	return paths, nil
}
