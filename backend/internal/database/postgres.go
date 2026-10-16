// Package database opens the PostgreSQL and Redis connections.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"

	"whiteboard/internal/config"
	"whiteboard/internal/domain"
)

// OpenPostgres connects to the primary and, when configured, a read replica.
//
// With a replica DSN the dbresolver plugin routes SELECTs to the replica and
// every write (and anything wrapped in a transaction) to the primary. Code that
// must read its own writes opts in with Clauses(dbresolver.Write).
func OpenPostgres(ctx context.Context, cfg *config.Config, log *zap.Logger) (*gorm.DB, error) {
	gcfg := &gorm.Config{
		Logger:                 newGormLogger(log),
		SkipDefaultTransaction: true, // single statements are already atomic
		PrepareStmt:            true,
	}

	var db *gorm.DB
	err := retry(ctx, log, "postgres", 30, 2*time.Second, func() error {
		var err error
		db, err = gorm.Open(postgres.Open(cfg.PostgresMasterDSN), gcfg)
		if err != nil {
			return err
		}
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return sqlDB.PingContext(pingCtx)
	})
	if err != nil {
		return nil, fmt.Errorf("connect primary: %w", err)
	}

	// Menggunakan Advisory Lock agar multi-node tidak race condition saat AutoMigrate
	log.Info("acquiring database migration advisory lock...", zap.String("node", cfg.NodeID))
	for {
		var locked bool
		err := db.Raw("SELECT pg_try_advisory_lock(888333111)").Scan(&locked).Error
		if err == nil && locked {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while waiting for migration lock")
		case <-time.After(1 * time.Second):
		}
	}

	// Pastikan lock dilepas setelah migrasi selesai
	defer func() {
		db.Exec("SELECT pg_advisory_unlock(888333111)")
	}()

	// Migrations run before the resolver is registered so they always hit the
	// primary.
	if err := db.AutoMigrate(&domain.User{}, &domain.Board{}, &domain.BoardSnapshot{}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	log.Info("database migrations completed successfully")

	resolver := dbresolver.Register(dbresolver.Config{
		Sources:  []gorm.Dialector{postgres.Open(cfg.PostgresMasterDSN)},
		Replicas: replicaDialectors(cfg),
		Policy:   dbresolver.RandomPolicy{},
	}).
		SetMaxOpenConns(cfg.DBMaxOpenConns).
		SetMaxIdleConns(cfg.DBMaxIdleConns).
		SetConnMaxLifetime(30 * time.Minute)

	if err := db.Use(resolver); err != nil {
		return nil, fmt.Errorf("register resolver: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if cfg.PostgresReplicaDSN != "" {
		log.Info("postgres ready", zap.Bool("replica", true))
	} else {
		log.Info("postgres ready", zap.Bool("replica", false))
	}
	return db, nil
}

// replicaDialectors falls back to the primary when no replica is configured so
// the resolver still has a valid read pool.
func replicaDialectors(cfg *config.Config) []gorm.Dialector {
	dsn := cfg.PostgresReplicaDSN
	if dsn == "" {
		dsn = cfg.PostgresMasterDSN
	}
	return []gorm.Dialector{postgres.Open(dsn)}
}

// ClosePostgres closes the underlying pool.
func ClosePostgres(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// retry runs fn until it succeeds, the attempts are exhausted or ctx ends.
func retry(ctx context.Context, log *zap.Logger, what string, attempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := 1; i <= attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		log.Warn("waiting for dependency", zap.String("dependency", what), zap.Int("attempt", i), zap.Error(err))
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(delay):
		}
	}
	return err
}

// gormLogger adapts zap to GORM's logger interface.
type gormLogger struct {
	log           *zap.Logger
	slowThreshold time.Duration
}

func newGormLogger(l *zap.Logger) gormlogger.Interface {
	return &gormLogger{log: l.Named("gorm").WithOptions(zap.AddCallerSkip(3)), slowThreshold: 300 * time.Millisecond}
}

func (g *gormLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface { return g }

func (g *gormLogger) Info(_ context.Context, msg string, args ...any) {
	g.log.Sugar().Infof(msg, args...)
}

func (g *gormLogger) Warn(_ context.Context, msg string, args ...any) {
	g.log.Sugar().Warnf(msg, args...)
}

func (g *gormLogger) Error(_ context.Context, msg string, args ...any) {
	g.log.Sugar().Errorf(msg, args...)
}

func (g *gormLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		sql, rows := fc()
		g.log.Error("query failed", zap.Error(err), zap.Duration("elapsed", elapsed), zap.String("sql", sql), zap.Int64("rows", rows))
	case elapsed > g.slowThreshold:
		sql, rows := fc()
		g.log.Warn("slow query", zap.Duration("elapsed", elapsed), zap.String("sql", sql), zap.Int64("rows", rows))
	}
}