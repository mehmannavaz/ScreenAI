package engine

import "unsafe"

// Skia constants used by the packed SkBitmap.
const (
	skColorTypeRGBA8888 = 4
	skAlphaTypeOpaque   = 1
)

// skBitmap mirrors Skia's 48-byte SkBitmap value layout on 64-bit
// platforms — the exact layout PerformOCR expects by const reference:
//
//	+0x00  reserved (fID / padding)
//	+0x08  pixels pointer
//	+0x10  rowBytes
//	+0x18  colorSpace (sk_sp, null)
//	+0x20  colorType (4 = kRGBA_8888)
//	+0x24  alphaType (1 = kOpaque)
//	+0x28  width
//	+0x2c  height
type skBitmap struct {
	_          [8]byte
	pixels     uintptr
	rowBytes   uintptr
	colorSpace uintptr
	colorType  uint32
	alphaType  uint32
	width      int32
	height     int32
}

// checkSkBitmapSize verifies the Go struct layout still matches the
// 48-byte ABI. The Screen AI binary exists only for x64 platforms, so a
// mismatch (e.g. a future 32-bit build) must be reported, not ignored.
func checkSkBitmapSize() error {
	if unsafe.Sizeof(skBitmap{}) != 48 {
		return &ABIError{msg: "SkBitmap layout is not 48 bytes on this platform"}
	}
	return nil
}

func newSkBitmap(pixels *byte, width, height, rowBytes int) skBitmap {
	return skBitmap{
		pixels:    uintptr(unsafe.Pointer(pixels)),
		rowBytes:  uintptr(rowBytes),
		colorType: skColorTypeRGBA8888,
		alphaType: skAlphaTypeOpaque,
		width:     int32(width),
		height:    int32(height),
	}
}

// ABIError reports a platform whose calling convention cannot match the
// library's.
type ABIError struct{ msg string }

func (e *ABIError) Error() string { return e.msg }
