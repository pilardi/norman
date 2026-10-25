// Package docs provides programmatic generation of API reference documentation
// from the project's api-spec.yaml using the Scalar library.
package docs

import (
	"os"
	"path/filepath"

	scalar "github.com/MarceloPetrucio/go-scalar-api-reference"
)

// Generate writes a Scalar-powered API reference HTML page to outputPath,
// rendering the OpenAPI spec at specPath. The customCSS parameter can contain
// CSS rules that will be injected into the generated page (e.g., custom color palette).
func Generate(specPath, outputPath, title string, darkMode bool, customCSS string) error {
	opts := scalar.Options{
		SpecURL:     "file://" + absPath(specPath),
		CustomCss:   customCSS,
		DarkMode:    darkMode,
	}
	opts.PageTitle = title

	html, err := scalar.ApiReferenceHTML(&opts)
	if err != nil {
		return err
	}

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(outputPath, []byte(html), 0644)
}

// GenerateBytes returns the HTML content without writing to disk.
func GenerateBytes(specPath, title string, darkMode bool, customCSS string) ([]byte, error) {
	opts := scalar.Options{
		SpecURL:   "file://" + absPath(specPath),
		CustomCss: customCSS,
		DarkMode:  darkMode,
	}
	opts.PageTitle = title

	html, err := scalar.ApiReferenceHTML(&opts)
	if err != nil {
		return nil, err
	}

	return []byte(html), nil
}

// absPath resolves a spec path to an absolute filesystem path.
func absPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, p)
}
