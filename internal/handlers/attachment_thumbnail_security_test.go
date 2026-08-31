//go:build test

package handlers

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateThumbnailRejectsOversizedDeclaredDimensions(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png fixture: %v", err)
	}
	data := encoded.Bytes()
	binary.BigEndian.PutUint32(data[16:20], 65_535)
	binary.BigEndian.PutUint32(data[20:24], 65_535)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))

	path := filepath.Join(t.TempDir(), "pixel-bomb.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write png fixture: %v", err)
	}

	handler := &AttachmentHandler{attachmentPath: t.TempDir()}
	thumbnailPath, err := handler.generateThumbnail(path, "pixel-bomb.png")
	if !errors.Is(err, errThumbnailSourceTooLarge) {
		t.Fatalf("generateThumbnail error = %v, want errThumbnailSourceTooLarge", err)
	}
	if thumbnailPath != "" {
		t.Fatalf("thumbnail path = %q, want empty", thumbnailPath)
	}
}
