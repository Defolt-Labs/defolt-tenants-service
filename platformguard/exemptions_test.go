package platformguard

// exemptFiles exempts a whole file whose contents are dictated by
// something outside this service. Keyed by repository-relative path.
//
// This service has none. It is the reference product-agnostic service in
// §9.1's audit and should stay that way.
var exemptFiles = map[string]string{}

// allowedIdentifiers exempts a single identifier that trips the word
// list for an unrelated reason. Keyed "path::Ident".
//
// Every entry here is `product` in its OTHER sense, and the distinction
// is the whole of §9.1's finding. `Product` on a tenant is the Defolt
// product namespace — `drs`, `nyaraka`, `vinono`, later `afya` — which
// is precisely what makes this service serve AfyaOS unchanged. It is not
// a thing on a shelf. The guard cannot tell the two apart from the
// spelling, and a session adding a third sense should have to write down
// which one it means.
var allowedIdentifiers = map[string]string{
	"model/tenant.go::Product": "the Defolt product namespace (drs/afya), " +
		"the column §4.3 and §9.1 both rest on — not a retail product",
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
}

// knownCoupling is the debt ledger: product vocabulary that is real,
// that a named board row is paying off, and that must not be treated as
// approved. Entries are removed by the row that removes the coupling —
// and the rot check above means the row cannot be closed without doing
// so.
var knownCoupling = map[string]string{}
