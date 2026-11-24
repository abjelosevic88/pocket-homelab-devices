package server

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
)

var reZone = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+){0,2}$`)

// posixTZ reads the POSIX rule at the end of a TZif (v2+) zone file, which
// is exactly what a device's tz setting wants: "Europe/Sarajevo" →
// "CET-1CEST,M3.5.0,M10.5.0/3".
func posixTZ(zone string) (string, error) {
	if !reZone.MatchString(zone) {
		return "", errors.New("not a time zone name")
	}
	dirs := []string{os.Getenv("ZONEINFO"), "/usr/share/zoneinfo", "/usr/lib/zoneinfo", "/usr/share/lib/zoneinfo", "/etc/zoneinfo"}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(zone)))
		if err != nil || !bytes.HasPrefix(b, []byte("TZif")) {
			continue
		}
		b = bytes.TrimRight(b, "\n")
		i := bytes.LastIndexByte(b, '\n')
		if i < 0 {
			continue
		}
		tz := string(b[i+1:])
		k, _ := kinds.Get("ntp-clock")
		if k == nil {
			return tz, nil
		}
		if n, err := k.Normalize("tz", tz); err == nil {
			return n, nil
		}
	}
	return "", errors.New("no rule found for this time zone")
}
