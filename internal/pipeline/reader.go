package pipeline

import (
	"fmt"
	"html"
	"strings"
)

// OutlineItem is one heading inside the main content — the document
// outline the Screen2x selection implies (role "heading" nodes with a
// hierarchical level, in tree order).
type OutlineItem struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// ReadingWordsPerMinute drives the reading-time estimate in MCEStats.
const ReadingWordsPerMinute = 200

// RenderReaderView renders the MCE reader view — the distilled main
// content — in one of the export formats: "markdown", "text" or
// "html". Headings keep their levels from the page tree (h1..h6 for
// HTML inputs; OCR inputs have no heading nodes and export as plain
// paragraphs).
func RenderReaderView(m *MCE, format string) string {
	switch format {
	case "text", "txt":
		return renderReaderText(m)
	case "html", "htm":
		return renderReaderHTML(m)
	default: // "markdown", "md"
		return renderReaderMarkdown(m)
	}
}

func renderReaderMarkdown(m *MCE) string {
	var b strings.Builder
	for _, part := range m.Reader {
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		if part.Role == "heading" {
			level := 1
			if part.Level != nil {
				level = clamp(*part.Level, 1, 6)
			}
			b.WriteString(strings.Repeat("#", level) + " " + oneLine(text) + "\n\n")
			continue
		}
		b.WriteString(text + "\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderReaderText(m *MCE) string {
	var b strings.Builder
	for _, part := range m.Reader {
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		if part.Role == "heading" {
			underline := strings.Repeat("-", len(oneLine(text)))
			if part.Level != nil && *part.Level <= 1 {
				underline = strings.Repeat("=", len(oneLine(text)))
			}
			b.WriteString(oneLine(text) + "\n" + underline + "\n\n")
			continue
		}
		b.WriteString(text + "\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderReaderHTML(m *MCE) string {
	var b strings.Builder
	b.WriteString("<article>\n")
	for _, part := range m.Reader {
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		if part.Role == "heading" {
			level := 1
			if part.Level != nil {
				level = clamp(*part.Level, 1, 6)
			}
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", level, html.EscapeString(oneLine(text)), level)
			continue
		}
		fmt.Fprintf(&b, "<p>%s</p>\n", html.EscapeString(text))
	}
	b.WriteString("</article>\n")
	return b.String()
}

// ReaderExtension maps an export format to its file extension.
func ReaderExtension(format string) string {
	switch format {
	case "text", "txt":
		return "txt"
	case "html", "htm":
		return "html"
	default:
		return "md"
	}
}

// oneLine collapses inner whitespace so a heading never wraps across
// lines in the text/markdown exports.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
