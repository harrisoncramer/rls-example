// Middleware for managing per-request database connections with RLS.
//
// The middleware stack has three pieces that work together:
//
//   - Conn(): Global middleware. Acquires a pooled connection (already app_user
//     via AfterConnect) and sets app.current_org from the X-Organization-ID
//     header when present. Every request gets a connection; handlers pull it
//     from gin.Context via ConnFromContext(). Connection is released after the
//     handler chain completes.
//
//   - RequireOrg(): Lightweight guard. No DB work — just rejects requests
//     without the org header. Applied to route groups that need tenant context.
//
//   - Admin(): Upgrades the connection (already in context from Conn()) from
//     app_user to app_system. Applied to admin route groups that need
//     cross-tenant access. Resets the role back to app_user after the handler.
//
// The key property: Conn() and Admin() operate on the same connection. Conn()
// acquires it and Admin() upgrades it. This means we never hold two connections
// per request, and the role upgrade only happens for routes that explicitly
// opt into it.
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

// RLSMiddleware manages per-request connection acquisition. The pool defaults
// to app_user via rls.ConfigurePool, so every connection is RLS-enforced from
// the start. The global middleware acquires a connection and optionally sets
// the org context. Admin routes explicitly upgrade to app_system.
type RLSMiddleware struct {
	pool *pgxpool.Pool
}

// NewRLSMiddleware creates middleware backed by the given pool.
func NewRLSMiddleware(pool *pgxpool.Pool) *RLSMiddleware {
	return &RLSMiddleware{pool: pool}
}

// Conn is global middleware that acquires a connection for every request. If
// the X-Organization-ID header is present, it sets app.current_org so queries
// are scoped to that tenant. If the header is absent, the connection still
// defaults to app_user with no org — RLS denies all access (safe default).
//
// This runs on every route. Admin middleware later upgrades the role to
// app_system on the same connection.
func (m *RLSMiddleware) Conn() gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := m.pool.Acquire(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to acquire connection: %v", err)})
			return
		}
		defer func() {
			_ = rls.ResetOrg(c.Request.Context(), conn)
			conn.Release()
		}()

		if orgHeader := c.GetHeader("X-Organization-ID"); orgHeader != "" {
			orgID, err := uuid.Parse(orgHeader)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Organization-ID must be a valid UUID"})
				return
			}
			if err := rls.SetOrg(c.Request.Context(), conn, orgID); err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set org: %v", err)})
				return
			}
		}

		c.Set(connCtxKey, conn)
		c.Next()
	}
}

// RequireOrg is middleware that rejects requests without a valid
// X-Organization-ID header. Apply this to route groups where tenant context
// is mandatory (most routes). The org has already been set by Conn() — this
// just enforces that it was present.
func RequireOrg() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-Organization-ID") == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Organization-ID header is required"})
			return
		}
		c.Next()
	}
}

// Admin returns middleware that upgrades the connection (already acquired by
// Conn()) from app_user to app_system. The handler sees all data across all
// tenants. The role is reset back to app_user on release so the pool's
// default invariant is maintained.
func (m *RLSMiddleware) Admin() gin.HandlerFunc {
	return func(c *gin.Context) {
		conn := ConnFromContext(c)
		ctx := c.Request.Context()

		if err := rls.SetRole(ctx, conn, "app_system"); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set role: %v", err)})
			return
		}

		c.Next()

		_ = rls.SetRole(ctx, conn, "app_user")
	}
}

// ConnFromContext extracts the database connection from the gin context.
func ConnFromContext(c *gin.Context) *pgxpool.Conn {
	conn, _ := c.Get(connCtxKey)
	return conn.(*pgxpool.Conn)
}
