// Package reqid carries the fleet request ID across the gin boundary.
//
// Handlers hand the service layer c.Request.Context(), not the gin
// context, so an ID stashed only via gin's c.Set never reaches the
// outbound service clients. defolt-identity rejects any request without
// X-Request-ID, so losing it here silently breaks tenant signup.
package reqid

import "context"

// key is unexported and typed so nothing outside this package can
// collide with it. Note this is deliberately NOT a string key: it is
// read from a plain context.Context, never from gin.Context, so the
// ContextWithFallback caveat does not apply.
type key struct{}

// With returns ctx carrying the request ID. An empty id is not stored,
// so From stays empty rather than reporting a blank ID as present.
func With(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, key{}, id)
}

// From returns the request ID, or "" when the context carries none.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(key{}).(string); ok {
		return v
	}
	return ""
}
