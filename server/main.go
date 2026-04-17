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

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
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

	r := gin.Default()
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	rlsMiddleware := NewRLSMiddleware(pool)
	h := NewHandler(pool)

	admin := r.Group("/admin")
	admin.Use(rlsMiddleware.Admin())
	{
		admin.POST("/organizations", h.CreateOrganization)
		admin.GET("/organizations", h.ListOrganizations)
	}

	scoped := r.Group("")
	scoped.Use(rlsMiddleware.Scoped())
	{
		scoped.POST("/programs", h.CreateProgram)
		scoped.GET("/programs", h.ListPrograms)

		scoped.POST("/transfers", h.CreateTransfer)
		scoped.GET("/transfers", h.ListTransfers)

		scoped.POST("/ledger-entries", h.CreateLedgerEntry)
		scoped.GET("/ledger-entries", h.ListLedgerEntries)
	}

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

func waitForPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	var err error
	for range 30 {
		pool, err = pgxpool.New(ctx, dbURL)
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
