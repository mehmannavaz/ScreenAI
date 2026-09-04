package imaging

import (
	"fmt"
	"image"
)

// decodePPM decodes the netpbm family: PBM (P1/P4 bitmaps), PGM
// (P2/P5 graymaps) and PPM (P3/P6 pixmaps), in both ASCII and binary
// flavors. Maxval up to 65535 is supported (16-bit samples are scaled
// down to 8-bit).
func decodePPM(data []byte) (*image.RGBA, error) {
	if len(data) < 2 || data[0] != 'P' {
		return nil, fmt.Errorf("imaging: not a netpbm file")
	}
	kind := int(data[1] - '0')
	if kind < 1 || kind > 6 {
		return nil, fmt.Errorf("imaging: netpbm magic P%d not supported", kind)
	}
	pos := 2
	// P1..P3 are the ASCII flavors, P4..P6 the binary ones.
	ascii := kind <= 3

	// Header: whitespace/comment separated width, height, maxval.
	nextToken := func() (string, error) {
		for {
			if pos >= len(data) {
				return "", fmt.Errorf("imaging: netpbm header truncated")
			}
			c := data[pos]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				pos++
				continue
			}
			if c == '#' { // comment until end of line
				for pos < len(data) && data[pos] != '\n' {
					pos++
				}
				continue
			}
			break
		}
		start := pos
		for pos < len(data) && data[pos] != ' ' && data[pos] != '\t' && data[pos] != '\r' && data[pos] != '\n' {
			pos++
		}
		return string(data[start:pos]), nil
	}
	nextInt := func() (int, error) {
		tok, err := nextToken()
		if err != nil {
			return 0, err
		}
		n := 0
		for i := 0; i < len(tok); i++ {
			c := tok[i]
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("imaging: netpbm header field %q is not a number", tok)
			}
			n = n*10 + int(c-'0')
		}
		return n, nil
	}

	w, err := nextInt()
	if err != nil {
		return nil, err
	}
	h, err := nextInt()
	if err != nil {
		return nil, err
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("imaging: netpbm has degenerate dimensions %dx%d", w, h)
	}

	maxval := 1
	if kind != 1 && kind != 4 {
		maxval, err = nextInt()
		if err != nil {
			return nil, err
		}
		if maxval <= 0 || maxval > 65535 {
			return nil, fmt.Errorf("imaging: netpbm maxval %d out of range", maxval)
		}
	}
	if ascii {
		// advance past the single whitespace after the last header field
		if pos < len(data) {
			pos++
		}
	} else {
		// binary: exactly one whitespace byte after maxval
		pos++
	}

	scale := func(v int) uint8 {
		if maxval == 1 {
			if v != 0 {
				return 0 // PBM: 1 = black
			}
			return 255
		}
		if maxval == 255 {
			return uint8(v)
		}
		return uint8((v*255 + maxval/2) / maxval)
	}

	out := image.NewRGBA(image.Rect(0, 0, w, h))
	put := func(x, y int, r, g, b uint8) {
		i := y*out.Stride + x*4
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = r, g, b, 255
	}

	if ascii {
		readInt := func() (int, error) {
			for {
				for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\r' || data[pos] == '\n') {
					pos++
				}
				for pos < len(data) && data[pos] == '#' {
					for pos < len(data) && data[pos] != '\n' {
						pos++
					}
				}
				if pos >= len(data) || (data[pos] >= '0' && data[pos] <= '9') {
					break
				}
				return 0, fmt.Errorf("imaging: netpbm ASCII data corrupted")
			}
			if pos >= len(data) {
				return 0, fmt.Errorf("imaging: netpbm ASCII data truncated")
			}
			n := 0
			for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
				n = n*10 + int(data[pos]-'0')
				pos++
			}
			return n, nil
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				switch {
				case kind == 1:
					v, err := readInt()
					if err != nil {
						return nil, err
					}
					g := scale(v)
					put(x, y, g, g, g)
				case kind == 2:
					v, err := readInt()
					if err != nil {
						return nil, err
					}
					g := scale(v)
					put(x, y, g, g, g)
				default: // kind == 3
					r, err := readInt()
					if err != nil {
						return nil, err
					}
					g, err := readInt()
					if err != nil {
						return nil, err
					}
					b, err := readInt()
					if err != nil {
						return nil, err
					}
					put(x, y, scale(r), scale(g), scale(b))
				}
			}
		}
		return out, nil
	}

	// Binary variants.
	sampleBytes := 1
	if maxval > 255 {
		sampleBytes = 2
	}
	readSample := func() (int, error) {
		if pos+sampleBytes > len(data) {
			return 0, fmt.Errorf("imaging: netpbm binary data truncated")
		}
		v := int(data[pos])
		if sampleBytes == 2 {
			v = v<<8 | int(data[pos+1])
		}
		pos += sampleBytes
		return v, nil
	}
	switch kind {
	case 4: // packed bits, MSB first, rows padded to full bytes
		rowBytes := (w + 7) / 8
		for y := 0; y < h; y++ {
			if pos+rowBytes > len(data) {
				return nil, fmt.Errorf("imaging: netpbm P4 data truncated")
			}
			row := data[pos : pos+rowBytes]
			pos += rowBytes
			for x := 0; x < w; x++ {
				bit := row[x/8] >> (7 - uint(x%8)) & 1
				g := scale(int(bit))
				put(x, y, g, g, g)
			}
		}
	case 5:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v, err := readSample()
				if err != nil {
					return nil, err
				}
				g := scale(v)
				put(x, y, g, g, g)
			}
		}
	case 6:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, err := readSample()
				if err != nil {
					return nil, err
				}
				g, err := readSample()
				if err != nil {
					return nil, err
				}
				b, err := readSample()
				if err != nil {
					return nil, err
				}
				put(x, y, scale(r), scale(g), scale(b))
			}
		}
	}
	return out, nil
}
