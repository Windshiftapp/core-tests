package logbook

import (
	"bytes"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngHeaderWithDimensions writes a PNG whose IHDR declares the given
// dimensions without any pixel data, enough for a DecodeConfig-based guard.
func pngHeaderWithDimensions(t *testing.T, dir string, width, height uint32) string {
	t.Helper()
	buf := bytes.NewBuffer([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	chunk := make([]byte, 4+4+13)
	copy(chunk, []byte{0, 0, 0, 13})
	copy(chunk[4:], "IHDR")
	chunk[8], chunk[9], chunk[10], chunk[11] = byte(width>>24), byte(width>>16), byte(width>>8), byte(width)
	chunk[12], chunk[13], chunk[14], chunk[15] = byte(height>>24), byte(height>>16), byte(height>>8), byte(height)
	chunk[16] = 8
	chunk[17] = 2
	crc := crc32.ChecksumIEEE(chunk[4:])
	buf.Write(chunk)
	buf.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	path := filepath.Join(dir, "bomb.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write bomb header: %v", err)
	}
	return path
}

// TestDecodeImageRejectsPixelBomb pins the pixel-bomb guard on ingested
// documents: oversized declared dimensions are rejected before image.Decode
// allocates memory proportional to those dimensions.
func TestDecodeImageRejectsPixelBomb(t *testing.T) {
	dir := t.TempDir()
	bombPath := pngHeaderWithDimensions(t, dir, 60_000, 60_000)

	_, err := decodeImage(bombPath)
	if err == nil {
		t.Fatal("decodeImage accepted a 3.6 gigapixel header")
	}
	if !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("error = %v, want the pixel-limit message", err)
	}
}
