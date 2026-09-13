package history

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validEvent() Event {
	start := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	return Event{
		ID:        uuid.New(),
		ItemType:  "song",
		Title:     "Nada es imposible",
		StartedAt: start,
		EndedAt:   start.Add(4 * time.Minute),
	}
}

func TestAWellFormedEventIsAccepted(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("valid event refused: %v", err)
	}
}

func TestAnEventWithoutAClientIDIsRefused(t *testing.T) {
	// The client id is what makes a resend harmless. Without one, a service
	// recorded offline and sent twice would be counted twice in the report.
	e := validEvent()
	e.ID = uuid.Nil
	if err := e.Validate(); err == nil {
		t.Fatal("event with no id was accepted")
	}
}

func TestAnEventThatEndsBeforeItStartsIsRefused(t *testing.T) {
	// A clock that jumped backwards mid-service would otherwise put a negative
	// duration into a report someone has to sign.
	e := validEvent()
	e.EndedAt = e.StartedAt.Add(-time.Second)
	if err := e.Validate(); err == nil {
		t.Fatal("event ending before it starts was accepted")
	}
}

func TestAZeroLengthEventIsAccepted(t *testing.T) {
	// Sent and immediately replaced is still "was on the screen".
	e := validEvent()
	e.EndedAt = e.StartedAt
	if err := e.Validate(); err != nil {
		t.Fatalf("instantaneous event refused: %v", err)
	}
}

func TestAnUnknownItemTypeIsRefused(t *testing.T) {
	e := validEvent()
	e.ItemType = "holograma"
	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "holograma") {
		t.Fatalf("unknown type not named in the refusal: %v", err)
	}
}

func TestNoRangeMeansTheLastNinetyDays(t *testing.T) {
	// The window a quarterly licence report needs.
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	from, to, err := parseRange("", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !to.Equal(now) {
		t.Fatalf("to = %v, want now", to)
	}
	if got := to.Sub(from); got != 90*24*time.Hour {
		t.Fatalf("default window is %v", got)
	}
}

func TestARangeBackwardsIsRefused(t *testing.T) {
	_, _, err := parseRange("2026-09-01T00:00:00Z", "2026-08-01T00:00:00Z", time.Now())
	if err == nil {
		t.Fatal("a range ending before it starts was accepted")
	}
}

func TestARangeLongerThanAYearIsRefused(t *testing.T) {
	// An unbounded range is an easy way to ask the database for everything.
	_, _, err := parseRange("2020-01-01T00:00:00Z", "2026-01-01T00:00:00Z", time.Now())
	if err == nil {
		t.Fatal("a six-year range was accepted")
	}
}

func TestADateThatIsNotADateSaysWhichOne(t *testing.T) {
	_, _, err := parseRange("ayer", "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "from") {
		t.Fatalf("bad from not named: %v", err)
	}
}
