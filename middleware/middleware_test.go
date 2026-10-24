package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hk "github.com/Defolt-Labs/defolt-httpkit"
	"github.com/gin-gonic/gin"
)

func callInternal(t *testing.T, set hk.KeySet, presented string) (int, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var caller string
	r := gin.New()
	r.Use(RequestID())
	r.GET("/x", InternalServiceKey(set), func(c *gin.Context) {
		caller = c.GetString(CtxInternalCaller)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if presented != "" {
		req.Header.Set(InternalKeyHeader, presented)
	}
	req.Header.Set(RequestIDHeader, "req-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String(), caller
}

func TestInternalServiceKeyLetsEachNamedCallerIn(t *testing.T) {
	set, err := hk.NewKeySet("", "platform=k1, dhs=k2")
	if err != nil {
		t.Fatal(err)
	}
	for presented, want := range map[string]string{"k1": "platform", "k2": "dhs"} {
		code, _, caller := callInternal(t, set, presented)
		if code != http.StatusOK || caller != want {
			t.Fatalf("key %q: status %d caller %q, want 200 %q", presented, code, caller, want)
		}
	}
}

func TestInternalServiceKeyRefusesWithTheServiceEnvelope(t *testing.T) {
	set, err := hk.NewKeySet("", "platform=k1")
	if err != nil {
		t.Fatal(err)
	}
	for _, presented := range []string{"", "old", "platform=k1"} {
		code, body, _ := callInternal(t, set, presented)
		if code != http.StatusUnauthorized {
			t.Fatalf("key %q: status %d, want 401", presented, code)
		}
		if !strings.Contains(body, `"code":"DL_FORBIDDEN"`) || !strings.Contains(body, `"request_id":"req-1"`) {
			t.Fatalf("key %q: body %s lacks the service refusal code or request id", presented, body)
		}
	}
}

func TestInternalServiceKeyKeepsASingleKeyWhole(t *testing.T) {
	set, err := hk.NewKeySet("a=b", "")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, caller := callInternal(t, set, "a=b"); code != http.StatusOK || caller != hk.BareKeyName {
		t.Fatalf("status %d caller %q, want 200 %q", code, caller, hk.BareKeyName)
	}
	if code, _, _ := callInternal(t, set, "b"); code != http.StatusUnauthorized {
		t.Fatalf("a part of the key was accepted: %d", code)
	}
}
