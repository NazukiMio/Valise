package service

import (
	"os"
	"strings"
	"time"
)

// LocalZone is this machine's IANA time zone (for vault periods), falling
// back to UTC.
func LocalZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if _, z, ok := strings.Cut(p, "zoneinfo/"); ok {
			return z
		}
	}
	if n := time.Local.String(); n != "Local" {
		return n
	}
	return "UTC"
}
