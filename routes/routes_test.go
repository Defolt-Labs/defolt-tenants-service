package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"defolt-tenants-service/handler"

	hk "github.com/Defolt-Labs/defolt-httpkit"
	"github.com/gin-gonic/gin"
)

func probe(t *testing.T, ping func(context.Context) error, path string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	hk.ReadinessFailedLog = nil
	e := gin.New()
	MountProbes(e, &handler.Handlers{}, hk.Readiness(ping, nil))
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: body %q is not JSON: %v", path, w.Body.String(), err)
	}
	return w.Code, body
}

func TestReadyAnswers200WhenTheDatabaseAnswers(t *testing.T) {
	code, body := probe(t, func(context.Context) error { return nil }, "/ready")
	if code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("GET /ready = %d %v, want 200 ready", code, body)
	}
}

func TestReadyAnswers503WhenTheDatabasePingFails(t *testing.T) {
	code, body := probe(t, func(context.Context) error { return errors.New("connection refused") }, "/ready")
	if code != http.StatusServiceUnavailable || body["status"] != "db_unreachable" {
		t.Fatalf("GET /ready = %d %v, want 503 db_unreachable", code, body)
	}
}
