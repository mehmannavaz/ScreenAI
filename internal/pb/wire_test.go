package pb

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"
)

func TestVarintRoundtrip(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 300, 1 << 20, 1<<32 - 1, 1<<63 - 1, math.MaxUint64}
	for _, v := range values {
		enc := AppendVarint(nil, v)
		got, n, err := ReadVarint(enc, 0)
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", v, err)
		}
		if got != v || n != len(enc) {
			t.Fatalf("roundtrip %d: got %d, consumed %d of %d", v, got, n, len(enc))
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	if _, _, err := ReadVarint([]byte{0x80}, 0); err == nil {
		t.Fatal("truncated varint must error")
	}
	// 11 continuation bytes: too long
	if _, _, err := ReadVarint(bytes.Repeat([]byte{0x80}, 11), 0); err == nil {
		t.Fatal("over-long varint must error")
	}
}

func TestForEachFieldRejectsDamage(t *testing.T) {
	bad := [][]byte{
		{0x0a},            // key without length
		{0x0a, 0x05, 'a'}, // length beyond buffer
		{0x0f},            // wire type 7 unsupported
	}
	for _, b := range bad {
		if err := ForEachField(b, func(Field) bool { return true }); err == nil {
			t.Fatalf("damaged message %v must error", b)
		}
	}
}

func TestAppendPackedInt32(t *testing.T) {
	got := AppendPackedInt32Field(nil, 4, []int{1, 2, 300})
	// field 4, wire 2, len 4, varints 1, 2, 300
	want := []byte{0x22, 0x04, 0x01, 0x02, 0xac, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("packed int32: got %x, want %x", got, want)
	}
}

func TestDecodeAnnotation(t *testing.T) {
	// One line: text "Hi", box (1,2,3x4), confidence 0.5, language "en".
	var line []byte
	line = AppendStringField(line, 3, "Hi")
	var box []byte
	box = AppendVarintField(box, 1, 1)
	box = AppendVarintField(box, 2, 2)
	box = AppendVarintField(box, 3, 3)
	box = AppendVarintField(box, 4, 4)
	line = AppendBytesField(line, 2, box)
	line = AppendVarintField(line, 5, 1)
	line = AppendFloatField(line, 10, 0.5)
	line = AppendStringField(line, 4, "en")
	ann := AppendBytesField(nil, 2, line)

	got, err := DecodeAnnotation(ann)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(got.Lines))
	}
	ln := got.Lines[0]
	if ln.Text != "Hi" || ln.BlockID != 1 || ln.Language != "en" {
		t.Fatalf("line fields wrong: %+v", ln)
	}
	if ln.Box == nil || ln.Box.X != 1 || ln.Box.Y != 2 || ln.Box.Width != 3 || ln.Box.Height != 4 {
		t.Fatalf("box wrong: %+v", ln.Box)
	}
	if ln.Confidence != 0.5 {
		t.Fatalf("confidence %v", ln.Confidence)
	}
	if got.PlainText() != "Hi" || got.WordCount() != 0 {
		t.Fatalf("text helpers wrong")
	}
}

func TestPlainTextBlockBreaks(t *testing.T) {
	ann := &Annotation{Lines: []Line{
		{Text: "a", BlockID: 0},
		{Text: "b", BlockID: 0},
		{Text: "c", BlockID: 1},
	}}
	want := "a\nb\n\nc"
	if got := ann.PlainText(); got != want {
		t.Fatalf("PlainText = %q, want %q", got, want)
	}
}

func TestEncodeViewHierarchyKnownBytes(t *testing.T) {
	// One root element (id 0, type ROOT, parent -1) with an axnode_id
	// attribute — hand-computed wire bytes. Bounding boxes are always
	// emitted (fields 6 and 8), exactly like Chromium's convertor.
	elements := []Element{{
		ID: 0, Type: UIElementRoot, ParentID: -1,
		Attrs: []Attr{Int("axnode_id", 100)},
	}}
	got := EncodeViewHierarchy(elements)
	want, _ := hex.DecodeString(
		"12" + "3e" + // field 2 (ui_elements), len 62
			"08" + "00" + // id 0
			"10" + "01" + // type 1 (ROOT)
			"18" + "ffffffffffffffffff01" + // parent -1 (10-byte varint)
			"32" + "14" + // field 6 (normalized bbox), len 20
			"0d" + "00000000" + // top
			"15" + "00000000" + // left
			"1d" + "00000000" + // bottom
			"25" + "00000000" + // right
			"3a" + "0d" + // field 7 (attributes), len 13
			"0a" + "09" + "61786e6f64655f6964" + // name "axnode_id"
			"18" + "64" + // int value 100
			"42" + "08" + // field 8 (pixel bbox), len 8
			"08" + "00" + "10" + "00" + "18" + "00" + "20" + "00") // top/left/bottom/right = 0
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestAttrKindSwitch(t *testing.T) {
	for _, a := range []Attr{
		Bool("b", true), Int("i", -1), String("s", "v"), Float("f", 1.5),
	} {
		if a.Name == "" {
			t.Fatal("unnamed attribute")
		}
	}
}
