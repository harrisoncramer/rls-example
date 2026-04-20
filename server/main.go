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
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/harrisoncramer/rls-example/internal/rls"
)

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	pool, err := rls.GetPool(ctx, dbURL)
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
