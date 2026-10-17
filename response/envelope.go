package response

import (
	"net/http"
	"strconv"
	"strings"

	"defolt-tenants-service/logger"

	"github.com/gin-gonic/gin"
)

// Meta holds bilingual response messages.
type Meta struct {
	EN string `json:"en"`
	SW string `json:"sw"`
}

// Envelope is the standard response structure.
type Envelope struct {
	Code      string      `json:"code"`
	Meta      Meta        `json:"meta"`
	Data      interface{} `json:"data,omitempty"`
	Details   interface{} `json:"details,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

func write(c *gin.Context, status int, code string, meta Meta, data, details any) {
	if status >= http.StatusInternalServerError {
		logServerError(c, status, code)
	}
	c.JSON(status, Envelope{Code: code, Meta: meta, Data: data, Details: details, RequestID: requestID(c)})
}

// requestID retrieves request ID from gin context.
func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// RequestIDOf returns request ID from gin context for custom envelope building.
func RequestIDOf(c *gin.Context) string { return requestID(c) }

// Status responds with bare status code without body while logging 5xx errors.
func Status(c *gin.Context, status int, code string) {
	if status >= http.StatusInternalServerError {
		logServerError(c, status, code)
	}
	c.Status(status)
}

// logServerError ensures 5xx responses are logged with trace and error details.
func logServerError(c *gin.Context, status int, code string) {
	msg := c.Request.Method + " " + c.FullPath() + " -> " + strconv.Itoa(status) + " " + code
	if causes := c.Errors.Errors(); len(causes) > 0 {
		msg += ": " + strings.Join(causes, "; ")
	}
	logger.LogError(requestID(c), "http-5xx", msg)
}
