package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IdentityClient talks to defolt-identity-service for the public signup
// path — creates the initial admin user attached to a fresh tenant.
type IdentityClient struct {
	baseURL     string
	internalKey string
	platform    string
	http        *http.Client
}

func NewIdentityClient(baseURL, internalKey, platform string) *IdentityClient {
	return &IdentityClient{
		baseURL:     baseURL,
		internalKey: internalKey,
		platform:    platform,
		http:        &http.Client{Timeout: 12 * time.Second},
	}
}

type RegisterInput struct {
	Email     string
	FirstName string
	LastName  string
	Password  string // generated on the tenants side; user resets on first login
}

// Register creates a defolt-identity user. Best-effort — if it fails,
// the caller decides whether to roll back the tenant record.
func (c *IdentityClient) Register(ctx context.Context, in RegisterInput) error {
	if c.baseURL == "" {
		return fmt.Errorf("identity-client: baseURL not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"email":      in.Email,
		"first_name": in.FirstName,
		"last_name":  in.LastName,
		"password":   in.Password,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Platform", c.platform)
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("identity Register: %d %s", resp.StatusCode, string(raw))
	}
	return nil
}
