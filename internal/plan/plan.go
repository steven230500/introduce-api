// Package plan says how much room a church gets.
//
// Storage is the one thing here that costs real money per church rather than
// per deploy: a single Sunday's countdown video is larger than everything else
// an organization will ever write to this database. So it is the thing the
// paid tiers buy, and the free tier is sized for a church that projects
// lyrics, verses and the occasional photo, which is most of them.
package plan

import "fmt"

type Name string

const (
	Free    Name = "free"
	Iglesia Name = "iglesia"
	Red     Name = "red"
)

const (
	MB = int64(1) << 20
	GB = int64(1) << 30
)

// Limits is what one plan allows.
type Limits struct {
	Name Name `json:"plan"`

	// Label is what the operator reads, in Spanish.
	Label string `json:"label"`

	// Storage is everything the organization has uploaded, added up.
	Storage int64 `json:"storage_bytes"`

	// MaxUpload caps a single file. Separate from [Storage] because the cost
	// that hurts is not a full library, it is one two-gigabyte export of the
	// whole service that somebody drags in by mistake.
	MaxUpload int64 `json:"max_upload_bytes"`
}

var all = map[Name]Limits{
	Free: {
		Name:      Free,
		Label:     "Gratis",
		Storage:   500 * MB,
		MaxUpload: 25 * MB,
	},
	Iglesia: {
		Name:      Iglesia,
		Label:     "Iglesia",
		Storage:   25 * GB,
		MaxUpload: 500 * MB,
	},
	Red: {
		Name:      Red,
		Label:     "Red de iglesias",
		Storage:   100 * GB,
		MaxUpload: 2 * GB,
	},
}

// For resolves a stored plan name. An unknown name means a row written by a
// newer version, or by hand: it gets the free limits rather than no limits,
// because the failure that costs money is the one that lets everything
// through.
func For(name string) Limits {
	if limits, ok := all[Name(name)]; ok {
		return limits
	}
	return all[Free]
}

// Human renders a byte count the way the app shows it, so the API and the
// operator are talking about the same number.
//
// Integer division rather than a rounded format: Go rounds a half to even and
// Dart rounds it away from zero, so 512 bytes would come out "0 KB" here and
// "1 KB" in the app. Rounding down agrees everywhere and never claims more
// room than there is.
func Human(bytes int64) string {
	switch {
	case bytes >= GB:
		whole := bytes / GB
		tenths := (bytes * 10 / GB) % 10
		return fmt.Sprintf("%d.%d GB", whole, tenths)
	case bytes >= MB:
		return fmt.Sprintf("%d MB", bytes/MB)
	default:
		return fmt.Sprintf("%d KB", bytes/1024)
	}
}
