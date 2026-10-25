// Package docs provides an HTTP handler that serves a Scalar API reference
// page, rendering the OpenAPI spec dynamically (no static file needed).
package docs

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	scalar "github.com/MarceloPetrucio/go-scalar-api-reference"
)

// Handler returns an Echo handler for serving the API reference at /docs.
// It renders the spec from a given file path using Scalar's CDN-based renderer.
func Handler(specPath string, title string, darkMode bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		opts := scalar.Options{
			SpecURL: "file://" + absPath(specPath),
			CustomOptions: scalar.CustomOptions{
				PageTitle: title,
			},
			DarkMode: darkMode,
		}

		html, err := scalar.ApiReferenceHTML(&opts)
		if err != nil {
			return c.String(http.StatusInternalServerError, fmt.Sprintf("failed to render docs: %v", err))
		}

		return c.HTML(http.StatusOK, html)
	}
}


