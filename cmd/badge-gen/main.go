// cmd/badge-gen generates a coverage SVG badge from a Go coverage profile.
//
// Usage: go run ./cmd/badge-gen -input coverage.out -output docs/coverage-badge.svg

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	input := flag.String("input", "coverage.out", "path to coverage.out")
	output := flag.String("output", "badge.svg", "output path for the SVG badge")
	flag.Parse()

	// Ensure output directory exists
	if dir := filepath.Dir(*output); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating output dir %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", *input, err)
		os.Exit(1)
	}

	totalLines, coveredLines := parseCoverage(data)
	pct := 0.0
	if totalLines > 0 {
		pct = float64(coveredLines) / float64(totalLines) * 100
	}

	svg := generateBadge(pct, totalLines, coveredLines)
	if err := os.WriteFile(*output, []byte(svg), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", *output, err)
		os.Exit(1)
	}

	fmt.Printf("Coverage badge written to %s (%.1f%%)\n", *output, pct)
}

func parseCoverage(data []byte) (totalLines, coveredLines int) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		// Format: file:line.start,line.end count
		// Split on first comma to isolate start and end positions
		commaIdx := strings.Index(line, ",")
		if commaIdx < 0 {
			continue
		}

		beforeComma := line[:commaIdx]  // e.g. "github.com/.../main.go:16.13"
		afterComma := line[commaIdx+1:] // e.g. "22.46 4 0"

		// Extract start position from before comma
		colonIdx := strings.LastIndex(beforeComma, ":")
		if colonIdx < 0 {
			continue
		}
		startPos := beforeComma[colonIdx+1:] // e.g. "16.13"

		// Extract end position and count from after comma
		fields := strings.Fields(afterComma)
		if len(fields) < 2 {
			continue
		}
		endPos := fields[0] // e.g. "22.46"
		// Last field is branch indicator (0/1); count is second-to-last
		countStr := fields[len(fields)-2]

		// Parse line numbers (ignore column)
		startLine, err := strconv.Atoi(strings.Split(startPos, ".")[0])
		if err != nil {
			continue
		}
		endLine, err := strconv.Atoi(strings.Split(endPos, ".")[0])
		if err != nil {
			continue
		}

		totalLines += endLine - startLine + 1
		count, err := strconv.Atoi(countStr)
		if err == nil && count > 0 {
			coveredLines += count
		}
	}
	return
}

func generateBadge(pct float64, totalLines, coveredLines int) string {
	color := "#4c1" // green for >= 80%
	if pct < 50 {
		color = "#e05d44" // red
	} else if pct < 70 {
		color = "#9f9f9f" // gray
	} else if pct < 80 {
		color = "#c3732b" // orange
	}

	label := fmt.Sprintf("%.1f%%", pct)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="100" height="20">
  <linearGradient id="a" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <rect rx="3" width="100" height="20" fill="#555555"/>
  <rect rx="3" x="50" width="50" height="20" fill="%s"/>
  <text x="25" y="15" fill="#fff" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="11" text-anchor="middle">coverage</text>
  <text x="75" y="15" fill="#fff" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="11" text-anchor="middle" font-weight="bold">%s</text>
</svg>`, color, label)
}
