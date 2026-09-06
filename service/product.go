package service

import "strings"

// DefaultProduct is the fleet's original single product. Slug resolution
// and creation default to it whenever a caller supplies no product, so
// every request that predates product-scoping (drs-vue signup, the
// DRS-only forward-auth host family) keeps resolving exactly as before.
const DefaultProduct = "drs"

// NormalizeProduct lowercases/trims a product string and falls back to
// DefaultProduct when it is empty. Tenant slugs are unique PER PRODUCT
// (composite index ux_tenants_product_slug on (product, slug)), so every
// slug lookup, uniqueness pre-check and cache key must carry a product,
// and it must be normalized the same way the write path stores it.
func NormalizeProduct(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return DefaultProduct
	}
	return p
}

// ---------------------------------------------------------------------
// Where a customer of each product actually lives.
//
// This exists because the resume-payment path sent a dental clinic to
// nowhere. ResendPaymentLink called billing's checkout with an EMPTY
// redirect_url, billing passes that value straight through to Selcom, and
// Selcom with no return URL leaves the payer on its own page. So an owner
// who abandoned signup and came back through the sign-in panel paid the
// 2,000 TZS and was never returned to the product he had just bought.
//
// The signup form does supply one (dhs-vue/src/lib/platform.ts sends
// `${origin}/login`), which is why this was invisible: the first attempt
// works and only the resume is broken, and the resume is the path a person
// reaches precisely because something already went wrong once.
//
// The host shapes differ per product and that is the whole reason a single
// value cannot serve. The chart carried one anyway, TENANT_BASE_DOMAIN =
// `mt.drs.uat.defoltlabs.com`, which was read by no Go code at all and
// named the legacy multi-tenant apex no customer may be shown.

// ProductRootDomain is the DNS root every customer-facing host hangs off.
// Injected rather than derived so a new environment is one chart value.
const DefaultRootDomain = "uat.defoltlabs.com"

// ProductHome is the address of a tenant's own front door.
//
//	drs     https://<slug>.shop.<root>   one host per store
//	health  https://dhs.<root>           one host for every clinic
//
// DHS serves every clinic from a single host and resolves the facility
// after sign-in, which is the state the owner has not yet decided to
// change. If he chooses per-clinic addresses later, this function is the
// one place that changes, plus a wildcard certificate and an ingress.
//
// An unknown product gets the marketing site rather than a guess: a
// customer sent to the wrong product's host is worse served than one sent
// to the front page.
func ProductHome(product, slug, rootDomain string) string {
	root := strings.TrimSpace(rootDomain)
	if root == "" {
		root = DefaultRootDomain
	}
	root = strings.Trim(strings.ToLower(root), "./")
	slug = strings.ToLower(strings.TrimSpace(slug))

	switch NormalizeProduct(product) {
	case "health", "dhs", "afya":
		return "https://dhs." + root
	case DefaultProduct, "retail":
		if slug == "" {
			// No slug means no store host exists to send them to. The
			// product's own apex is a real page; `https://.shop.<root>`
			// is not a hostname at all.
			return "https://drs." + root
		}
		return "https://" + slug + ".shop." + root
	default:
		return "https://www." + root
	}
}

// ProductReturnURL is where Selcom returns the payer once the
// registration payment clears. `/login` for both products: the tenant is
// not active until billing's activation callback lands, so the sign-in
// screen is the only page that can tell them the truth either way.
func ProductReturnURL(product, slug, rootDomain string) string {
	return ProductHome(product, slug, rootDomain) + "/login"
}
