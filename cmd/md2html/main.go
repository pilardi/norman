// Package main converts Markdown files to HTML using Goldmark.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yuin/goldmark"
)

func main() {
	var outDir string
	flag.StringVar(&outDir, "out", "", "Output directory for HTML files (required)")
	flag.Parse()

	if outDir == "" {
		fmt.Fprintf(os.Stderr, "Error: --out flag is required\n")
		os.Exit(1)
	}

	mdFiles := flag.Args()
	if len(mdFiles) == 0 {
		fmt.Fprintln(os.Stderr, "No input files specified.")
		os.Exit(1)
	}

	markdown := goldmark.New()

	for _, path := range mdFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
			continue
		}

		var buf bytes.Buffer
		if err := markdown.Convert(data, &buf); err != nil {
			fmt.Fprintf(os.Stderr, "Error converting %s: %v\n", path, err)
			continue
		}

		outPath := filepath.Join(outDir, filepath.Base(path))
		// Replace .md extension with .html
		outPath = outPath[:len(outPath)-3] + ".html"

		if err := os.WriteFile(outPath, buf.Bytes(), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outPath, err)
			continue
		}

		fmt.Printf("Converted %s -> %s\n", filepath.Base(path), outPath)
	}
}
