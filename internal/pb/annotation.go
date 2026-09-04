package pb

import "math"

// Rect is chrome_screen_ai.Rect: integer pixel box plus a rotation angle.
type Rect struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Angle  float32 `json:"angle"`
}

// Symbol is chrome_screen_ai.SymbolBox.
type Symbol struct {
	Box        *Rect   `json:"box,omitempty"`
	Text       string  `json:"text"`
	Confidence float32 `json:"confidence"`
}

// Word is chrome_screen_ai.WordBox.
type Word struct {
	Symbols       []Symbol `json:"symbols,omitempty"`
	Box           *Rect    `json:"box,omitempty"`
	Text          string   `json:"text"`
	Language      string   `json:"language,omitempty"`
	HasSpaceAfter bool     `json:"has_space_after,omitempty"`
	ForegroundRGB *uint32  `json:"foreground_rgb,omitempty"`
	BackgroundRGB *uint32  `json:"background_rgb,omitempty"`
	Direction     int      `json:"direction,omitempty"`
	ContentType   int      `json:"content_type,omitempty"`
	Confidence    float32  `json:"confidence,omitempty"`
	WhitespaceBox *Rect    `json:"whitespace_box,omitempty"`
}

// Line is chrome_screen_ai.LineBox.
type Line struct {
	Words       []Word  `json:"words,omitempty"`
	Box         *Rect   `json:"box,omitempty"`
	Text        string  `json:"text"`
	Language    string  `json:"language,omitempty"`
	BlockID     int     `json:"block_id,omitempty"`
	Direction   int     `json:"direction,omitempty"`
	ContentType int     `json:"content_type,omitempty"`
	Confidence  float32 `json:"confidence,omitempty"`
	ParagraphID int     `json:"paragraph_id,omitempty"`
}

