package database

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"whiteboard/internal/config"
)

// OpenRedis connects to Redis. One address gives a standalone client; several
// addresses give a Redis Cluster client. Pub/Sub works the same on both, so the
// rest of the code only sees redis.UniversalClient.
func OpenRedis(ctx context.Context, cfg *config.Config, log *zap.Logger) (redis.UniversalClient, error) {
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        cfg.RedisAddrs,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		PoolSize:     32,
		MinIdleConns: 2,
	})

	err := retry(ctx, log, "redis", 30, 2*time.Second, func() error {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return client.Ping(pingCtx).Err()
	})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	log.Info("redis ready", zap.Strings("addrs", cfg.RedisAddrs), zap.Bool("cluster", len(cfg.RedisAddrs) > 1))
	return client, nil
}
