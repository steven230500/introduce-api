// Package geo turns an IP address into the country it belongs to, and nothing
// finer. It reads the free DB-IP "IP to Country Lite" database, which the
// Docker image downloads when it is built.
//
// IP geolocation by DB-IP (https://db-ip.com), licensed CC BY 4.0.
package geo

import (
	"log"
	"net"
	"net/http"

	"github.com/oschwald/maxminddb-golang"
)

// Locator answers "which country". A nil or empty Locator answers "" for every
// address, so a build without the database still runs, just without countries.
type Locator struct {
	db *maxminddb.Reader
}

// Open reads the database at path. A missing or unreadable file is logged and
// yields a Locator that knows no countries rather than an error: stats with
// blank countries are better than an API that will not start.
func Open(path string) *Locator {
	db, err := maxminddb.Open(path)
	if err != nil {
		log.Printf("geo: no country database at %s (%v); countries will be blank", path, err)
		return &Locator{}
	}
	return &Locator{db: db}
}

// Country is the ISO 3166 alpha-2 code for ip, or "".
func (l *Locator) Country(ip string) string {
	if l == nil || l.db == nil {
		return ""
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := l.db.Lookup(parsed, &record); err != nil {
		return ""
	}
	return record.Country.ISOCode
}

// ClientIP is the caller's address as the request arrived. The router's RealIP
// middleware has already replaced RemoteAddr with the address Caddy forwarded.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
