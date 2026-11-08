// Package pipeline runs the Screen AI engines over any supported input:
// classify the bytes (image / page / tree / text), run the real OCR on
// images, build the a11y view hierarchy, and run the real Screen2x
// main-content model.
package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mehmannavaz/ScreenAI/internal/imaging"
	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// Kind classifies an input.
type Kind string

// Input kinds.
const (
	KindImage   Kind = "image"
	KindPage    Kind = "page"
	KindTree    Kind = "tree"
	KindText    Kind = "text"
	KindPDF     Kind = "pdf"     // the document itself — expanded into pages by ExpandPDF
	KindPDFPage Kind = "pdfpage" // one page of it, with the synthesized annotation
)

// Extension sets used as format hints.
var (
	ImageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".jpe": true, ".gif": true, ".bmp": true, ".tga": true, ".tpic": true, ".ppm": true, ".pgm": true, ".pbm": true, ".pnm": true, ".pam": true}
	PageExts  = map[string]bool{".html": true, ".htm": true, ".xhtml": true}
	TreeExts  = map[string]bool{".json": true}
	TextExts  = map[string]bool{".txt": true, ".text": true, ".md": true, ".markdown": true}
	PDFExts   = map[string]bool{".pdf": true}
)

// ErrUnsupportedInput reports input bytes in a format this tool does not
// understand.
var ErrUnsupportedInput = fmt.Errorf("pipeline: unsupported input format")

// Input is a classified input: bytes plus everything the pipeline needs.
type Input struct {
	Name   string // display name / original path
	Data   []byte
	Hint   string // filename hint for sniffing
	Kind   Kind   // image | page | tree | text | pdf | pdfpage
	Format string // upper-cased format name for reports

	// pdfpage fields (set by ExpandPDF): the page's text layer as a
	// synthesized OCR annotation plus the page geometry it was built
	// from. Boxes are page points with a top-left origin.
	Ann       *pb.Annotation
	Page      int // 1-based page number
	PageCount int // pages in the source document
	Width     int // page width in points
	Height    int // page height in points
}

var htmlTagRe = regexp.MustCompile(`(?i)</?[a-z][a-z0-9-]*(\s|>|/)`)

// looksLikeHTML mirrors the tolerant HTML detector: doctype / <html
// tag, or three recognizable tags in the first 2 KB.
func looksLikeHTML(text string) bool {
	head := strings.ToLower(firstN(text, 2048))
	if strings.Contains(head, "<!doctype html") || strings.Contains(head, "<html") {
		return true
	}
	return len(htmlTagRe.FindAllString(head, -1)) >= 3
}

func looksLikeJSON(text string) bool {
	s := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

// looksLikeText rejects binary garbage that happens to decode as UTF-8:
// at least 70% of the first 512 runes must be printable. Control soup
// like "\x00\x01\x02garbage" must NOT be classified as a text document.
func looksLikeText(text string) bool {
	if text == "" {
		return false
	}
	runes := []rune(firstN(text, 512))
	printable := 0
	for _, r := range runes {
		if unicode.IsPrint(r) || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			printable++
		}
	}
	return float64(printable)/float64(len(runes)) >= 0.70
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// cut at a rune boundary
	for i := n; i > n-4 && i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i]
		}
	}
	return s[:n]
}

// Load reads a path or http(s) URL and classifies it.
func Load(source string) (*Input, error) {
	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		data, ctype, err := fetch(source)
		if err != nil {
			return nil, err
		}
		hint := path.Base(u.Path)
		if hint == "/" || hint == "." || hint == "" {
			hint = "download"
		}
		return Classify(data, hint, ctype, source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	return Classify(data, path.Base(source), "", source)
}

func fetch(rawURL string) ([]byte, string, error) {
	client := &http.Client{Timeout: 0} // big files; no total timeout
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("pipeline: GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Classify detects the format of data (image/page/tree/text) by
// sniffing, with the filename hint and content type as tie-breakers.
func Classify(data []byte, hint, contentType, source string) (*Input, error) {
	if source == "" {
		source = hint
	}
	if source == "" {
		source = "input"
	}
	ext := strings.ToLower(path.Ext(hint))

	if format := imaging.Sniff(data, hint); format != "" {
		return &Input{Name: source, Data: data, Hint: hint, Kind: KindImage, Format: strings.ToUpper(format)}, nil
	}

	if PDFExts[ext] || bytes.HasPrefix(data, []byte("%PDF-")) {
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return nil, fmt.Errorf("%w: %s: extension suggests a PDF but the %%PDF- header is missing", ErrUnsupportedInput, hint)
		}
		return &Input{Name: source, Data: data, Hint: hint, Kind: KindPDF, Format: "PDF"}, nil
	}

	text := ""
	if utf8.Valid(data) {
		text = string(data)
	}

	if ImageExts[ext] {
		return nil, fmt.Errorf("%w: %s: extension suggests an image but the content is not a supported image format (WebP / progressive JPEG are not supported — convert to PNG first)", ErrUnsupportedInput, hint)
	}
	if PageExts[ext] || (text != "" && looksLikeHTML(text)) || strings.Contains(strings.ToLower(contentType), "html") {
		if text == "" {
			return nil, fmt.Errorf("%w: %s: not valid UTF-8 HTML", ErrUnsupportedInput, hint)
		}
		return &Input{Name: source, Data: data, Hint: hint, Kind: KindPage, Format: "HTML"}, nil
	}
	if TreeExts[ext] || (text != "" && looksLikeJSON(text)) || strings.Contains(strings.ToLower(contentType), "json") {
		if text == "" {
			return nil, fmt.Errorf("%w: %s: not valid UTF-8 JSON", ErrUnsupportedInput, hint)
		}
		if !json.Valid(data) {
			return nil, fmt.Errorf("%w: %s: invalid JSON", ErrUnsupportedInput, hint)
		}
		return &Input{Name: source, Data: data, Hint: hint, Kind: KindTree, Format: "JSON"}, nil
	}
	if text != "" && looksLikeText(text) {
		return &Input{Name: source, Data: data, Hint: hint, Kind: KindText, Format: "text"}, nil
	}
	return nil, fmt.Errorf("%w: %s: unrecognized format (images: PNG/JPEG/GIF/BMP/TGA/PPM; pages: HTML; trees: JSON; text: TXT)", ErrUnsupportedInput, hint)
}
