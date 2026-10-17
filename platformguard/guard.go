// Package platformguard holds the exemption lists read by the product-vocabulary guard test.
package platformguard

// exemptFiles exempts a whole file whose contents an outside party dictates. Keyed by repo-relative path.
var exemptFiles = map[string]string{}

// allowedIdentifiers exempts an identifier that means the product namespace, not a retail product. Keyed "path::Ident".
var allowedIdentifiers = map[string]string{
	"model/tenant.go::Product": "the Defolt product namespace (drs/afya), " +
		"the tenant product column, not a retail product",
	"handler/handler.go::Product": "signup body field carrying the product " +
		"namespace through to model.Tenant.Product",
	"service/service.go::Product": "SignupInput field carrying the product " +
		"namespace through to model.Tenant.Product",
	"service/signup.go::Product": "SignupInput field carrying the product " +
		"namespace (drs/health) into the signup flow — selects the paid vs " +
		"auto-activate path, not a retail product",
	"middleware/middleware.go::TenantProductHeader": "X-Defolt-Tenant-Product " +
		"names the product namespace Traefik resolved, not a retail product",
	"middleware/middleware.go::X-Defolt-Tenant-Product": "the header value of " +
		"TenantProductHeader above",
	"middleware/middleware.go::ProductHeader": "X-Defolt-Product is the request-" +
		"side product-namespace signal (drs/health) the edge sends so slug " +
		"resolution can be scoped — the namespace, not a retail product",
	"middleware/middleware.go::X-Defolt-Product": "the header value of " +
		"ProductHeader above",
	"service/product.go::DefaultProduct": "the fleet's default product namespace " +
		"(drs) that slug resolution falls back to — the namespace, not a shelf item",
	"service/product.go::NormalizeProduct": "normalizes a product-namespace string " +
		"(lowercase/trim, default drs) for slug scoping — the namespace, not a retail product",
	"handler/product_host.go::productFromHost": "derives the product namespace " +
		"(drs/health) from the request host/header so slug resolution is scoped — " +
		"the namespace, not a retail product",
	"service/product.go::ProductHome": "maps a product NAMESPACE (drs/health) to " +
		"the customer-facing host shape it is served from — <slug>.shop.<root> for " +
		"a store, dhs.<root> for a clinic. The namespace is exactly what varies, " +
		"which is why the function takes it; it is not a retail product",
	"service/product.go::ProductReturnURL": "where Selcom returns the payer, per " +
		"product namespace, built on ProductHome above — the namespace, not a " +
		"retail product",
	"service/service.go::RequireOwnerForProduct": "decides whether a tenant in a " +
		"given product NAMESPACE (health) must name an owner before it may be " +
		"created. The namespace is the input precisely because it is what varies, " +
		"and the rule is the platform's own — not a retail product",
}

// knownCoupling lists real coupling that is being paid off; it must only shrink.
var knownCoupling = map[string]string{}
