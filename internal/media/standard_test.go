package media

import (
	"strings"
	"testing"
	"time"
)

func TestBackgroundStandard(t *testing.T) {
	good := func(filename string) Candidate {
		c := Candidate{Filename: filename, Bytes: 4 << 20, Width: 1920, Height: 1080}
		if backgroundKind(filename) == "video" {
			c.Duration = 30 * time.Second
		}
		return c
	}

	for _, name := range []string{"cruz.jpg", "CRUZ.JPEG", "luz.png", "cielo.webp", "loop.mp4", "loop.MOV", "loop.m4v"} {
		if problems := CheckBackground(good(name)); len(problems) != 0 {
			t.Errorf("%s: a Full HD file broke the standard: %v", name, problems)
		}
	}

	// The shapes churches actually have: a WXGA projector, a cheap 1366 × 768
	// panel, and 4K for a video.
	for _, size := range [][2]int{{1280, 800}, {1366, 768}, {1280, 720}, {3840, 2160}} {
		c := good("loop.mp4")
		c.Width, c.Height = size[0], size[1]
		if problems := CheckBackground(c); len(problems) != 0 {
			t.Errorf("%d × %d refused: %v", size[0], size[1], problems)
		}
	}

	cases := []struct {
		name   string
		change func(*Candidate)
		says   string
	}{
		{"a document", func(c *Candidate) { c.Filename = "letra.pdf" }, "no es un formato"},
		{"too small", func(c *Candidate) { c.Width, c.Height = 1024, 576 }, "el mínimo es 1280 × 720"},
		{"portrait", func(c *Candidate) { c.Width, c.Height = 1080, 1920 }, "horizontal"},
		{"square", func(c *Candidate) { c.Width, c.Height = 2000, 2000 }, "horizontal"},
		{"4:3", func(c *Candidate) { c.Width, c.Height = 1600, 1200 }, "horizontal"},
		{"ultrawide", func(c *Candidate) { c.Width, c.Height = 2560, 1080 }, "horizontal"},
		{"a heavy photo", func(c *Candidate) { c.Filename = "foto.jpg"; c.Duration = 0; c.Bytes = 30 << 20 }, "el máximo es 20 MB"},
		{"a heavy video", func(c *Candidate) { c.Bytes = 300 << 20 }, "el máximo es 250 MB"},
		{"8K video", func(c *Candidate) { c.Width, c.Height = 7680, 4320 }, "4K"},
		{"a blink", func(c *Candidate) { c.Duration = 2 * time.Second }, "al menos 4 segundos"},
		{"a film", func(c *Candidate) { c.Duration = 20 * time.Minute }, "hasta 3 minutos"},
		{"unknown length", func(c *Candidate) { c.Duration = 0 }, "cuánto dura"},
		{"unreadable", func(c *Candidate) { c.Width = 0 }, "No se pudo leer el tamaño"},
	}
	for _, tc := range cases {
		c := good("loop.mp4")
		tc.change(&c)
		problems := CheckBackground(c)
		if !strings.Contains(strings.Join(problems, " | "), tc.says) {
			t.Errorf("%s: want a problem mentioning %q, got %v", tc.name, tc.says, problems)
		}
	}

	// An image is scaled down on upload, so a large one is not a problem.
	big := good("foto.jpg")
	big.Width, big.Height = 6000, 3375
	if problems := CheckBackground(big); len(problems) != 0 {
		t.Errorf("a large photo was refused: %v", problems)
	}

	// Everything wrong is said at once.
	bad := Candidate{Filename: "x.mp4", Bytes: 900 << 20, Width: 640, Height: 640, Duration: time.Second}
	if problems := CheckBackground(bad); len(problems) != 4 {
		t.Errorf("want 4 problems at once, got %d: %v", len(problems), problems)
	}
}

func TestVideoIsNotMistakenForAudio(t *testing.T) {
	// "video/mp4" contains "mp4", and the check for audio used to look for
	// exactly that, so every MP4 uploaded to the library was filed as audio
	// and listed as an image.
	for ct, want := range map[string]string{
		"video/mp4":                "videos",
		"video/quicktime":          "videos",
		"audio/mp4":                "audio",
		"audio/mpeg":               "audio",
		"audio/x-m4a":              "audio",
		"image/png":                "images",
		"application/octet-stream": "videos",
	} {
		if got := categoryFor(ct); got != want {
			t.Errorf("%s filed as %s, want %s", ct, got, want)
		}
	}
}
