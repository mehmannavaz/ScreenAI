package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestSniff(t *testing.T) {
	cases := []struct {
		data []byte
		hint string
		want string
	}{
		{[]byte("\x89PNG\r\n\x1a\n...."), "x", "png"},
		{[]byte("\xff\xd8\xff\xe0...."), "x", "jpeg"},
		{[]byte("GIF89a...."), "x", "gif"},
		{[]byte("BM...."), "x", "bmp"},
		{[]byte("P6\n2 2\n255\n"), "x", "ppm"},
		{[]byte("anything"), "photo.TGA", "tga"},
		{make([]byte, 64), "photo.tga", "tga"}, // no footer: hint decides
		{[]byte("\x00\x01\x02\x03"), "x.bin", ""},
	}
	for _, c := range cases {
		if got := Sniff(c.data, c.hint); got != c.want {
			t.Fatalf("Sniff(%q, %q) = %q, want %q", c.data[:4], c.hint, got, c.want)
		}
	}
}

func TestDecodeTGAUncompressed24(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 2, 0, 24, 0})
	b.Write([]byte{0, 0, 255, 0, 255, 0})     // bottom row: red, green (BGR)
	b.Write([]byte{255, 0, 0, 255, 255, 255}) // top row: blue, white
	img, err := Decode(b.Bytes(), "f.tga")
	if err != nil {
		t.Fatal(err)
	}
	want := [4][4]uint8{
		{0, 0, 255, 255}, {255, 255, 255, 255},
		{255, 0, 0, 255}, {0, 255, 0, 255},
	}
	checkPixels(t, img, want)
}

func TestDecodeTGARLE32(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 2, 0, 32, 0x20}) // top-down
	b.Write([]byte{0x82, 0, 255, 255, 255})                                    // run of 2 yellow
	b.Write([]byte{0x02, 255, 0, 0, 128, 0, 0, 0, 255})                        // raw 2 pixels
	img, err := Decode(b.Bytes(), "f.tga")
	if err != nil {
		t.Fatal(err)
	}
	want := [4][4]uint8{
		{255, 255, 0, 255}, {255, 255, 0, 255},
		{0, 0, 255, 128}, {0, 0, 0, 255},
	}
	checkPixels(t, img, want)
}

func TestDecodeTGAPalette8(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0, 1, 1, 0, 0, 2, 0, 24, 0, 0, 0, 0, 2, 0, 1, 0, 8, 0x20})
	b.Write([]byte{0, 0, 255, 255, 0, 0}) // palette: red, blue
	b.Write([]byte{1, 0})                 // pixels: blue, red
	img, err := Decode(b.Bytes(), "f.tga")
	if err != nil {
		t.Fatal(err)
	}
	want := [4][4]uint8{
		{0, 0, 255, 255}, {255, 0, 0, 255},
	}
	checkPixels(t, img, want)
}

func TestDecodePPMBinary(t *testing.T) {
	data := []byte("P6\n2 2\n255\n\xff\x00\x00\x00\xff\x00\x00\x00\xff\xff\xff\xff")
	img, err := Decode(data, "f.ppm")
	if err != nil {
		t.Fatal(err)
	}
	want := [4][4]uint8{
		{255, 0, 0, 255}, {0, 255, 0, 255},
		{0, 0, 255, 255}, {255, 255, 255, 255},
	}
	checkPixels(t, img, want)
}

func TestDecodePPMAscii(t *testing.T) {
	data := []byte("P2\n# comment\n2 2\n255\n0 128\n255 64")
	img, err := Decode(data, "f.pgm")
	if err != nil {
		t.Fatal(err)
	}
	want := [4][4]uint8{
		{0, 0, 0, 255}, {128, 128, 128, 255},
		{255, 255, 255, 255}, {64, 64, 64, 255},
	}
	checkPixels(t, img, want)
}

func TestEncodePNGBinary(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 255
	}
	img.SetRGBA(0, 0, color.RGBA{255, 255, 255, 255})
	img.SetRGBA(2, 1, color.RGBA{255, 0, 0, 255})
	pngBytes, err := EncodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	// the stdlib decoder must accept it (proves it is a real PNG)
	back, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	rgba := ToRGBA(back)
	if rgba.Stride != 3*4 {
		t.Fatalf("stride %d — not tightly packed", rgba.Stride)
	}
	if rgba.At(0, 0) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("white lost: %v", rgba.At(0, 0))
	}
}

func TestResizeMaxBox(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	out := ResizeMax(img, 2)
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 2 {
		t.Fatalf("dims %dx%d", out.Bounds().Dx(), out.Bounds().Dy())
	}
	// every 2x2 block has two white and two black pixels -> mean 127
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			r, g, b, _ := out.At(x, y).RGBA()
			if r>>8 != 127 || g>>8 != 127 || b>>8 != 127 {
				t.Fatalf("box average wrong at (%d,%d): %d %d %d", x, y, r>>8, g>>8, b>>8)
			}
		}
	}
	if same := ResizeMax(img, 4); same != img {
		t.Fatal("ResizeMax must return the original when it already fits")
	}
}

func checkPixels(t *testing.T, img *image.RGBA, want [4][4]uint8) {
	t.Helper()
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	k := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*4
			got := [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
			if got != want[k] {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want[k])
			}
			k++
		}
	}
}
