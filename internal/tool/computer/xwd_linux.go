//go:build linux

package computer

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"math/bits"
)

const (
	xwdHeaderBytes = 25 * 4
	xwdFileVersion = 7
	xwdZPixmap     = 2
	xwdLSBFirst    = 0
	xwdMSBFirst    = 1
)

// decodeXWD decodes the TrueColor/DirectColor ZPixmap emitted by the X11 xwd
// utility. Supporting xwd gives Linux desktops a dependency-light screenshot
// fallback without shell pipelines or ImageMagick.
func decodeXWD(data []byte) (*image.NRGBA, error) {
	if len(data) < xwdHeaderBytes {
		return nil, fmt.Errorf("XWD header is truncated")
	}
	header := make([]uint32, 25)
	for index := range header {
		header[index] = binary.BigEndian.Uint32(data[index*4 : index*4+4])
	}
	headerSize := uint64(header[0])
	width, height := uint64(header[4]), uint64(header[5])
	byteOrder := header[7]
	bitsPerPixel := header[11]
	bytesPerLine := uint64(header[12])
	ncolors := uint64(header[19])
	if header[1] != xwdFileVersion || header[2] != xwdZPixmap {
		return nil, fmt.Errorf("unsupported XWD version %d or pixmap format %d", header[1], header[2])
	}
	if headerSize < xwdHeaderBytes || headerSize > uint64(len(data)) {
		return nil, fmt.Errorf("invalid XWD header size %d", headerSize)
	}
	if width == 0 || height == 0 || width > 200000 || height > 200000 || width*height > 200000000 {
		return nil, fmt.Errorf("invalid XWD dimensions %dx%d", width, height)
	}
	if byteOrder != xwdLSBFirst && byteOrder != xwdMSBFirst {
		return nil, fmt.Errorf("unsupported XWD byte order %d", byteOrder)
	}
	if bitsPerPixel != 16 && bitsPerPixel != 24 && bitsPerPixel != 32 {
		return nil, fmt.Errorf("unsupported XWD bits per pixel %d", bitsPerPixel)
	}
	if header[13] != 4 && header[13] != 5 {
		return nil, fmt.Errorf("unsupported XWD visual class %d", header[13])
	}
	if header[6] != 0 {
		return nil, fmt.Errorf("unsupported XWD x offset %d", header[6])
	}
	pixelBytes := uint64(bitsPerPixel / 8)
	if bytesPerLine < width*pixelBytes {
		return nil, fmt.Errorf("invalid XWD row stride %d", bytesPerLine)
	}
	colorTableBytes := ncolors * 12
	pixelOffset := headerSize + colorTableBytes
	if pixelOffset < headerSize || pixelOffset > uint64(len(data)) || height > (uint64(len(data))-pixelOffset)/bytesPerLine {
		return nil, fmt.Errorf("XWD pixel data is truncated")
	}

	redMask, greenMask, blueMask := header[14], header[15], header[16]
	if redMask == 0 || greenMask == 0 || blueMask == 0 {
		return nil, fmt.Errorf("XWD image has invalid RGB masks")
	}
	result := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := uint64(0); y < height; y++ {
		row := pixelOffset + y*bytesPerLine
		for x := uint64(0); x < width; x++ {
			offset := row + x*pixelBytes
			pixel := decodeXWDPixel(data[offset:offset+pixelBytes], byteOrder)
			result.SetNRGBA(int(x), int(y), color.NRGBA{
				R: xwdComponent(pixel, redMask),
				G: xwdComponent(pixel, greenMask),
				B: xwdComponent(pixel, blueMask),
				A: 0xff,
			})
		}
	}
	return result, nil
}

func decodeXWDPixel(data []byte, byteOrder uint32) uint32 {
	var value uint32
	if byteOrder == xwdLSBFirst {
		for index := len(data) - 1; index >= 0; index-- {
			value = value<<8 | uint32(data[index])
		}
		return value
	}
	for _, item := range data {
		value = value<<8 | uint32(item)
	}
	return value
}

func xwdComponent(pixel, mask uint32) uint8 {
	shift := bits.TrailingZeros32(mask)
	maximum := mask >> shift
	value := (pixel & mask) >> shift
	return uint8((uint64(value)*255 + uint64(maximum)/2) / uint64(maximum))
}
