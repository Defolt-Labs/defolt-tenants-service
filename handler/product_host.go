package handler

import (
	"strings"

	"defolt-tenants-service/service"
)

// productFromHost decides which fleet product a forward-auth request
// belongs to, so slug resolution can be scoped to (product, slug) rather
// than the ambiguous bare slug.
//
// The explicit X-Defolt-Product header wins when the edge sets it — that
// is the durable signal for a per-product ingress (e.g. a future DHS
// host family). With no header we fall back to parsing the host suffix,
// which is where product is encoded today: every DRS host family carries
// `.drs.` or `.shop.`, and health hosts (greenfield) carry `.dhs.` or
// `.afya.`. Anything unrecognized defaults to drs, preserving the
// original single-product behaviour rather than guessing.
func productFromHost(host, headerProduct string) string {
	if p := strings.TrimSpace(headerProduct); p != "" {
		return service.NormalizeProduct(p)
	}
	h := strings.ToLower(host)
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	switch {
	case strings.Contains(h, ".dhs.") || strings.Contains(h, ".afya."):
		return "health"
	case strings.Contains(h, ".drs.") || strings.Contains(h, ".shop."):
		return "drs"
	default:
		return service.DefaultProduct
	}
}
