# defolt-tenants-service endpoints

One section per route, with the full path. Bodies are the Defolt envelope `{code, meta{en,sw}, data, details, request_id}`.

Rules shared by every route, stated once:
- Routes marked "internal key" need the `X-Internal-Service-Key` header to match a configured key, else 401 `DL_FORBIDDEN`.
- Ids in the path must be uuids, else 400 `DL_VALIDATION_FAILED`.
- An unknown tenant is 404 `DL_TENANT_UNKNOWN`.
- Unexpected failure: 500 `DL_INTERNAL_ERROR`.

### GET /health
- Handler: Health
- Who may call: anyone, no key. Liveness probe.
- In: nothing.
- Out: 200 `DL_HEALTH_OK`, data `version`.
- Saves: nothing.
- Refuses: nothing.

### GET /ready
- Handler: Readiness (httpkit)
- Who may call: anyone, no key. Readiness probe.
- In: nothing.
- Out: 200 `{"status":"ready","nats":"connected|disconnected|absent"}` after the database answered a ping within 2 seconds. Not the envelope.
- Saves: nothing.
- Refuses: 503 `{"status":"db_unreachable"}` when the ping fails or is slow; the error goes to the log, never the body.

### GET /tenants/v1/by-slug/:slug
- Handler: GetBySlug
- Who may call: anyone, no key. Public lookup, rate limited at Traefik.
- In: `slug` in the path; product from the `product` query or the `X-Defolt-Product` header, default drs.
- Out: 200 `DL_TENANT`, data id, slug, status, product. Cached 30 seconds.
- Saves: nothing.
- Refuses: 404 `DL_TENANT_UNKNOWN`.

### POST /tenants/v1/public/signup
- Handler: PublicSignup
- Who may call: anyone, no key. A Turnstile check guards it; with no `TURNSTILE_SECRET` set it is skipped only when `ENVIRONMENT=development` is set explicitly and refused 503 everywhere else.
- In: slug, name, contact_email, phone, first_name, last_name (all required, not blank); middle_name, country_code, turnstile_token, redirect_url, product optional. Optional `Idempotency-Key` header replays the first answer.
- Out: 201 `DL_TENANT_CREATED`, data id, slug, status, one_time_password, owner_existing, owner_email, payment_url, amount_tzs. For a product named in `OWNER_LINK_NAMESPACES` (the owner's first password is a link emailed by that product's own service) there is no `one_time_password` key and `owner_link_by_email` is true instead.
- Saves: a pending_payment tenant, the owner in identity (with a password nobody is shown when the owner is linked), a billing checkout; announces `tenant.created`. A stalled earlier signup for the same email resumes.
- Refuses: 400 `DL_SIGNUP_FIELD_REQUIRED`, `DL_SIGNUP_EMAIL_INVALID`, `DL_SIGNUP_BODY_UNREADABLE`, `DL_SIGNUP_PHONE_INVALID`, `DL_SIGNUP_PHONE_COUNTRY_UNCLEAR`, `DL_TENANT_SLUG_INVALID`; 403 `DL_SIGNUP_HUMAN_CHECK_FAILED`; 409 `DL_TENANT_SLUG_RESERVED`, `DL_TENANT_SLUG_TAKEN`; 503 `DL_SIGNUP_HUMAN_CHECK_UNAVAILABLE` (Cloudflare unreachable, our secret refused, or no secret set outside development).

### POST /tenants/v1
- Handler: Create
- Who may call: internal key.
- In: slug, name, contact_email, phone required; currency, timezone, country_code, product, plan, owner_user_id, owner_email, owner names optional. The health product requires owner_user_id. Optional `Idempotency-Key`.
- Out: 201 `DL_TENANT_CREATED`, the full tenant.
- Saves: the tenant; announces `tenant.created`.
- Refuses: 400 `DL_VALIDATION_FAILED`, `DL_TENANT_OWNER_REQUIRED`; 409 `DL_TENANT_SLUG_RESERVED`, `DL_TENANT_SLUG_TAKEN`.

### GET /tenants/v1/:id
- Handler: Get
- Who may call: internal key.
- In: `id` in the path.
- Out: 200 `DL_TENANT`, the full tenant.
- Saves: nothing.
- Refuses: 404.

