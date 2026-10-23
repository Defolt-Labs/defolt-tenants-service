# defolt-tenants-service

The fleet registry of tenants (shops and clinics): the slug, the product (drs, health), the owner,
the lifecycle status (pending_payment, active, grace, suspended, archived) and the trial window.
It also runs public self-signup and answers Traefik's host lookup, so every request can be tied to
a tenant. It knows nothing about shelves or patients. Go, Gin, GORM on Postgres, Redis cache, NATS.

## Read order

1. README.md (this file)
2. ENDPOINTS.md: per route, who may call, input, output, what it saves, what it refuses
3. DESIGN.md: owned data, invariants, refusals, events, upstream calls, the gate
4. `routes/v1/`: route registration by purpose

## Folder map

| Folder | Holds |
|---|---|
| `main.go` | boot: config, logger, server |
| `server/` | wiring: database, NATS, Redis, services, handlers, background sweeps |
| `routes/` | `routes/routes.go` mounts probes and versions; `routes/v1/` one file per area |
| `handler/` | HTTP: parse, call one service method, map the error to a code |
| `service/` | lifecycle rules, signup, clients for identity, billing and Turnstile, NATS publish |
| `repository/` | GORM queries |
| `model/` | the Tenant type and statuses |
| `middleware/` | internal service key, CORS, request id, tenant headers |
| `cache/` | Redis slug cache and idempotency keys |
| `response/` | the response envelope, success and failure writers, codes |
| `testkit/` | shared test helpers and the product-vocabulary guard |
| `values.yaml` | Helm values for UAT |

## Run locally

Needs Postgres, Redis and NATS settings from the environment (names in `config/config.go`).
`go run .` listens on `SERVER_PORT` (8080). With `AUTO_MIGRATE=true` it creates the table itself.

## Internal keys

- Inbound: `INTERNAL_SERVICE_KEYS` (a `name=key` set), else `DEFOLT_INTERNAL_SERVICE_KEY`, else `INTERNAL_SERVICE_KEY`.
- Outbound, sent to identity and billing: `DEFOLT_INTERNAL_SERVICE_KEY`, else `INTERNAL_SERVICE_KEY`. Always one key, never the set.

## Modules

No `vendor/`. Private modules come from the UAT Athens proxy. On the Mac, with `defolt-tunnel` up:
`export GOPROXY=http://127.0.0.1:13000,https://proxy.golang.org GONOSUMDB='github.com/Defolt-Labs/*' GONOPROXY=none.invalid`.

## Test

`go vet ./...` then `go test ./... -count=1`. Tests needing Postgres skip when it is absent.

## Who calls it

- Traefik: `/tenants/v1/internal/resolve-host` on every tenant request.
- The signup pages: `POST /tenants/v1/public/signup` and `GET /tenants/v1/by-slug/:slug`.
- defolt-billing-service: activation and subscription state callbacks.
- Other services and admin tools: the tenant routes under `/tenants/v1` with the internal service key.
