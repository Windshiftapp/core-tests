package services

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
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

// TestGenerateAttachmentThumbnailRejectsPixelBomb pins the pixel-bomb guard:
// a file declaring oversized dimensions is rejected before image.Decode can
// allocate memory proportional to those dimensions.
func TestGenerateAttachmentThumbnailRejectsPixelBomb(t *testing.T) {
	dir := t.TempDir()
	bombPath := pngHeaderWithDimensions(t, dir, 60_000, 60_000)

	_, err := generateAttachmentThumbnail(bombPath, "bomb.png")
	if err == nil {
		t.Fatal("thumbnail generation accepted a 3.6 gigapixel header")
	}
	if !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("error = %v, want the pixel-limit message", err)
	}
}

func TestGenerateAttachmentThumbnailCreatesThumbnailForSmallImage(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	img.Set(1, 1, color.RGBA{R: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := filepath.Join(dir, "small.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}

	thumbPath, err := generateAttachmentThumbnail(path, "small.png")
	if err != nil {
		t.Fatalf("generateAttachmentThumbnail: %v", err)
	}
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail missing: %v", err)
	}
}