### PATCH /tenants/v1/:id
- Handler: Patch
- Who may call: internal key.
- In: any of name, contact_email, phone, plan.
- Out: 200 `DL_TENANT_UPDATED`, the full tenant.
- Saves: the changed fields; announces `tenant.updated`.
- Refuses: 400 `DL_VALIDATION_FAILED`; 404.

### POST /tenants/v1/:id/suspend
- Handler: Suspend
- Who may call: internal key.
- In: `id` in the path.
- Out: 200 `DL_TENANT_SUSPENDED`, the tenant.
- Saves: status suspended; clears the slug cache; announces `tenant.status_changed` and `tenant.suspended`.
- Refuses: 404.

### POST /tenants/v1/:id/restore
- Handler: Restore
- Who may call: internal key.
- In: `id` in the path.
- Out: 200 `DL_TENANT_RESTORED`, the tenant.
- Saves: status active; clears the slug cache; announces `tenant.status_changed` and `tenant.restored`.
- Refuses: 404.

### POST /tenants/v1/:id/reissue-owner-otp
- Handler: ReissueOwnerOTP
- Who may call: internal key.
- In: `id` in the path.
- Out: 200 `DL_TENANT_OTP_REISSUED`, data one_time_password.
- Saves: a new owner password in identity.
- Refuses: 404; 409 `DL_TENANT_NO_OWNER`; 409 `DL_TENANT_OWNER_LINK_ELSEWHERE` for a tenant whose product is in `OWNER_LINK_NAMESPACES`, before identity is touched: the reissue is the product's own.

### POST /tenants/v1/:id/resend-payment-link
- Handler: ResendPaymentLink
- Who may call: internal key.
- In: `id` in the path.
- Out: 200 `DL_TENANT_PAYMENT_LINK`, data payment_url, amount_tzs.
- Saves: a fresh checkout in billing.
- Refuses: 404; 503 `DL_BILLING_UNAVAILABLE`.

### GET /tenants/v1/pending-checkout
- Handler: PendingCheckoutByEmail
- Who may call: internal key.
- In: `email` query required, `product` query optional.
- Out: 200 `DL_TENANT_PAYMENT_LINK`, data slug, payment_url, amount_tzs.
- Saves: nothing.
- Refuses: 400 `DL_VALIDATION_FAILED`; 404; 503 `DL_BILLING_UNAVAILABLE`.

### GET /tenants/v1/:id/health
- Handler: TenantHealth
- Who may call: internal key.
- In: `id` in the path.
- Out: 200 `DL_TENANT_HEALTHY`, data id, slug, status, healthy (true when active or grace).
- Saves: nothing.
- Refuses: 404.

### POST /tenants/v1/internal/tenants/:id/activate
- Handler: Activate
- Who may call: internal key (billing after payment).
- In: `id` in the path.
- Out: 200 `DL_TENANT_RESTORED`, the tenant. A tenant not pending_payment comes back unchanged.
- Saves: status active, a 7 day trial, activated_at; announces `tenant.activated`.
- Refuses: 404.

### PUT /tenants/v1/internal/tenants/:id/subscription-state
- Handler: SyncSubscriptionState
- Who may call: internal key (billing).
- In: `state` required, the billing subscription state.
- Out: 200 `DL_TENANT_UPDATED`, the tenant.
- Saves: the mapped status when it changed; announces `tenant.status_changed`, and `tenant.activated` on pending_payment to active.
- Refuses: 400 `DL_VALIDATION_FAILED`; 404.

### POST /tenants/v1/internal/sweep-exploring
- Handler: SweepExploring
- Who may call: internal key.
- In: nothing.
- Out: 200 `DL_TENANT_UPDATED`, data activated (count).
- Saves: activates pending exploring tenants; announces `tenant.activated` for each.
- Refuses: nothing.

### POST /tenants/v1/internal/resolve-host
- Handler: ResolveHost
- Who may call: Traefik forward auth only; open in code, the network policy keeps others out.
- In: `X-Forwarded-Host` (or Host); `X-Defolt-Product` optional.
- Out: 200 with empty body and headers for tenant id, slug, product and status; unknown tenant is 200 with status header `unknown`.
- Saves: nothing.
- Refuses: 403 for a missing slug or an archived tenant; 503 `DL_TENANT_RESOLVE_UNAVAILABLE`.

### GET /tenants/v1/internal/resolve-host
- Handler: ResolveHost
- Who may call: same as the POST form.
- In: same.
- Out: same.
- Saves: nothing.
- Refuses: same.
