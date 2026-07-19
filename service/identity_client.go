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

// IdentityClient talks to defolt-identity-service for the public signup
// path — creates the initial admin user attached to a fresh tenant —
// plus the owner-lifecycle admin calls (reset password, lookup by
// email, best-effort delete on abandoned signups).
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

// identityUser covers the envelope shapes identity returns: the
// by-email lookup uses `user_id` (+ `name`), while register may carry
// the user object as data = {...} or data = {"user": {...}}.
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
	// defolt-identity rejects every request without this header. Never
	// generated here: the RequestID middleware already guarantees one.
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

// Register creates a defolt-identity user and returns the created
// user's id parsed from the identity envelope (nil when the envelope
// carries no id — the caller backfills lazily via FindUserIDByEmail).
// Best-effort: if it fails, the caller decides whether to roll back
// the tenant record.
func (c *IdentityClient) Register(ctx context.Context, in RegisterInput) (*uuid.UUID, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/auth/register", map[string]any{
		// camelCase: defolt-identity's RegisterRequest binds firstName /
		// lastName, and both are `binding:"required"`, so snake_case keys
		// bound to empty strings and failed validation.
		"email":     in.Email,
		"firstName": in.FirstName,
		"lastName":  in.LastName,
		"password":  in.Password,
	})
	if err != nil {
		return nil, err
	}
	var env struct {
		Data identityUser `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil // user created; envelope shape unexpected — backfill later
	}
	return env.Data.userID(), nil
}

// FindUserIDByEmail resolves a user id via the internal admin lookup.
// Identity answers 200 DL_USER_FOUND with data.user_id (and data.name)
// or 404 DL_USER_NOT_FOUND.
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

// ResetPassword sets a fresh one-time password on the owner user. The
// password must satisfy identity's complexity policy (min 8, mixed
// case, digit) — generatePassword guarantees that.
func (c *IdentityClient) ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	path := fmt.Sprintf("/api/v1/internal/admin/users/%s/reset-password", userID)
	_, err := c.do(ctx, http.MethodPost, path, map[string]any{
		"new_password": newPassword,
	})
	return err
}

// DeleteUser removes an identity user. Used best-effort by the
// abandoned-signup sweeper so orphaned Store Admin accounts do not
// linger after their tenant is reaped.
func (c *IdentityClient) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	path := fmt.Sprintf("/api/v1/internal/admin/users/%s", userID)
	_, err := c.do(ctx, http.MethodDelete, path, nil)
	return err
}
