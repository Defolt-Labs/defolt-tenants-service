package service

import "strings"

// DefaultProduct is the fallback product namespace.
const DefaultProduct = "drs"

// NormalizeProduct lowercases and trims product, defaulting to drs.
func NormalizeProduct(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return DefaultProduct
	}
	return p
}

// DefaultRootDomain is the fallback DNS root for customer-facing hosts.
const DefaultRootDomain = "uat.defoltlabs.com"

// ProductHome returns the public customer-facing home URL for a product.
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
			return "https://drs." + root
		}
		return "https://" + slug + ".shop." + root
	default:
		return "https://www." + root
	}
}

// ProductReturnURL returns post-payment return URL for a tenant product.
func ProductReturnURL(product, slug, rootDomain string) string {
	return ProductHome(product, slug, rootDomain) + "/login"
}
