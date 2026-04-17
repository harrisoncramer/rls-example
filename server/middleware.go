package main

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/harrisoncramer/rls-example/internal/rls"
)

const connCtxKey = "db_conn"

// RLSMiddleware manages per-request connection acquisition with the appropriate
// role and org context. This mirrors how Chariot services handle auth middleware —
// the middleware resolves identity, sets up the connection, and downstream handlers
// just pull the connection from the gin context.
type RLSMiddleware struct {
	pool *pgxpool.Pool
}

// NewRLSMiddleware creates middleware backed by the given pool.
func NewRLSMiddleware(pool *pgxpool.Pool) *RLSMiddleware {
	return &RLSMiddleware{pool: pool}
}

// Scoped returns gin middleware that acquires a connection, switches to app_user,
// and sets app.current_org from the X-Organization-ID header. The connection is
// cleaned up after the handler chain completes.
func (m *RLSMiddleware) Scoped() gin.HandlerFunc {
	return func(c *gin.Context) {
		orgHeader := c.GetHeader("X-Organization-ID")
		if orgHeader == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Organization-ID header is required"})
			return
		}

		orgID, err := uuid.Parse(orgHeader)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Organization-ID must be a valid UUID"})
			return
		}

		conn, err := m.pool.Acquire(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to acquire connection: %v", err)})
			return
		}
		defer func() {
			_ = rls.ResetRole(c.Request.Context(), conn)
			_ = rls.ResetOrg(c.Request.Context(), conn)
			conn.Release()
		}()

		if err := rls.SetRole(c.Request.Context(), conn, "app_user"); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set role: %v", err)})
			return
		}

		if err := rls.SetOrg(c.Request.Context(), conn, orgID); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set org: %v", err)})
			return
		}

		c.Set(connCtxKey, conn)
		c.Next()
	}
}

// Admin returns gin middleware that acquires a connection and switches to
// app_system. No org scoping — the handler sees all data across all tenants.
func (m *RLSMiddleware) Admin() gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := m.pool.Acquire(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to acquire connection: %v", err)})
			return
		}
		defer func() {
			_ = rls.ResetRole(c.Request.Context(), conn)
			conn.Release()
		}()

		if err := rls.SetRole(c.Request.Context(), conn, "app_system"); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set role: %v", err)})
			return
		}

		c.Set(connCtxKey, conn)
		c.Next()
	}
}

// ConnFromContext extracts the scoped database connection from the gin context.
func ConnFromContext(c *gin.Context) *pgxpool.Conn {
	conn, _ := c.Get(connCtxKey)
	return conn.(*pgxpool.Conn)
}
