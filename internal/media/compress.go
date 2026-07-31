package media

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"github.com/nfnt/resize"
)

const (
	maxDimension = 2048
	jpegQuality  = 85
)

// compress decodes the image, downsizes if > maxDimension, re-encodes as JPEG.
// Returns the compressed bytes and the canonical extension (.jpg).
func compress(data []byte, contentType string) ([]byte, string, error) {
	var img image.Image
	var err error

	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "png"):
		img, err = png.Decode(bytes.NewReader(data))
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		img, err = jpeg.Decode(bytes.NewReader(data))
	default:
		// Try generic decode
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}

	// Downsize if needed (keeps aspect ratio)
	bounds := img.Bounds()
	w, h := uint(bounds.Dx()), uint(bounds.Dy())
	if w > maxDimension || h > maxDimension {
		if w > h {
			img = resize.Resize(maxDimension, 0, img, resize.Lanczos3)
		} else {
			img = resize.Resize(0, maxDimension, img, resize.Lanczos3)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), ".jpg", nil
}

func isImage(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "image/")
}

func isAudio(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "audio/") ||
		strings.Contains(ct, "mpeg") ||
		strings.Contains(ct, "mp4") ||
		strings.Contains(ct, "m4a") ||
		strings.Contains(ct, "ogg") ||
		strings.Contains(ct, "flac")
}
