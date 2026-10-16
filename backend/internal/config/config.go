// Package config loads runtime settings from environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the complete runtime configuration of one server node.
type Config struct {
	Env      string // "production" or "development"
	LogLevel string
	NodeID   string

	HTTPAddr        string
	AllowedOrigins  []string // exact origins, or "*" to allow any
	TrustProxy      bool     // honour X-Forwarded-For for rate limiting
	ShutdownTimeout time.Duration

	PostgresMasterDSN  string
	PostgresReplicaDSN string // optional; empty disables read replicas
	DBMaxOpenConns     int
	DBMaxIdleConns     int

	RedisAddrs    []string // one address = standalone, several = cluster
	RedisPassword string
	RedisDB       int

	AuthSecret string
	TokenTTL   time.Duration

	Workers          int
	PoolQueue        int
	SnapshotDebounce time.Duration
	SnapshotMaxWait  time.Duration
	RoomIdleTimeout  time.Duration
	MaxRoomClients   int
}

// Load reads and validates configuration.
func Load() (*Config, error) {
	c := &Config{
		Env:      str("APP_ENV", "development"),
		LogLevel: str("LOG_LEVEL", "info"),
		NodeID:   str("NODE_ID", ""),

		HTTPAddr:        str("HTTP_ADDR", ":8080"),
		AllowedOrigins:  list("ALLOWED_ORIGINS", "http://localhost:3000"),
		TrustProxy:      boolean("TRUST_PROXY", false),
		ShutdownTimeout: duration("SHUTDOWN_TIMEOUT", 20*time.Second),

		PostgresMasterDSN:  str("POSTGRES_MASTER_DSN", ""),
		PostgresReplicaDSN: str("POSTGRES_REPLICA_DSN", ""),
		DBMaxOpenConns:     integer("DB_MAX_OPEN_CONNS", 20),
		DBMaxIdleConns:     integer("DB_MAX_IDLE_CONNS", 5),

		RedisAddrs:    list("REDIS_ADDRS", "localhost:6379"),
		RedisPassword: str("REDIS_PASSWORD", ""),
		RedisDB:       integer("REDIS_DB", 0),

		AuthSecret: str("AUTH_SECRET", ""),
		TokenTTL:   duration("TOKEN_TTL", 30*24*time.Hour),

		Workers:          integer("WORKERS", 8),
		PoolQueue:        integer("POOL_QUEUE", 1024),
		SnapshotDebounce: duration("SNAPSHOT_DEBOUNCE", 2*time.Second),
		SnapshotMaxWait:  duration("SNAPSHOT_MAX_WAIT", 15*time.Second),
		RoomIdleTimeout:  duration("ROOM_IDLE_TIMEOUT", 30*time.Second),
		MaxRoomClients:   integer("MAX_ROOM_CLIENTS", 200),
	}

	if c.NodeID == "" {
		host, _ := os.Hostname()
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		c.NodeID = host + "-" + hex.EncodeToString(b)
	}
	return c, c.validate()
}

// Production reports whether the node runs in production mode.
func (c *Config) Production() bool { return c.Env == "production" }

func (c *Config) validate() error {
	var errs []error
	if c.PostgresMasterDSN == "" {
		errs = append(errs, errors.New("POSTGRES_MASTER_DSN is required"))
	}
	if len(c.RedisAddrs) == 0 {
		errs = append(errs, errors.New("REDIS_ADDRS is required"))
	}
	if len(c.AuthSecret) < 32 {
		errs = append(errs, errors.New("AUTH_SECRET must be at least 32 characters"))
	}
	if c.Workers < 1 || c.PoolQueue < 1 {
		errs = append(errs, errors.New("WORKERS and POOL_QUEUE must be positive"))
	}
	if c.SnapshotDebounce <= 0 || c.SnapshotMaxWait < c.SnapshotDebounce {
		errs = append(errs, errors.New("SNAPSHOT_MAX_WAIT must be >= SNAPSHOT_DEBOUNCE > 0"))
	}
	if c.Production() && contains(c.AllowedOrigins, "*") {
		errs = append(errs, errors.New("ALLOWED_ORIGINS must not be * in production"))
	}
	return errors.Join(errs...)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func list(key, def string) []string {
	var out []string
	for _, p := range strings.Split(str(key, def), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func integer(key string, def int) int {
	v := str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("config: %s=%q is not an integer", key, v))
	}
	return n
}

func boolean(key string, def bool) bool {
	v := str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("config: %s=%q is not a boolean", key, v))
	}
	return b
}

func duration(key string, def time.Duration) time.Duration {
	v := str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("config: %s=%q is not a duration", key, v))
	}
	return d
}
