package middleware

import (
	"crypto/subtle"
	"strings"
)

// One internal key for the whole fleet, and how to stop having one.
//
// WP-SEC4 H1. INTERNAL_SERVICE_KEY was a single 64-character value, identical
// in defolt-platform, dhs and drs, held by every pod in all three. Compromise of
// any one pod anywhere yielded every internal route fleet-wide — including
// defolt-identity-service's user create, delete and reset-password API. There
// was no per-caller identity, no isolation, and no way to rotate one caller
// without rotating all of them at once.
//
// The smallest step that actually shrinks that is to let the CALLEE accept
// several named keys while each CALLER keeps holding exactly one. No caller
// changes: a caller still sends the single value in its own bundle's
// INTERNAL_SERVICE_KEY, and the owner simply makes that value different per
// namespace. What changes is that the platform services are told the whole set:
//
//	INTERNAL_SERVICE_KEYS=platform=<k1>,dhs=<k2>,drs=<k3>
//
// A compromised DRS pod then yields the DRS caller key and nothing else, and
// that key can be rotated on its own. The bare single-value form is still
// accepted so the rollover is one namespace at a time rather than a flag day.
//
// This is NOT the destination. Per-caller keys or mTLS is, and the name each
// key carries here is what makes that next step incremental rather than another
// rewrite: the callee already knows which caller it just authenticated.

// keySet is a parsed INTERNAL_SERVICE_KEYS value: caller name to secret.
type keySet struct {
	pairs []keyPair
}

type keyPair struct {
	name   string
	secret string
}

// parseKeySet reads either the named form "dhs=aaa,drs=bbb" or a single bare
// key, which is named "legacy" so a log line can say which shape authenticated.
//
// A bare value containing no "=" is unambiguous. A value that DOES contain "="
// is only treated as named when every comma-separated element has one; a single
// secret that happens to contain an "=" (base64 padding does) would otherwise be
// silently truncated into a name and a fragment, and the service would reject
// every caller for a reason nothing in the logs would explain.
func parseKeySet(raw string) keySet {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return keySet{}
	}
	parts := strings.Split(raw, ",")
	named := make([]keyPair, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, secret, ok := strings.Cut(p, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(secret) == "" {
			return keySet{pairs: []keyPair{{name: "legacy", secret: raw}}}
		}
		named = append(named, keyPair{name: strings.TrimSpace(name), secret: strings.TrimSpace(secret)})
	}
	if len(named) == 0 {
		return keySet{pairs: []keyPair{{name: "legacy", secret: raw}}}
	}
	return keySet{pairs: named}
}

// match returns the name of the caller whose key was presented.
//
// Every candidate is compared even after a hit, so the time taken does not
// depend on which key matched or on how many are configured. An empty set
// matches nothing: a service whose key was never provisioned must refuse every
// request rather than accept every request.
func (s keySet) match(presented string) (string, bool) {
	if presented == "" || len(s.pairs) == 0 {
		return "", false
	}
	caller, ok := "", false
	for _, p := range s.pairs {
		if subtle.ConstantTimeCompare([]byte(presented), []byte(p.secret)) == 1 {
			caller, ok = p.name, true
		}
	}
	return caller, ok
}
