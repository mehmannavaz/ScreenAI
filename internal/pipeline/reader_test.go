package pipeline

import (
	"strings"
	"testing"
)

func readerFixture() *MCE {
	two, three := 2, 3
	return &MCE{Reader: []ReaderPart{
		{Role: "heading", Level: &two, Text: "Bread that takes all day"},
		{Role: "paragraph", Text: "Long fermentation is not a fashion; it is a chemistry that trades your calendar for flavour."},
		{Role: "heading", Level: &three, Text: "  The   levain  "},
		{Role: "staticText", Text: "Start the levain Friday night."},
	}}
}

func TestRenderReaderMarkdown(t *testing.T) {
	md := RenderReaderView(readerFixture(), "markdown")
	for _, want := range []string{
		"## Bread that takes all day",
		"### The levain",
		"Long fermentation is not a fashion",
		"Start the levain Friday night.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "  The   levain  ") {
		t.Errorf("heading text not whitespace-normalized")
	}
}

func TestRenderReaderText(t *testing.T) {
	txt := RenderReaderView(readerFixture(), "text")
	if !strings.Contains(txt, "Bread that takes all day\n------------------------") {
		t.Errorf("h2 underline wrong:\n%s", txt)
	}
	if !strings.Contains(txt, "The levain\n----------") {
		t.Errorf("h3 underline wrong:\n%s", txt)
	}
}

func TestRenderReaderHTML(t *testing.T) {
	htm := RenderReaderView(readerFixture(), "html")
	for _, want := range []string{"<article>", "<h2>Bread that takes all day</h2>", "<h3>The levain</h3>", "<p>Start the levain Friday night.</p>", "</article>"} {
		if !strings.Contains(htm, want) {
			t.Errorf("html missing %q:\n%s", want, htm)
		}
	}
	if !strings.Contains(htm, "&lt;") && strings.Contains(htm, "<script>") {
		t.Errorf("html not escaped")
	}
}

func TestRenderReaderEscapesHTML(t *testing.T) {
	m := &MCE{Reader: []ReaderPart{{Role: "paragraph", Text: "a < b & c"}}}
	htm := RenderReaderView(m, "html")
	if !strings.Contains(htm, "a &lt; b &amp; c") {
		t.Errorf("html escaping wrong: %q", htm)
	}
}

func TestReaderExtension(t *testing.T) {
	cases := map[string]string{"text": "txt", "txt": "txt", "markdown": "md", "md": "md", "html": "html"}
	for in, want := range cases {
		if got := ReaderExtension(in); got != want {
			t.Errorf("ReaderExtension(%q) = %q, want %q", in, got, want)
		}
	}
}
