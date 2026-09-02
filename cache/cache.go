// Package cache wraps the shared fleet Redis for the two things this
// service needs it for (plan §5.3, §5.13, §5.19):
//
//  1. the 30-second slug-lookup cache in front of resolve-host, and
//  2. best-effort Idempotency-Key replay storage (24h TTL).
//
// The whole package is nil-safe: if Redis is unreachable at boot the
// service runs cache-less and every method degrades to a no-op / miss.
package cache

import (
	"context"
	"time"

	"defolt-tenants-service/logger"

	"github.com/redis/go-redis/v9"
)

const (
	slugTTL = 30 * time.Second
	idemTTL = 24 * time.Hour
)

type Cache struct {
	rdb *redis.Client
}

// Connect parses REDIS_URL and pings once. Failure is non-fatal: we
// log and return nil so callers fall through to Postgres.
func Connect(redisURL string) *Cache {
	if redisURL == "" {
		logger.LogInfo("cache", "REDIS_URL empty; running cache-less")
		return nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.LogWarn("", "cache", "invalid REDIS_URL, running cache-less: "+err.Error())
		return nil
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.LogWarn("", "cache", "redis unreachable, running cache-less: "+err.Error())
		_ = rdb.Close()
		return nil
	}
	logger.LogInfo("cache", "redis connected")
	return &Cache{rdb: rdb}
}

func (c *Cache) Close() {
	if c == nil || c.rdb == nil {
		return
	}
	_ = c.rdb.Close()
}

// slugKey is keyed by (product, slug), not slug alone: tenant slugs are
// unique only per product, so a bare-slug key would cross-serve a `drs`
// tenant's slim record to a `health` request that reused the same slug
// (or vice versa) for the 30s TTL. The product segment keeps the two
// namespaces from poisoning each other.
func slugKey(product, slug string) string { return "tenant:slug:" + product + ":" + slug }

// GetSlug returns the cached slim-tenant JSON for a (product, slug), or
// (nil, false) on miss / cache-less operation.
func (c *Cache) GetSlug(ctx context.Context, product, slug string) ([]byte, bool) {
	if c == nil || c.rdb == nil {
		return nil, false
	}
	b, err := c.rdb.Get(ctx, slugKey(product, slug)).Bytes()
	if err != nil {
		return nil, false
	}
	return b, true
}

// SetSlug stores the slim-tenant JSON with the 30s TTL. Best effort.
func (c *Cache) SetSlug(ctx context.Context, product, slug string, body []byte) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.Set(ctx, slugKey(product, slug), body, slugTTL).Err(); err != nil {
		logger.LogWarn("", "cache", "SET "+slugKey(product, slug)+": "+err.Error())
	}
}

// InvalidateSlug drops the cached entry. Shared Redis means one DEL
// serves every replica.
func (c *Cache) InvalidateSlug(ctx context.Context, product, slug string) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.Del(ctx, slugKey(product, slug)).Err(); err != nil {
		logger.LogWarn("", "cache", "DEL "+slugKey(product, slug)+": "+err.Error())
	}
}

// GetIdempotent returns a previously stored success response for an
// Idempotency-Key, or (nil, false).
func (c *Cache) GetIdempotent(ctx context.Context, key string) ([]byte, bool) {
	if c == nil || c.rdb == nil {
		return nil, false
	}
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	return b, true
}

// StoreIdempotent stores a success response under the Idempotency-Key
// with SET NX EX 86400. Best effort: a lost write just means the
// retry re-executes, which the slug uniqueness guard already absorbs.
func (c *Cache) StoreIdempotent(ctx context.Context, key string, body []byte) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.SetNX(ctx, key, body, idemTTL).Err(); err != nil {
		logger.LogWarn("", "cache", "SETNX "+key+": "+err.Error())
	}
}
