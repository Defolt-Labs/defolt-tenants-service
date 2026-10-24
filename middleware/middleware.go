package middleware

import (
	"defolt-tenants-service/reqid"
	"defolt-tenants-service/response"

	hk "github.com/Defolt-Labs/defolt-httpkit"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CtxInternalCaller stores the matched caller name in context.
const CtxInternalCaller = "auth.internal_caller"

const (
	RequestIDHeader     = "X-Request-ID"
	InternalKeyHeader   = "X-Internal-Service-Key"
	TenantHeader        = "X-Defolt-Tenant-ID"
	TenantSlugHeader    = "X-Defolt-Tenant-Slug"
	TenantProductHeader = "X-Defolt-Tenant-Product"
	TenantStatusHeader  = "X-Defolt-Tenant-Status"
	// ProductHeader carries the request-side product namespace signal.
	ProductHeader = "X-Defolt-Product"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = "unknown-" + uuid.NewString()[:8]
		}
		c.Set("request_id", id)
		c.Request = c.Request.WithContext(reqid.With(c.Request.Context(), id))
		c.Writer.Header().Set(RequestIDHeader, id)
		c.Next()
	}
}

func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// InternalServiceKey gates internal routes on the key set; the refusal keeps this service's envelope.
func InternalServiceKey(keys hk.KeySet) gin.HandlerFunc {
	return func(c *gin.Context) {
		caller, ok := keys.Match(c.GetHeader(InternalKeyHeader))
		if !ok {
			response.Unauthorized(c, response.ErrForbidden.Code, response.ErrForbidden.Meta)
			c.Abort()
			return
		}
		c.Set(CtxInternalCaller, caller)
		c.Next()
	}
}

// CORS attaches standard CORS headers for cross-origin browser callers.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-Internal-Service-Key, Idempotency-Key, X-Defolt-Product")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		c.Header("Access-Control-Max-Age", "600")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
