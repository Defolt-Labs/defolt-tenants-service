package middleware

import (
	"crypto/subtle"
	"strings"
)

// keySet is a parsed set of caller name to secret pairs.
type keySet struct {
	pairs []keyPair
}

type keyPair struct {
	name   string
	secret string
}

// parseKeySet reads either comma-separated name=secret pairs or a bare legacy key.
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

// match returns the name of the caller whose key matched in constant time.
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
