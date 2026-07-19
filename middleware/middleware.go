package middleware

import (
	"crypto/subtle"

	"defolt-tenants-service/reqid"
	"defolt-tenants-service/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	RequestIDHeader     = "X-Request-ID"
	InternalKeyHeader   = "X-Internal-Service-Key"
	TenantHeader        = "X-Defolt-Tenant-ID"
	TenantSlugHeader    = "X-Defolt-Tenant-Slug"
	TenantProductHeader = "X-Defolt-Tenant-Product"
	TenantStatusHeader  = "X-Defolt-Tenant-Status"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = "unknown-" + uuid.NewString()[:8]
		}
		c.Set("request_id", id)
		// Also carry it on the request context: handlers pass
		// c.Request.Context() down, and the outbound service clients
		// must forward the ID or defolt-identity rejects the call.
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

// InternalServiceKey gates the /api/v1/tenants create + admin routes
// to service-to-service callers only. Constant-time compare to keep
// timing attacks off the table.
func InternalServiceKey(sharedSecret string) gin.HandlerFunc {
	want := []byte(sharedSecret)
	return func(c *gin.Context) {
		got := c.GetHeader(InternalKeyHeader)
		if len(got) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			response.Unauthorized(c, response.ErrForbidden.Code, response.ErrForbidden.Meta)
			c.Abort()
			return
		}
		c.Next()
	}
}

// CORS is a permissive dev-friendly setup; production tightens it at
// the Traefik ingress. Kept here for symmetry with the fleet.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-Internal-Service-Key")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
