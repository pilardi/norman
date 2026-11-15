package tui

import (
	"fmt"
	"strings"

	"github.com/norman/norman/pkg/protocol"
)

// renderRichOutput dispatches a RichOutput to the appropriate renderer based on render_hint.
func renderRichOutput(ro *protocol.RichOutput) string {
	if ro == nil || ro.RenderHint == nil {
		return ""
	}

	switch *ro.RenderHint {
	case protocol.Json:
		return renderJSONTree(ro.StructuredData)
	case protocol.Chart:
		return renderBarChart(ro.StructuredData)
	case protocol.Markdown:
		return renderMarkdown(ro.StructuredData)
	case protocol.Text:
		return renderText(ro.StructuredData, ro.ArtifactUrl)
	case protocol.Html:
		return renderHtml(ro.StructuredData, ro.ArtifactUrl)
	case protocol.Pdf:
		return renderPdf(ro.ArtifactUrl)
	case protocol.Diagram:
		return renderDiagram(ro.StructuredData, ro.ArtifactUrl)
	case protocol.FileRef:
		return renderFileRef(ro.ArtifactUrl, ro.MimeType)
	default:
		// Fallback: return raw structured data as text
		if ro.StructuredData != nil {
			return fmt.Sprintf("Unknown render_hint=%s, showing raw data.\n%s", *ro.RenderHint, formatMap(*ro.StructuredData))
		}
		return "[no content]"
	}
}

// formatMap returns a simple key=value representation of the map.
func formatMap(m map[string]interface{}) string {
	var lines []string
	for k, v := range m {
		lines = append(lines, fmt.Sprintf("  %s: %v", k, v))
	}
	return strings.Join(lines, "\n")
}

// renderText renders text content from structured_data or artifact_url.
func renderText(data *map[string]interface{}, artifactUrl *string) string {
	if data != nil && (*data)["content"] != nil {
		content, ok := (*data)["content"].(string)
		if ok {
			var lines []string
			lines = append(lines, "┌─ Text Output ──────────────────────────────────────┐")
			for _, line := range strings.Split(content, "\n") {
				if line == "" {
					lines = append(lines, "│                                                     │")
				} else {
					// Truncate long lines
					if len(line) > 76 {
						line = line[:73] + "..."
					}
					lines = append(lines, fmt.Sprintf("│ %-*s │", 75, line))
				}
			}
			lines = append(lines, "└─────────────────────────────────────────────────────┘")
			return strings.Join(lines, "\n")
		}
	}
	if artifactUrl != nil && *artifactUrl != "" {
		return fmt.Sprintf("┌─ Text Artifact ─────────────────────────────────────┐\n│  URL: %s                                  │\n└──────────────────────────────────────────────────────┘", *artifactUrl)
	}
	return "┌─ Text Output ───────────────────────────────────────┐\n│  (no content)                                         │\n└──────────────────────────────────────────────────────┘"
}

// renderHtml renders HTML content or artifact_url reference.
func renderHtml(data *map[string]interface{}, artifactUrl *string) string {
	if artifactUrl != nil && *artifactUrl != "" {
		return fmt.Sprintf("┌─ HTML Output ───────────────────────────────────────┐\n│  Artifact URL: %s                                  │\n└──────────────────────────────────────────────────────┘", *artifactUrl)
	}
	if data != nil && (*data)["content"] != nil {
		content, ok := (*data)["content"].(string)
		if ok {
			var lines []string
			lines = append(lines, "┌─ HTML Output ───────────────────────────────────────┐")
			for _, line := range strings.Split(content, "\n") {
				if line == "" {
					lines = append(lines, "│                                                     │")
				} else {
					if len(line) > 76 {
						line = line[:73] + "..."
					}
					lines = append(lines, fmt.Sprintf("│ %-*s │", 75, line))
				}
			}
			lines = append(lines, "└──────────────────────────────────────────────────────┘")
			return strings.Join(lines, "\n")
		}
	}
	return "┌─ HTML Output ───────────────────────────────────────┐\n│  (no content)                                         │\n└──────────────────────────────────────────────────────┘"
}

// renderPdf renders PDF artifact_url reference.
func renderPdf(artifactUrl *string) string {
	if artifactUrl != nil && *artifactUrl != "" {
		return fmt.Sprintf("┌─ PDF Document ──────────────────────────────────────┐\n│  Download URL: %s                                │\n└──────────────────────────────────────────────────────┘", *artifactUrl)
	}
	return "┌─ PDF Document ──────────────────────────────────────┐\n│  (no artifact URL)                                    │\n└──────────────────────────────────────────────────────┘"
}

// renderDiagram renders diagram content or artifact_url reference.
func renderDiagram(data *map[string]interface{}, artifactUrl *string) string {
	if artifactUrl != nil && *artifactUrl != "" {
		return fmt.Sprintf("┌─ Diagram ───────────────────────────────────────────┐\n│  Artifact URL: %s                                  │\n└──────────────────────────────────────────────────────┘", *artifactUrl)
	}
	if data != nil && (*data)["content"] != nil {
		content, ok := (*data)["content"].(string)
		if ok {
			var lines []string
			lines = append(lines, "┌─ Diagram ───────────────────────────────────────────┐")
			for _, line := range strings.Split(content, "\n") {
				if line == "" {
					lines = append(lines, "│                                                     │")
				} else {
					if len(line) > 76 {
						line = line[:73] + "..."
					}
					lines = append(lines, fmt.Sprintf("│ %-*s │", 75, line))
				}
			}
			lines = append(lines, "└──────────────────────────────────────────────────────┘")
			return strings.Join(lines, "\n")
		}
	}
	return "┌─ Diagram ───────────────────────────────────────────┐\n│  (no content or artifact URL)                         │\n└──────────────────────────────────────────────────────┘"
}

// renderFileRef renders file reference with artifact_url and mime_type.
func renderFileRef(artifactUrl *string, mimeType *string) string {
	var mimeStr string
	if mimeType != nil && *mimeType != "" {
		mimeStr = *mimeType
	} else {
		mimeStr = "application/octet-stream"
	}

	if artifactUrl != nil && *artifactUrl != "" {
		return fmt.Sprintf("┌─ File Reference ────────────────────────────────────┐\n│  Type: %s\n│  URL: %s                               │\n└──────────────────────────────────────────────────────┘", mimeStr, *artifactUrl)
	}
	return fmt.Sprintf("┌─ File Reference ────────────────────────────────────┐\n│  Type: %s\n│  (no artifact URL)                                    │\n└──────────────────────────────────────────────────────┘", mimeStr)
}