// Annotation is chrome_screen_ai.VisualAnnotation, the PerformOCR output.
// Width/Height/Source/Elapsed/DownscaledTo are filled in by the pipeline,
// not by the decoder.
type Annotation struct {
	Lines        []Line  `json:"lines"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Source       string  `json:"source"`
	Elapsed      float64 `json:"elapsed"`
	DownscaledTo []int   `json:"downscaled_to,omitempty"`
}

// ContentTypeNames maps chrome_screen_aiContentType enum values to names.
var ContentTypeNames = map[int]string{
	0: "printed-text", 1: "handwritten-text", 2: "image",
	3: "line-drawing", 4: "separator", 5: "unreadable-text",
	6: "formula", 7: "handwritten-formula", 8: "signature",
}

// DirectionNames maps chrome_screen_aiTextDirection values to names.
var DirectionNames = map[int]string{
	0: "unspecified", 1: "left-to-right", 2: "right-to-left",
	3: "top-to-bottom",
}

// DecodeAnnotation decodes a serialized chrome_screen_ai.VisualAnnotation.
// Every box is in pixels relative to the image that was passed to OCR.
func DecodeAnnotation(data []byte) (*Annotation, error) {
	ann := &Annotation{Lines: []Line{}}
	err := ForEachField(data, func(f Field) bool {
		if f.Num == 2 && f.Wire == WireBytes { // lines
			ann.Lines = append(ann.Lines, decodeLine(f.Bytes))
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return ann, nil
}

func decodeRect(b []byte) Rect {
	rect := Rect{}
	ForEachField(b, func(f Field) bool {
		switch {
		case f.Wire == WireVarint && f.Num == 1:
			rect.X = int(f.Varint)
		case f.Wire == WireVarint && f.Num == 2:
			rect.Y = int(f.Varint)
		case f.Wire == WireVarint && f.Num == 3:
			rect.Width = int(f.Varint)
		case f.Wire == WireVarint && f.Num == 4:
			rect.Height = int(f.Varint)
		case f.Wire == WireFixed32 && f.Num == 5:
			rect.Angle = math.Float32frombits(f.Fixed32)
		}
		return true
	})
	return rect
}

func decodeSymbol(b []byte) Symbol {
	sym := Symbol{}
	ForEachField(b, func(f Field) bool {
		switch {
		case f.Num == 1 && f.Wire == WireBytes:
			box := decodeRect(f.Bytes)
			sym.Box = &box
		case f.Num == 2 && f.Wire == WireBytes:
			sym.Text = string(f.Bytes)
		case f.Num == 3 && f.Wire == WireFixed32:
			sym.Confidence = math.Float32frombits(f.Fixed32)
		}
		return true
	})
	return sym
}

func decodeWord(b []byte) Word {
	w := Word{}
	ForEachField(b, func(f Field) bool {
		switch {
		case f.Num == 1 && f.Wire == WireBytes: // symbols
			w.Symbols = append(w.Symbols, decodeSymbol(f.Bytes))
		case f.Num == 2 && f.Wire == WireBytes: // bounding_box
			box := decodeRect(f.Bytes)
			w.Box = &box
		case f.Num == 3 && f.Wire == WireBytes: // utf8_string
			w.Text = string(f.Bytes)
		case f.Num == 5 && f.Wire == WireBytes: // language
			w.Language = string(f.Bytes)
		case f.Num == 6 && f.Wire == WireVarint: // has_space_after
			w.HasSpaceAfter = f.Varint != 0
		case f.Num == 10 && f.Wire == WireVarint: // foreground_rgb
			v := uint32(f.Varint)
			w.ForegroundRGB = &v
		case f.Num == 11 && f.Wire == WireVarint: // background_rgb
			v := uint32(f.Varint)
			w.BackgroundRGB = &v
		case f.Num == 12 && f.Wire == WireVarint: // direction
			w.Direction = int(f.Varint)
		case f.Num == 13 && f.Wire == WireVarint: // content_type
			w.ContentType = int(f.Varint)
		case f.Num == 15 && f.Wire == WireFixed32: // confidence
			w.Confidence = math.Float32frombits(f.Fixed32)
		case f.Num == 17 && f.Wire == WireBytes: // whitespace_bounding_box
			box := decodeRect(f.Bytes)
			w.WhitespaceBox = &box
		}
		return true
	})
	return w
}

func decodeLine(b []byte) Line {
	ln := Line{}
	ForEachField(b, func(f Field) bool {
		switch {
		case f.Num == 1 && f.Wire == WireBytes: // words
			ln.Words = append(ln.Words, decodeWord(f.Bytes))
		case f.Num == 2 && f.Wire == WireBytes: // bounding_box
			box := decodeRect(f.Bytes)
			ln.Box = &box
		case f.Num == 3 && f.Wire == WireBytes: // utf8_string
			ln.Text = string(f.Bytes)
		case f.Num == 4 && f.Wire == WireBytes: // language
			ln.Language = string(f.Bytes)
		case f.Num == 5 && f.Wire == WireVarint: // block_id
			ln.BlockID = int(f.Varint)
		case f.Num == 7 && f.Wire == WireVarint: // direction
			ln.Direction = int(f.Varint)
		case f.Num == 8 && f.Wire == WireVarint: // content_type
			ln.ContentType = int(f.Varint)
		case f.Num == 10 && f.Wire == WireFixed32: // confidence
			ln.Confidence = math.Float32frombits(f.Fixed32)
		case f.Num == 11 && f.Wire == WireVarint: // paragraph_id
			ln.ParagraphID = int(f.Varint)
		}
		return true
	})
	return ln
}

// PlainText joins line texts with newlines, adding a blank line between
// OCR blocks — the canonical plain-text rendering of an annotation.
func (a *Annotation) PlainText() string {
	out := make([]string, 0, len(a.Lines))
	prevBlock := 0
	first := true
	for i := range a.Lines {
		ln := &a.Lines[i]
		if !first && ln.BlockID != prevBlock {
			out = append(out, "")
		}
		out = append(out, ln.Text)
		prevBlock = ln.BlockID
		first = false
	}
	s := ""
	for i, part := range out {
		if i > 0 {
			s += "\n"
		}
		s += part
	}
	return s
}

// WordCount returns the total number of words across all lines.
func (a *Annotation) WordCount() int {
	n := 0
	for i := range a.Lines {
		n += len(a.Lines[i].Words)
	}
	return n
}

// AvgConfidence returns the mean line confidence (0 when no lines report
// one).
func (a *Annotation) AvgConfidence() float64 {
	var sum float64
	var n int
	for i := range a.Lines {
		if a.Lines[i].Confidence != 0 {
			sum += float64(a.Lines[i].Confidence)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// Languages returns the sorted set of non-empty line languages.
func (a *Annotation) Languages() []string {
	seen := map[string]bool{}
	var out []string
	for i := range a.Lines {
		if lang := a.Lines[i].Language; lang != "" && !seen[lang] {
			seen[lang] = true
			out = append(out, lang)
		}
	}
	// insertion sort: the set is tiny
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
