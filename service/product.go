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
