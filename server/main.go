// This is the entry point for the RLS demo server. It creates a single
// pgxpool.Pool hardened with rls.ConfigurePool (every connection defaults to
// app_user), wires up the gin router, and runs with graceful shutdown.
//
// The pool is the only database resource. It's shared by all middleware and
// handlers. The AfterConnect hook on the pool ensures that even if a request
// somehow bypasses the middleware chain, it can never run as the postgres
// superuser — the worst case is app_user with no org set, which sees nothing.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/harrisoncramer/rls-example/internal/rls"
)

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	pool, err := waitForPool(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	r := SetupRouter(pool)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r.Handler(),
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to run server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}

	pool.Close()
	log.Println("server stopped")
}

// waitForPool creates a pool where every connection defaults to the app_user
// role. This is the safety net: if a handler skips the RLS middleware entirely,
// it still runs as app_user with no org set, which means RLS denies all access
// (NULL doesn't match any UUID). Data leaks become "see nothing" bugs instead
// of "see everything" bugs.
//
// The admin middleware explicitly upgrades to app_system when needed.
func waitForPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	rls.ConfigurePool(config)

	var pool *pgxpool.Pool
	for range 30 {
		pool, err = pgxpool.NewWithConfig(ctx, config)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				return pool, nil
			}
			pool.Close()
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("gave up connecting to database: %w", err)
}
