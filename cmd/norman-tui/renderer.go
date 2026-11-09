package main

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
