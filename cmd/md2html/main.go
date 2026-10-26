// Package main converts Markdown files to HTML using Goldmark.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
)

func stripFrontmatter(data []byte) []byte {
	content := string(data)
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		end := strings.Index(content[3:], "\n---\n")
		if end == -1 {
			end = strings.Index(content[3:], "\r\n---\r\n")
			if end == -1 {
				return data
			}
			return []byte(content[3+end+4:])
		}
		return []byte(content[3+end+4:])
	}
	return data
}

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
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", filepath.Base(path), err)
			continue
		}

		// Strip YAML frontmatter before converting
		data = stripFrontmatter(data)

		var buf bytes.Buffer
		if err := markdown.Convert(data, &buf); err != nil {
			fmt.Fprintf(os.Stderr, "Error converting %s: %v\n", filepath.Base(path), err)
			continue
		}

		outPath := filepath.Join(outDir, filepath.Base(path))
		// Replace .md extension with .html
		outPath = outPath[:len(outPath)-3] + ".html"

		if err := os.WriteFile(outPath, buf.Bytes(), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", filepath.Base(path), err)
			continue
		}

		fmt.Printf("Converted %s -> %s\n", filepath.Base(path), outPath)
	}
}
