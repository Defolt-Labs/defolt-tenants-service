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
}

// knownCoupling is the debt ledger: product vocabulary that is real,
// that a named board row is paying off, and that must not be treated as
// approved. Entries are removed by the row that removes the coupling —
// and the rot check above means the row cannot be closed without doing
// so.
var knownCoupling = map[string]string{}
