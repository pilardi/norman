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
	"github.com/yuin/goldmark/extension"
)

const materialCSS = `
<style>
  @import url('https://fonts.googleapis.com/css2?family=Roboto:wght@300;400;500;700&display=swap');
  @import url('https://fonts.googleapis.com/icon?family=Material+Icons');

  :root {
    --md-primary: #6200EE;
    --md-primary-variant: #3700B3;
    --md-secondary: #03DAC6;
    --md-surface: #FFFFFF;
    --md-background: #F5F5F5;
    --md-on-primary: #FFFFFF;
    --md-on-surface: #212121;
    --md-on-surface-variant: #757575;
    --md-elevation-1: 0 2px 4px rgba(0,0,0,0.1);
    --md-elevation-2: 0 4px 8px rgba(0,0,0,0.15);
    --md-radius: 8px;
  }

  * { box-sizing: border-box; margin: 0; padding: 0; }

  body {
    font-family: 'Roboto', sans-serif;
    background: var(--md-background);
    color: var(--md-on-surface);
    line-height: 1.6;
    max-width: 960px;
    margin: 0 auto;
    padding: 2rem 1.5rem;
  }

  h1, h2, h3, h4, h5, h6 {
    color: var(--md-primary);
    margin-top: 1.5em;
    font-weight: 500;
  }
  h1 { font-size: 2rem; border-bottom: 2px solid var(--md-primary); padding-bottom: 0.3em; }
  h2 { font-size: 1.5rem; }
  h3 { font-size: 1.25rem; color: var(--md-primary-variant); }

  a { color: var(--md-primary); text-decoration: none; }
  a:hover { text-decoration: underline; }

  code, pre { background: #EDEDED; border-radius: 4px; font-family: 'Roboto Mono', monospace; }
  code { padding: 2px 6px; font-size: 0.9em; }
  pre { padding: 1rem; overflow-x: auto; margin: 1em 0; }

  table { width: 100%; border-collapse: collapse; margin: 1em 0; box-shadow: var(--md-elevation-1); border-radius: var(--md-radius); overflow: hidden; }
  th { background: var(--md-primary); color: var(--md-on-primary); padding: 0.75rem 1rem; text-align: left; font-weight: 500; }
  td { padding: 0.6rem 1rem; border-bottom: 1px solid #E0E0E0; }
  tr:last-child td { border-bottom: none; }
  tr:hover { background: #F8F9FA; }

  ul, ol { padding-left: 1.5em; margin: 0.5em 0; }
  li { margin: 0.3em 0; }

  blockquote { border-left: 4px solid var(--md-primary); padding: 0.5em 1em; background: #F3E5F5; margin: 1em 0; border-radius: 0 var(--md-radius) var(--md-radius) 0; }

  .task-list-item { list-style: none; margin-left: -1.5em; }
  .task-list-item input { margin-right: 0.5em; }

  strong { color: #333; }

  @media (prefers-color-scheme: dark) {
    :root {
      --md-primary: #BB86FC;
      --md-primary-variant: #9D4EEB;
      --md-secondary: #03DAC6;
      --md-surface: #121212;
      --md-background: #1E1E1E;
      --md-on-primary: #000000;
      --md-on-surface: #E0E0E0;
      --md-on-surface-variant: #A0A0A0;
      --md-elevation-1: 0 2px 4px rgba(0,0,0,0.3);
    }
    code, pre { background: #2C2C2C; }
    tr:hover { background: #2A2A2A; }
    blockquote { background: #2D1B3E; }
  }
</style>
`

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

	markdown := goldmark.New(
		goldmark.WithExtensions(extension.Table),
	)

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

		result := append([]byte(materialCSS), buf.Bytes()...)
		// Convert .md href links to .html for generated output
		result = bytes.ReplaceAll(result, []byte(`.md"`), []byte(`.html"`))
		// Flatten design-log/ subdirectory paths in hrefs (output is flat)
		result = bytes.ReplaceAll(result, []byte(`href="design-log/`), []byte(`href="`))
		if err := os.WriteFile(outPath, result, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", filepath.Base(path), err)
			continue
		}

		fmt.Printf("Converted %s -> %s\n", filepath.Base(path), outPath)
	}
}
