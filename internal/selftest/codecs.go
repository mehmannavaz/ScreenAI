package selftest

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/mehmannavaz/ScreenAI/internal/imaging"
)

// testTGAUncompressed decodes a 2x2 uncompressed 24-bit bottom-up TGA.
//
//	red   green
//	blue  white
func testTGAUncompressed() error {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 2, 0, 24, 0}) // header, top-down bit NOT set
	// row 0 (bottom of the image): red, green — TGA stores BGR
	b.Write([]byte{0, 0, 255, 0, 255, 0})
	// row 1 (top): blue, white
	b.Write([]byte{255, 0, 0, 255, 255, 255})

	img, err := imaging.Decode(b.Bytes(), "fixture.tga")
	if err != nil {
		return err
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		return errSize(img)
	}
	// bottom-up: data row 1 is displayed as image row 0
	if err := wantPixel("tga24", img, 0, 0, 0, 0, 255, 255); err != nil { // blue
		return err
	}
	if err := wantPixel("tga24", img, 1, 0, 255, 255, 255, 255); err != nil { // white
		return err
	}
	if err := wantPixel("tga24", img, 0, 1, 255, 0, 0, 255); err != nil { // red
		return err
	}
	if err := wantPixel("tga24", img, 1, 1, 0, 255, 0, 255); err != nil { // green
		return err
	}
	return nil
}

// testTGARLE decodes a 2x2 RLE 32-bit top-down TGA with one run packet
// and one raw packet.
func testTGARLE() error {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 2, 0, 32, 0x20}) // header, top-down (0x20)
	// run packet: 2x opaque yellow BGRA (count | 0x80 = run marker)
	b.Write([]byte{0x82}) // run of 2
	b.Write([]byte{0, 255, 255, 255})
	// raw packet: 2 pixels
	b.Write([]byte{0x02})           // raw, 2 pixels
	b.Write([]byte{255, 0, 0, 128}) // translucent blue
	b.Write([]byte{0, 0, 0, 255})   // opaque black

	img, err := imaging.Decode(b.Bytes(), "fixture.tga")
	if err != nil {
		return err
	}
	if err := wantPixel("tga-rle", img, 0, 0, 255, 255, 0, 255); err != nil {
		return err
	}
	if err := wantPixel("tga-rle", img, 1, 0, 255, 255, 0, 255); err != nil {
		return err
	}
	if err := wantPixel("tga-rle", img, 0, 1, 0, 0, 255, 128); err != nil {
		return err
	}
	if err := wantPixel("tga-rle", img, 1, 1, 0, 0, 0, 255); err != nil {
		return err
	}
	return nil
}

// testTGAPalette decodes a 2x1 paletted TGA (image type 1) with a
// 2-entry 24-bit color map.
func testTGAPalette() error {
	var b bytes.Buffer
	// id len 0, color map present, type 1, cmap first 0, cmap len 2, cmap bpp 24
	b.Write([]byte{0, 1, 1, 0, 0, 2, 0, 24, 0, 0, 0, 0, 2, 0, 1, 0, 8, 0x20})
	// color map: entry 0 = red (BGR), entry 1 = blue
	b.Write([]byte{0, 0, 255, 255, 0, 0})
	// pixels: index 1, index 0
	b.Write([]byte{1, 0})

	img, err := imaging.Decode(b.Bytes(), "fixture.tga")
	if err != nil {
		return err
	}
	if err := wantPixel("tga-pal", img, 0, 0, 0, 0, 255, 255); err != nil {
		return err
	}
	if err := wantPixel("tga-pal", img, 1, 0, 255, 0, 0, 255); err != nil {
		return err
	}
	return nil
}

// testPPM6 decodes a 2x2 binary PPM.
func testPPM6() error {
	data := []byte("P6\n2 2\n255\n" +
		"\xff\x00\x00\x00\xff\x00" +
		"\x00\x00\xff\xff\xff\xff")
	img, err := imaging.Decode(data, "fixture.ppm")
	if err != nil {
		return err
	}
	if err := wantPixel("ppm6", img, 0, 0, 255, 0, 0, 255); err != nil {
		return err
	}
	if err := wantPixel("ppm6", img, 1, 0, 0, 255, 0, 255); err != nil {
		return err
	}
	if err := wantPixel("ppm6", img, 0, 1, 0, 0, 255, 255); err != nil {
		return err
	}
	if err := wantPixel("ppm6", img, 1, 1, 255, 255, 255, 255); err != nil {
		return err
	}
	return nil
}

// testPPM2 decodes a 2x2 ASCII PGM with a comment in the header.
func testPPM2() error {
	data := []byte("P2\n# a comment\n2 2\n255\n0 128\n255 64")
	img, err := imaging.Decode(data, "fixture.pgm")
	if err != nil {
		return err
	}
	if err := wantPixel("ppm2", img, 0, 0, 0, 0, 0, 255); err != nil {
		return err
	}
	if err := wantPixel("ppm2", img, 1, 0, 128, 128, 128, 255); err != nil {
		return err
	}
	if err := wantPixel("ppm2", img, 0, 1, 255, 255, 255, 255); err != nil {
		return err
	}
	if err := wantPixel("ppm2", img, 1, 1, 64, 64, 64, 255); err != nil {
		return err
	}
	return nil
}

// testPNGRoundtrip encodes an RGBA image as PNG and decodes it back.
func testPNGRoundtrip() error {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := range img.Pix { // opaque black background
		img.Pix[i] = 0
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	img.SetRGBA(0, 0, colorWhite())
	img.SetRGBA(2, 1, colorRed())
	pngBytes, err := imaging.EncodePNG(img)
	if err != nil {
		return err
	}
	if pngBytes[0] != 0x89 || pngBytes[1] != 'P' {
		return errPNGMagic()
	}
	back, err := imaging.Decode(pngBytes, "roundtrip.png")
	if err != nil {
		return err
	}
	if err := wantPixel("png", back, 0, 0, 255, 255, 255, 255); err != nil {
		return err
	}
	if err := wantPixel("png", back, 2, 1, 255, 0, 0, 255); err != nil {
		return err
	}
	if err := wantPixel("png", back, 1, 0, 0, 0, 0, 255); err != nil {
		return err
	}
	// the stdlib decoder must accept it too (proves it is a real PNG)
	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		return err
	}
	return nil
}

// testDownscale box-averages a 4x4 checker into 2x2.
func testDownscale() error {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)%2 == 0 {
				img.SetRGBA(x, y, colorWhite())
			} else {
				img.SetRGBA(x, y, colorBlack())
			}
		}
	}
	out := imaging.ResizeMax(img, 2)
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 2 {
		return errSize(out)
	}
	// every 2x2 block has exactly two white and two black pixels
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			if err := wantPixel("box", out, x, y, 127, 127, 127, 255); err != nil {
				return err
			}
		}
	}
	// no-op when it already fits
	if same := imaging.ResizeMax(img, 4); same != img {
		return errNoop()
	}
	return nil
}

func errSize(img *image.RGBA) error {
	return fmt.Errorf("unexpected image size %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
}

func errPNGMagic() error { return errors.New("encoded PNG lost its magic") }

func errNoop() error { return errors.New("ResizeMax must return the original image when it fits") }

func colorWhite() color.RGBA { return color.RGBA{255, 255, 255, 255} }
func colorRed() color.RGBA   { return color.RGBA{255, 0, 0, 255} }
func colorBlack() color.RGBA { return color.RGBA{0, 0, 0, 255} }
