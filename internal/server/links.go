package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Devices as the
// target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "devices:read", Name: "See your devices", Description: "Read your devices: name, place, whether they are online, what they report (temperature, CO₂, a door open, battery), their alerts, button presses and other events, and their settings — for your dashboards, your day, your flows and Pocket AI. Never WiFi passwords or device tokens."},
	{Scope: "devices:write", Name: "Change device settings", Description: "Change your devices' settings (an alarm time, a CO₂ limit) and send everyday commands like \"switch the plug on\" or \"stop the alarm\". Never restart a device, open its setup mode or erase it."},
}

// portable is what Pocket Move may copy out and bring in for a person
// (docs/move.md in pocket-homelab-core); Register adds move:export/import.
var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.json", File: "devices.json", Kind: core.KindPocket, Title: "Every device with the settings you set, so the clocks keep working on the new box (WiFi passwords are left out)"},
		{Path: "/api/v1/export.csv", File: "devices.csv", Kind: core.KindCSV, Title: "Your devices as a table"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, ContentType: "application/json", MaxBytes: 16 << 20, Title: "Adds the devices from another box with their settings and tokens; a device already here is skipped"},
	},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(devices|kinds|overview)$`), "devices:read"},
	{"GET", regexp.MustCompile(`^/api/v1/devices/\d+(/commands|/log|/readings|/events)?$`), "devices:read"},
	{"PUT", regexp.MustCompile(`^/api/v1/devices/\d+/settings$`), "devices:write"},
	{"POST", regexp.MustCompile(`^/api/v1/devices/\d+/commands$`), "devices:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
