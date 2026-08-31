//go:build test

package utils

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// pngHeader crafts a PNG containing only a signature and IHDR chunk with the
// given dimensions. DecodeConfig parses exactly this much, so the helper lets
// tests probe the dimension guard without a real pixel payload.
func pngHeader(width, height uint32) []byte {
	buf := bytes.NewBuffer([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	chunk := make([]byte, 4+4+13)
	copy(chunk, []byte{0, 0, 0, 13})
	copy(chunk[4:], "IHDR")
	chunk[8], chunk[9], chunk[10], chunk[11] = byte(width>>24), byte(width>>16), byte(width>>8), byte(width)
	chunk[12], chunk[13], chunk[14], chunk[15] = byte(height>>24), byte(height>>16), byte(height>>8), byte(height)
	chunk[16] = 8 // bit depth
	chunk[17] = 2 // truecolor
	crc := crc32.ChecksumIEEE(chunk[4:])
	buf.Write(chunk)
	buf.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	return buf.Bytes()
}

func TestEnsureImageDimensionsBoundedAcceptsSmallImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	r := bytes.NewReader(encoded.Bytes())
	if err := EnsureImageDimensionsBounded(r, 25_000_000); err != nil {
		t.Fatalf("bounded check rejected a small image: %v", err)
	}
	// The reader must be rewound so callers can decode without reopening.
	if _, _, err := image.Decode(r); err != nil {
		t.Fatalf("decode after bounds check: %v", err)
	}
}

func TestEnsureImageDimensionsBoundedRejectsPixelBombHeader(t *testing.T) {
	r := bytes.NewReader(pngHeader(60_000, 60_000))
	err := EnsureImageDimensionsBounded(r, 25_000_000)
	if err == nil {
		t.Fatal("bounded check accepted a 3.6 gigapixel header")
	}
	if !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("error = %v, want the pixel-limit message", err)
	}
}
