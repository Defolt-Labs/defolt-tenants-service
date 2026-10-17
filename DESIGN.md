# defolt-tenants-service design

## Purpose
The fleet registry of tenants. It holds the slug, product, owner and lifecycle status of every shop and clinic, runs public self-signup, and answers Traefik's host lookup (ENDPOINTS.md, 18 routes, 1 table).
It does not own sign-in (identity), prices or subscriptions (billing), or any product data.

## Ownership
- Table owned: `tenants` (model/tenant.go), created by GORM auto-migrate when `AUTO_MIGRATE=true`. Unique on (product, slug).
- NATS subjects owned: `tenant.created`, `tenant.updated`, `tenant.status_changed`, `tenant.suspended`, `tenant.restored`, `tenant.activated`, `tenant.abandoned`.
- Idempotency keys and the 30 second slug cache live in Redis (cache/cache.go).

## Where a change goes
- A route: `routes/v1/<area>.go`, plus its section in ENDPOINTS.md in the same commit. Keep each `g.METHOD(...)` literal on one line.
- Lifecycle rules: service/. A handler parses, calls one service method, and maps errors to codes.
- Queries: repository/.
- A new reply code: response/failure.go or response/success.go.
- A new event: `emit` in service/service.go, then list the subject above.
- Background work: server/server.go, started in `Initialize` and stopped in `Cleanup`.

## Data rules
- Slug: lowercase letters, digits and hyphens, unique per product; a reserved slug (config `RESERVED_SLUGS`) is refused.
- Product: the Defolt product namespace (drs, health); slug lookups are scoped by it, default drs.
- Phone: normalised to international form; an unclear country is refused.
- Status flow: pending_payment, then active; grace and suspended follow billing; archived hides the tenant from resolve-host.
- Trial: seven days from activation.
- Soft delete via `deleted_at`; abandoned signups are deleted for good after retention.

## Refusals
- Internal routes without a matching `X-Internal-Service-Key`: 401 `DL_FORBIDDEN`.
- Signup without a passing Turnstile check: 403 `DL_SIGNUP_HUMAN_CHECK_FAILED`.
- The health product refuses a tenant with no owner: 400 `DL_TENANT_OWNER_REQUIRED`.
- Every refusal carries English and Kiswahili text.

## Events
- Published after the row is saved; a failed publish is dropped, never blocks the request.
- `tenant.activated` carries the owner and product so downstream services can provision.
- Nothing is consumed.

## Upstream calls
- defolt-identity-service: create or find the owner, reset the owner password.
- defolt-billing-service: registration checkout and payment link.
- Cloudflare Turnstile: human check on public signup.

## The gate
- `gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./... -count=1`.
- `python3 office/tools/check-endpoints.py <repo>` prints PASS.
- The testkit coupling test fails the build when a type, field or function names a product-owned concept.

## Invariants
- A tenant row is never hard-deleted except an abandoned unpaid signup.
- The abandoned-signup sweep refuses to delete a live tenant (`abandonBlocked`).
- Public routes reveal only id, slug, status and product.
- Route paths and reply codes change by addition only.
