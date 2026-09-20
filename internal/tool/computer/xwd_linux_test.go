//go:build linux

package computer

import (
	"encoding/binary"
	"testing"
)

func TestDecodeXWDTrueColorLittleEndian(t *testing.T) {
	header := make([]uint32, 25)
	header[0] = xwdHeaderBytes
	header[1] = xwdFileVersion
	header[2] = xwdZPixmap
	header[3] = 24
	header[4] = 2
	header[5] = 1
	header[7] = xwdLSBFirst
	header[11] = 32
	header[12] = 8
	header[13] = 4 // TrueColor
	header[14] = 0x00ff0000
	header[15] = 0x0000ff00
	header[16] = 0x000000ff
	data := make([]byte, xwdHeaderBytes, xwdHeaderBytes+8)
	for index, value := range header {
		binary.BigEndian.PutUint32(data[index*4:index*4+4], value)
	}
	data = append(data,
		0x00, 0x00, 0xff, 0x00, // red in little-endian XRGB
		0x00, 0xff, 0x00, 0x00, // green
	)
	decoded, err := decodeXWD(data)
	if err != nil {
		t.Fatal(err)
	}
	red := decoded.NRGBAAt(0, 0)
	green := decoded.NRGBAAt(1, 0)
	if red.R != 255 || red.G != 0 || red.B != 0 || green.R != 0 || green.G != 255 || green.B != 0 {
		t.Fatalf("decoded pixels red=%#v green=%#v", red, green)
	}
}

func TestDecodeXWDRejectsTruncatedData(t *testing.T) {
	if _, err := decodeXWD(make([]byte, xwdHeaderBytes-1)); err == nil {
		t.Fatal("decodeXWD accepted a truncated header")
	}
}
