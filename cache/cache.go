// Package cache wraps Redis for slug lookup and idempotency caching.
package cache

import (
	"context"
	"errors"
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

// Connect parses REDIS_URL and returns a client, falling back to nil on error.
func Connect(redisURL string) *Cache {
	if redisURL == "" {
		logger.LogInfo("cache", "REDIS_URL empty; running cache-less")
		return nil
	}
	opt, err := parseRedisURL(redisURL)
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

// parseRedisURL hides the parse error, whose text can repeat the URL with its password.
func parseRedisURL(redisURL string) (*redis.Options, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, errors.New("REDIS_URL will not parse")
	}
	return opt, nil
}

func (c *Cache) Close() {
	if c == nil || c.rdb == nil {
		return
	}
	_ = c.rdb.Close()
}

// slugKey scopes the key by product namespace and slug.
func slugKey(product, slug string) string { return "tenant:slug:" + product + ":" + slug }

// GetSlug returns the cached slim-tenant JSON for a (product, slug), or (nil, false).
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

// SetSlug stores the slim-tenant JSON with a 30-second TTL.
func (c *Cache) SetSlug(ctx context.Context, product, slug string, body []byte) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.Set(ctx, slugKey(product, slug), body, slugTTL).Err(); err != nil {
		logger.LogWarn("", "cache", "SET "+slugKey(product, slug)+": "+err.Error())
	}
}

// InvalidateSlug removes the cached entry for all replicas.
func (c *Cache) InvalidateSlug(ctx context.Context, product, slug string) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.Del(ctx, slugKey(product, slug)).Err(); err != nil {
		logger.LogWarn("", "cache", "DEL "+slugKey(product, slug)+": "+err.Error())
	}
}

// GetIdempotent returns a previously stored response for an Idempotency-Key.
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

// StoreIdempotent stores a success response under the Idempotency-Key.
func (c *Cache) StoreIdempotent(ctx context.Context, key string, body []byte) {
	if c == nil || c.rdb == nil {
		return
	}
	if err := c.rdb.SetNX(ctx, key, body, idemTTL).Err(); err != nil {
		logger.LogWarn("", "cache", "SETNX "+key+": "+err.Error())
	}
}
