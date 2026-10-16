// Command server runs one node of the collaborative whiteboard backend.
//
// Any number of nodes can run side by side behind a load balancer: they share
// PostgreSQL for durable storage and Redis for live synchronisation.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"whiteboard/internal/auth"
	"whiteboard/internal/config"
	"whiteboard/internal/database"
	"whiteboard/internal/logger"
	"whiteboard/internal/pool"
	"whiteboard/internal/repository"
	"whiteboard/internal/service"
	"whiteboard/internal/transport/httpapi"
	"whiteboard/internal/websocket"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log, err := logger.New(cfg.Production(), cfg.LogLevel, cfg.NodeID)
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer func() { _ = log.Sync() }()

	// Cancelled on SIGINT/SIGTERM; everything below winds down from it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- infrastructure ------------------------------------------------------
	db, err := database.OpenPostgres(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = database.ClosePostgres(db) }()

	rdb, err := database.OpenRedis(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	workers := pool.New(cfg.Workers, cfg.PoolQueue, func(r any) {
		log.Error("worker job panicked", zap.Any("panic", r), zap.Stack("stack"))
	})

	// --- application ---------------------------------------------------------
	users := repository.NewUserRepository(db)
	boards := repository.NewBoardRepository(db)
	snapshots := repository.NewSnapshotRepository(db)

	authSvc := service.NewAuth(users, auth.NewSigner(cfg.AuthSecret), cfg.TokenTTL)
	boardSvc := service.NewBoards(boards)
	snapshotSvc := service.NewSnapshots(snapshots, boards)

	hub := websocket.NewHub(websocket.Options{
		NodeID:           cfg.NodeID,
		AllowedOrigins:   cfg.AllowedOrigins,
		SnapshotDebounce: cfg.SnapshotDebounce,
		SnapshotMaxWait:  cfg.SnapshotMaxWait,
		IdleTimeout:      cfg.RoomIdleTimeout,
		MaxRoomClients:   cfg.MaxRoomClients,
	}, snapshotSvc, workers, log)

	bus, err := websocket.NewBus(rdb, cfg.NodeID, log, hub.Dispatch)
	if err != nil {
		return err
	}
	hub.SetBus(bus)

	api, handler := httpapi.New(httpapi.Deps{
		Log:            log,
		Auth:           authSvc,
		Boards:         boardSvc,
		Hub:            hub,
		AllowedOrigins: cfg.AllowedOrigins,
		TrustProxy:     cfg.TrustProxy,
		Ready: map[string]httpapi.Pinger{
			"postgres": gormPinger{db},
			"redis":    redisPinger{rdb},
		},
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", zap.String("addr", cfg.HTTPAddr), zap.String("env", cfg.Env))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	// --- run until told to stop ----------------------------------------------
	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown requested")
	}

	// Graceful shutdown, in dependency order:
	//  1. report not-ready so the load balancer stops routing here;
	//  2. stop the HTTP listener (WebSockets are hijacked, so the hub owns them);
	//  3. close rooms: tell clients to reconnect elsewhere, flush final snapshots;
	//  4. drain the worker pool, then close Redis and Postgres (deferred).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	api.BeginDrain()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", zap.Error(err))
	}
	if err := hub.Shutdown(shutdownCtx); err != nil {
		log.Warn("hub shutdown incomplete", zap.Error(err))
	}
	if err := workers.Shutdown(shutdownCtx); err != nil {
		log.Warn("worker pool shutdown incomplete", zap.Error(err))
	}
	bus.Close()
	log.Info("shutdown complete")
	return nil
}

type gormPinger struct{ db *gorm.DB }

func (p gormPinger) Ping(ctx context.Context) error {
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

type redisPinger struct{ c redis.UniversalClient }

func (p redisPinger) Ping(ctx context.Context) error { return p.c.Ping(ctx).Err() }
