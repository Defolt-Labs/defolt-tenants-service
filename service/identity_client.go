package service

import (
	"bytes"
	"context"
	"defolt-tenants-service/reqid"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// IdentityClient calls defolt-identity-service for tenant owner provisioning and credential management.
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
	Email      string
	FirstName  string
	MiddleName string
	LastName   string
	Password   string
}

type identityUser struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	ID     uuid.UUID `json:"id"`
	User   *struct {
		ID uuid.UUID `json:"id"`
	} `json:"user"`
}

func (u identityUser) userID() *uuid.UUID {
	if u.UserID != uuid.Nil {
		id := u.UserID
		return &id
	}
	if u.ID != uuid.Nil {
		id := u.ID
		return &id
	}
	if u.User != nil && u.User.ID != uuid.Nil {
		id := u.User.ID
		return &id
	}
	return nil
}

func (c *IdentityClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("identity-client: baseURL not configured")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Platform", c.platform)
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	if rid := reqid.From(ctx); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("identity %s %s: %d %s", method, path, resp.StatusCode, string(raw))
	}
	return raw, nil
}

// CreateUser provisions an owner account in identity, reporting whether user already existed.
func (c *IdentityClient) CreateUser(ctx context.Context, in RegisterInput) (*uuid.UUID, bool, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/internal/admin/users", map[string]any{
		"email":      in.Email,
		"firstName":  in.FirstName,
		"middleName": in.MiddleName,
		"lastName":   in.LastName,
		"password":   in.Password,
	})
	if err != nil {
		return nil, false, err
	}
	var env struct {
		Code string       `json:"code"`
		Data identityUser `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, false, fmt.Errorf("identity create-user: bad envelope: %w", err)
	}
	id := env.Data.userID()
	if id == nil {
		return nil, false, fmt.Errorf("identity create-user: envelope missing user_id")
	}
	return id, env.Code == "DL_USER_EXISTS", nil
}

// FindUserIDByEmail resolves a user ID by email via the internal admin endpoint.
func (c *IdentityClient) FindUserIDByEmail(ctx context.Context, email string) (*uuid.UUID, error) {
	path := "/api/v1/internal/admin/users/by-email?email=" + url.QueryEscape(email)
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Data identityUser `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("identity by-email: bad envelope: %w", err)
	}
	id := env.Data.userID()
	if id == nil {
		return nil, fmt.Errorf("identity by-email: envelope missing user_id")
	}
	return id, nil
}

// ResetPassword sets a fresh one-time password for the user in identity.
func (c *IdentityClient) ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	path := fmt.Sprintf("/api/v1/internal/admin/users/%s/reset-password", userID)
	_, err := c.do(ctx, http.MethodPost, path, map[string]any{
		"new_password": newPassword,
	})
	return err
}

// DeleteUser deletes an orphaned identity user upon abandoned signup cleanup.
func (c *IdentityClient) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	path := fmt.Sprintf("/api/v1/internal/admin/users/%s?reason=tenant_signup_abandoned", userID)
	_, err := c.do(ctx, http.MethodDelete, path, nil)
	return err
}
