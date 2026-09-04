// Package examples bundles the demo inputs: one per supported kind
// (image / page / tree / text), embedded so a single compiled binary can
// run `screenai demo` anywhere.
package examples

import (
	"bytes"
	"embed"
	"image"
	"image/png"

	"github.com/mehmannavaz/ScreenAI/internal/imaging"
)

//go:embed example_1_news_article.png example_3_article.html example_4_page_tree.json example_6_memo.txt
var files embed.FS

// Map returns the demo inputs keyed by file name.
func Map() map[string][]byte {
	out := map[string][]byte{}
	for _, name := range []string{
		"example_1_news_article.png",
		"example_3_article.html",
		"example_4_page_tree.json",
		"example_6_memo.txt",
	} {
		if data, err := files.ReadFile(name); err == nil {
			out[name] = data
		}
	}
	return out
}

// Names returns the demo file names in a stable order.
func Names() []string {
	return []string{
		"example_1_news_article.png",
		"example_3_article.html",
		"example_4_page_tree.json",
		"example_6_memo.txt",
	}
}

// SampleNewsPNG returns the bundled news-page image (the /api/sample
// payload of the HTTP server).
func SampleNewsPNG() []byte {
	data, err := files.ReadFile("example_1_news_article.png")
	if err != nil {
		return nil
	}
	return data
}

// SampleNewsImage decodes the bundled sample as a tightly packed RGBA
// image (used by the selftest's engine smoke check).
func SampleNewsImage() *image.RGBA {
	data := SampleNewsPNG()
	if data == nil {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return imaging.ToRGBA(img)
}
