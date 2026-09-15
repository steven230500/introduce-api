package geo

import (
	"net/http/httptest"
	"os"
	"testing"
)

func TestWithoutTheDatabaseEveryCountryIsBlank(t *testing.T) {
	locator := Open("/nonexistent/dbip.mmdb")

	if got := locator.Country("8.8.8.8"); got != "" {
		t.Fatalf("got %q", got)
	}
	var none *Locator
	if got := none.Country("8.8.8.8"); got != "" {
		t.Fatalf("nil locator: %q", got)
	}
}

// Needs the real database, which is downloaded when the image is built; point
// GEOIP_TEST_DATABASE at a copy to run it.
func TestAnAddressIsPlacedInItsCountry(t *testing.T) {
	path := os.Getenv("GEOIP_TEST_DATABASE")
	if path == "" {
		t.Skip("GEOIP_TEST_DATABASE not set")
	}
	locator := Open(path)

	for ip, want := range map[string]string{"8.8.8.8": "US", "190.24.0.1": "CO", "not an ip": "", "2800:e2:1:1::1": "CO"} {
		if got := locator.Country(ip); got != want {
			t.Errorf("%s: got %q, want %q", ip, got, want)
		}
	}
}

func TestTheClientAddressIsReadWithOrWithoutAPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "190.24.0.1:51234"
	if got := ClientIP(req); got != "190.24.0.1" {
		t.Fatalf("with port: %q", got)
	}
	req.RemoteAddr = "190.24.0.1"
	if got := ClientIP(req); got != "190.24.0.1" {
		t.Fatalf("without port: %q", got)
	}
}
