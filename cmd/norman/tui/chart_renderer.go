package tui

import (
	"fmt"
	"math"
	"strings"
)

const (
	barFull  = "█"
	barHalf  = "▌"
	barEmpty = "░"
	barWidth = 40 // max bar width in characters
)

// renderBarChart renders structured_data as a horizontal ASCII bar chart.
func renderBarChart(data *map[string]interface{}) string {
	if data == nil {
		return "┌─ Bar Chart ────────────────────────────────────────┐\n│  (no data)                                          │\n└─────────────────────────────────────────────────────┘"
	}

	// Extract labels and values from structured_data.
	labels, values := extractChartDimensions(*data)
	if len(labels) == 0 {
		return "┌─ Bar Chart ────────────────────────────────────────┐\n│  (no data)                                          │\n└─────────────────────────────────────────────────────┘"
	}

	var lines []string
	lines = append(lines, "┌─ Bar Chart ────────────────────────────────────────┐")

	maxVal := math.MaxFloat64
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}

	for i, label := range labels {
		bar := renderBar(values[i], maxVal)
		labelStr := chartTruncate(label, 12)
		lines = append(lines, fmt.Sprintf("│ %-14s %s %.0f", labelStr+":", bar, values[i]))
	}

	lines = append(lines, "└─────────────────────────────────────────────────────┘")
	return strings.Join(lines, "\n")
}

// renderBar produces a horizontal bar string from value/maxVal.
func renderBar(value, maxVal float64) string {
	if maxVal == 0 {
		return strings.Repeat(barEmpty, barWidth)
	}

	ratio := value / maxVal
	fullBars := int(math.Round(ratio * barWidth))
	halfBar := 0
	if ratio*barWidth*2 > float64(fullBars)*2+0.5 {
		halfBar = 1
	}
	emptyBars := barWidth - fullBars - halfBar

	return strings.Repeat(barFull, fullBars) + strings.Repeat(barHalf, halfBar) + strings.Repeat(barEmpty, emptyBars)
}

// extractChartDimensions tries to pull labels and values from structured_data.
// It looks for common patterns: {labels: [...], values: [...]}, or a map of key→value.
func extractChartDimensions(data map[string]interface{}) ([]string, []float64) {
	// Pattern 1: explicit labels + values arrays
	if labelsRaw, ok := data["labels"]; ok {
		if valsRaw, ok := data["values"]; ok {
			labels, values := parseArrays(labelsRaw, valsRaw)
			if len(labels) > 0 {
				return labels, values
			}
		}
	}

	// Pattern 2: map of key→value (e.g., {"A": 10, "B": 20})
	if dataMap, ok := data["data"].(map[string]interface{}); ok {
		return extractKeyValuePairs(dataMap)
	}

	// Pattern 3: direct map of string→number
	var labels []string
	var values []float64
	for k, v := range data {
		labels = append(labels, k)
		switch val := v.(type) {
		case float64:
			values = append(values, val)
		case int:
			values = append(values, float64(val))
		}
	}

	return labels, values
}

// parseArrays converts interface{} arrays to string and float slices.
func parseArrays(labelsRaw, valsRaw interface{}) ([]string, []float64) {
	var labels []string
	var values []float64

	switch lv := labelsRaw.(type) {
	case []interface{}:
		for _, item := range lv {
			if s, ok := item.(string); ok {
				labels = append(labels, s)
			}
		}
	case []string:
		labels = lv
	}

	switch vv := valsRaw.(type) {
	case []interface{}:
		for _, item := range vv {
			switch n := item.(type) {
			case float64:
				values = append(values, n)
			case int:
				values = append(values, float64(n))
			}
		}
	case []float64:
		values = vv
	}

	return labels, values
}

// extractKeyValuePairs converts a key→value map to labels and values.
func extractKeyValuePairs(m map[string]interface{}) ([]string, []float64) {
	var labels []string
	var values []float64
	for k, v := range m {
		labels = append(labels, k)
		switch val := v.(type) {
		case float64:
			values = append(values, val)
		case int:
			values = append(values, float64(val))
		}
	}
	return labels, values
}

// chartTruncate shortens a string to maxLen with ellipsis.
func chartTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
