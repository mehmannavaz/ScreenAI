// Package pb implements exactly enough of the protocol-buffers wire format
// to decode chrome_screen_ai.VisualAnnotation (the OCR output of the Screen
// AI library) and encode screenai.ViewHierarchy (the Main Content Extraction
// input), as defined in services/screen_ai/proto/*.proto in the Chromium
// tree. Field numbers below are taken from those .proto files; the codecs
// are hand-rolled so ScreenAI has zero generated-code dependencies.
package pb

import (
	"errors"
	"math"
)

// Wire types of the protobuf binary format.
const (
	WireVarint  = 0
	WireFixed64 = 1
	WireBytes   = 2
	WireFixed32 = 5
)

var (
	// ErrTruncated reports a structurally damaged message.
	ErrTruncated = errors.New("pb: truncated message")
	errVarint    = errors.New("pb: varint too long")
)

// ReadVarint reads a base-128 varint from b at pos and returns the value
// and the position just past it.
func ReadVarint(b []byte, pos int) (uint64, int, error) {
	var result uint64
	var shift uint
	for {
		if pos >= len(b) {
			return 0, 0, ErrTruncated
		}
		by := b[pos]
		pos++
		result |= uint64(by&0x7f) << shift
		if by&0x80 == 0 {
			return result, pos, nil
		}
		shift += 7
		if shift >= 70 {
			return 0, 0, errVarint
		}
	}
}

// AppendVarint appends the base-128 varint encoding of v to dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for {
		by := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			dst = append(dst, by|0x80)
		} else {
			return append(dst, by)
		}
	}
}

func appendKey(dst []byte, field, wire int) []byte {
	return AppendVarint(dst, uint64(field)<<3|uint64(wire))
}

// AppendVarintField appends a proto varint field (wire type 0).
func AppendVarintField(dst []byte, field int, v uint64) []byte {
	dst = appendKey(dst, field, WireVarint)
	return AppendVarint(dst, v)
}

// AppendBoolField appends a proto bool field.
func AppendBoolField(dst []byte, field int, v bool) []byte {
	return AppendVarintField(dst, field, boolToUint64(v))
}

// AppendFloatField appends a proto 32-bit float field (wire type 5).
func AppendFloatField(dst []byte, field int, v float64) []byte {
	dst = appendKey(dst, field, WireFixed32)
	var scratch [4]byte
	bits := math.Float32bits(float32(v))
	scratch[0], scratch[1], scratch[2], scratch[3] = byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24)
	return append(dst, scratch[:]...)
}

// AppendBytesField appends a length-delimited field (wire type 2) whose
// payload is raw bytes.
func AppendBytesField(dst []byte, field int, v []byte) []byte {
	dst = appendKey(dst, field, WireBytes)
	dst = AppendVarint(dst, uint64(len(v)))
	return append(dst, v...)
}

// AppendStringField appends a length-delimited UTF-8 string field.
func AppendStringField(dst []byte, field int, v string) []byte {
	dst = appendKey(dst, field, WireBytes)
	dst = AppendVarint(dst, uint64(len(v)))
	return append(dst, v...)
}

// AppendMessageField appends an embedded message field (its payload is a
// serialized sub-message).
func AppendMessageField(dst []byte, field int, payload []byte) []byte {
	return AppendBytesField(dst, field, payload)
}

// AppendPackedInt32Field appends a packed repeated varint field.
func AppendPackedInt32Field(dst []byte, field int, values []int) []byte {
	payload := make([]byte, 0, len(values)*2)
	for _, v := range values {
		payload = AppendVarint(payload, uint64(v))
	}
	return AppendBytesField(dst, field, payload)
}

// Field is one decoded field of a serialized message. Exactly the member
// matching Wire is populated.
type Field struct {
	Num     int    // field number
	Wire    int    // wire type
	Varint  uint64 // value when Wire == WireVarint
	Fixed32 uint32 // value when Wire == WireFixed32 (raw bits)
	Fixed64 uint64 // value when Wire == WireFixed64 (raw bits)
	Bytes   []byte // payload when Wire == WireBytes
}

// ForEachField walks the fields of a serialized message in order and calls
// fn for each one. Returning false from fn stops the walk. Structural
// damage (truncated fields, unsupported wire types) is reported as an
// error, mirroring how strict decoders reject damaged input.
func ForEachField(b []byte, fn func(f Field) bool) error {
	pos := 0
	for pos < len(b) {
		key, p, err := ReadVarint(b, pos)
		if err != nil {
			return err
		}
		pos = p
		f := Field{Num: int(key >> 3), Wire: int(key & 7)}
		switch f.Wire {
		case WireVarint:
			v, p, err := ReadVarint(b, pos)
			if err != nil {
				return err
			}
			f.Varint, pos = v, p
		case WireFixed64:
			if pos+8 > len(b) {
				return ErrTruncated
			}
			f.Fixed64 = le64(b[pos : pos+8])
			pos += 8
		case WireBytes:
			ln, p, err := ReadVarint(b, pos)
			if err != nil {
				return err
			}
			if p+int(ln) > len(b) {
				return ErrTruncated
			}
			f.Bytes, pos = b[p:p+int(ln)], p+int(ln)
		case WireFixed32:
			if pos+4 > len(b) {
				return ErrTruncated
			}
			f.Fixed32 = uint32(le32(b[pos : pos+4]))
			pos += 4
		default:
			return errors.New("pb: unsupported wire type")
		}
		if !fn(f) {
			return nil
		}
	}
	return nil
}

func boolToUint64(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func le32(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
}

func le64(b []byte) uint64 {
	return le32(b) | le32(b[4:])<<32
}
