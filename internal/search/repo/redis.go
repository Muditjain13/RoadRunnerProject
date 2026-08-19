// Package repo is search's Redis-backed result cache.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Muditjain13/transport-app/internal/search/service"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

func (c *RedisCache) Get(ctx context.Context, key string) ([]service.Trip, bool, error) {
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("redis get %s: %w", key, err)
	}
	var trips []service.Trip
	if err := json.Unmarshal(data, &trips); err != nil {
		return nil, false, fmt.Errorf("unmarshal cached trips: %w", err)
	}
	return trips, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, trips []service.Trip, ttl time.Duration) error {
	data, err := json.Marshal(trips)
	if err != nil {
		return fmt.Errorf("marshal trips: %w", err)
	}
	if err := c.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	return nil
}
