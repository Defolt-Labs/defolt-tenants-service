// Package static embeds the tenant lifecycle landing pages (plan
// §5.17) plus the apex marketing landing and the public signup form.
// Traefik forward auth redirects unresolved, suspended and
// pending-payment hosts here. Pages are fully self-contained: inline
// CSS, system font stack, no external assets.
package static

import "embed"

//go:embed *.html
var FS embed.FS

// Pages is the allowlist of servable page names (without ".html").
var Pages = map[string]string{
	"not-registered":     "not-registered.html",
	"suspended":          "suspended.html",
	"pending-activation": "pending-activation.html",
	"signup":             "signup.html",
	"landing":            "landing.html",
}

// Landing is the page served on the bare apex host (no tenant slug).
// An unknown SUBDOMAIN keeps falling through to "not-registered": a
// typo'd store address and a deliberate visit to the product's main
// URL are different events and read differently.
const Landing = "landing"
