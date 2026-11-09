package main

import (
	"fmt"
	"strings"
)

// renderJSONTree renders a structured_data map as a collapsible JSON tree using box-drawing characters.
func renderJSONTree(data *map[string]interface{}) string {
	if data == nil {
		return "┌─ JSON Output ───────────────────────────────────────┐\n│  (empty)                                              │\n└──────────────────────────────────────────────────────┘"
	}

	var lines []string
	lines = append(lines, "┌─ JSON Output ───────────────────────────────────────┐")

	renderMap(*data, &lines, "│  ")

	lines = append(lines, "└──────────────────────────────────────────────────────┘")
	return strings.Join(lines, "\n")
}

// renderMap recursively renders a map with indentation.
func renderMap(m map[string]interface{}, lines *[]string, prefix string) {
	for k, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			*lines = append(*lines, fmt.Sprintf("%s├─ %s:", prefix, k))
			renderMap(val, lines, prefix+"│  ")
		case []interface{}:
			*lines = append(*lines, fmt.Sprintf("%s├─ %s:", prefix, k))
			for i, item := range val {
				if i < len(val)-1 {
					*lines = append(*lines, fmt.Sprintf("%s│  ├─ [%d]", prefix, i))
				} else {
					*lines = append(*lines, fmt.Sprintf("%s│  └─ [%d]", prefix, i))
				}
				renderValue(item, lines, prefix+"│     ")
			}
		default:
			suffix := "├─"
			renderValue(v, lines, prefix+suffix)
		}
	}
}

// renderValue formats a single value with type annotation.
func renderValue(v interface{}, lines *[]string, prefix string) {
	switch val := v.(type) {
	case map[string]interface{}:
		*lines = append(*lines, fmt.Sprintf("%s[%d] object:", prefix, 0))
		renderMap(val, lines, prefix+"│     ")
	case []interface{}:
		*lines = append(*lines, fmt.Sprintf("%sarray[%d]", prefix, len(val)))
		for i, item := range val {
			suffix := "├─"
			if i < len(val)-1 {
				renderValue(item, lines, prefix+suffix)
			} else {
				renderValue(item, lines, prefix+"└─")
			}
		}
	case string:
		truncated := val
		if len(val) > 60 {
			truncated = val[:57] + "..."
		}
		*lines = append(*lines, fmt.Sprintf("%sstring: %q", prefix, truncated))
	case float64:
		*lines = append(*lines, fmt.Sprintf("%snumber: %.2f", prefix, val))
	case int:
		*lines = append(*lines, fmt.Sprintf("%snumber: %d", prefix, val))
	case bool:
		*lines = append(*lines, fmt.Sprintf("%sbool: %v", prefix, val))
	case nil:
		*lines = append(*lines, fmt.Sprintf("%snull", prefix))
	default:
		*lines = append(*lines, fmt.Sprintf("%s[%T] %v", prefix, v, v))
	}
}
