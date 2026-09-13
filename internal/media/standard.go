package media

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/steven230500/introduce-api/internal/plan"
)

// The background standard: what a file has to be before a church can keep it
// as a background for its designs.
//
// Every rule is here because breaking it shows on the wall. Too small and the
// lyrics sit on a blur; the wrong shape and the picture is cropped or letter-
// boxed on a projector; too heavy and the machine running the service stutters
// or the upload never finishes on church wifi; too short a loop and the seam
// where it restarts is visible every few seconds.
//
// The app checks the same rules before it uploads, so the operator hears about
// a problem before waiting on a transfer. This is the check that cannot be
// skipped. Keep the numbers in step with
// lib/core/backgrounds/background_standard.dart in the app.
const (
	// 720p is the least that still looks sharp behind text on a 1080p
	// projector; below it the background is visibly soft.
	BackgroundMinWidth  = 1280
	BackgroundMinHeight = 720

	// 4K. Past it a video is decoded at a size no church projector shows, on a
	// computer that is also running the service.
	BackgroundMaxWidth  = 3840
	BackgroundMaxHeight = 2160

	// From 16:10 - the WXGA projectors that are still in half the churches -
	// to a little past 16:9, which absorbs 1366 × 768 and encoder rounding.
	// Anything outside is cropped hard or shown with bars.
	BackgroundMinAspect = 1.6
	BackgroundMaxAspect = 1.8

	BackgroundMaxImageBytes = 20 * plan.MB
	BackgroundMaxVideoBytes = 250 * plan.MB

	// Shorter than this and the restart is seen over and over; longer and it is
	// no longer a loop but a film nobody will watch to the end.
	BackgroundMinLoop = 4 * time.Second
	BackgroundMaxLoop = 3 * time.Minute
)

var (
	backgroundImageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	backgroundVideoExt = map[string]bool{".mp4": true, ".mov": true, ".m4v": true}
)

// backgroundKind says whether a filename is a still or a loop by its
// extension, or "" when it is neither. The content type a client sends is not
// used: it is whatever the uploading library guessed.
func backgroundKind(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case backgroundImageExt[ext]:
		return "image"
	case backgroundVideoExt[ext]:
		return "video"
	default:
		return ""
	}
}

// Candidate is what is known about a file offered as a background.
type Candidate struct {
	Filename string
	Bytes    int64
	Width    int
	Height   int
	// Duration is zero for an image.
	Duration time.Duration
}

// CheckBackground returns every rule the candidate breaks, each as a sentence
// the operator can act on, or nothing when it meets the standard. All of them
// at once: fixing one problem only to be told about the next is a second trip
// to the video editor.
func CheckBackground(c Candidate) []string {
	kind := backgroundKind(c.Filename)
	if kind == "" {
		return []string{fmt.Sprintf(
			"%q no es un formato de fondo. Imágenes: JPG, PNG o WebP. Videos: MP4, MOV o M4V.",
			filepath.Base(c.Filename))}
	}

	var problems []string

	if c.Width <= 0 || c.Height <= 0 {
		return append(problems, "No se pudo leer el tamaño del archivo.")
	}
	if c.Width < BackgroundMinWidth || c.Height < BackgroundMinHeight {
		problems = append(problems, fmt.Sprintf(
			"Mide %d × %d y el mínimo es %d × %d. Lo recomendado es 1920 × 1080.",
			c.Width, c.Height, BackgroundMinWidth, BackgroundMinHeight))
	}
	// Only a video is held to the upper size: an image is scaled down on
	// upload, a video would have to be re-encoded.
	if kind == "video" && (c.Width > BackgroundMaxWidth || c.Height > BackgroundMaxHeight) {
		problems = append(problems, fmt.Sprintf(
			"Mide %d × %d y el máximo para un video es %d × %d (4K).",
			c.Width, c.Height, BackgroundMaxWidth, BackgroundMaxHeight))
	}
	aspect := float64(c.Width) / float64(c.Height)
	if aspect < BackgroundMinAspect || aspect > BackgroundMaxAspect {
		problems = append(problems, fmt.Sprintf(
			"Su forma es %.2f:1 y un fondo tiene que ser horizontal, entre 16:10 y 16:9.", aspect))
	}

	maxBytes := int64(BackgroundMaxImageBytes)
	if kind == "video" {
		maxBytes = BackgroundMaxVideoBytes
	}
	if c.Bytes > maxBytes {
		problems = append(problems, fmt.Sprintf(
			"Pesa %s y el máximo es %s.", plan.Human(c.Bytes), plan.Human(maxBytes)))
	}

	if kind == "video" {
		switch {
		case c.Duration <= 0:
			problems = append(problems, "No se pudo leer cuánto dura el video.")
		case c.Duration < BackgroundMinLoop:
			problems = append(problems, fmt.Sprintf(
				"Dura %s y un fondo tiene que durar al menos %d segundos para que no se note cuando vuelve a empezar.",
				seconds(c.Duration), int(BackgroundMinLoop.Seconds())))
		case c.Duration > BackgroundMaxLoop:
			problems = append(problems, fmt.Sprintf(
				"Dura %s y un fondo puede durar hasta %d minutos.",
				seconds(c.Duration), int(BackgroundMaxLoop.Minutes())))
		}
	}

	return problems
}

func seconds(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0f s", d.Seconds())
	}
	return fmt.Sprintf("%d:%02d min", int(d.Minutes()), int(d.Seconds())%60)
}
