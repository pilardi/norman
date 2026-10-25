// Package main provides a CLI for generating API reference HTML via Scalar.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/norman/norman/pkg/docs"
)

func main() {
	var specPath, outputPath, title string
	var darkMode bool

	flag.StringVar(&specPath, "spec", "api-spec.yaml", "Path to the OpenAPI spec file")
	flag.StringVar(&outputPath, "out", "", "Output path for the generated HTML (required)")
	flag.StringVar(&title, "title", "API Reference", "Page title")
	flag.BoolVar(&darkMode, "dark", false, "Enable dark mode")
	flag.Parse()

	if outputPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -out flag is required\n")
		os.Exit(1)
	}

	if err := docs.Generate(specPath, outputPath, title, darkMode); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating docs: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated API reference at %s\n", outputPath)
}
