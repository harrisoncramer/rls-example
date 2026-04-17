// Middleware for managing per-request database transactions with RLS.
//
// Every request is wrapped in a transaction with SET LOCAL for role and org
// context. SET LOCAL is automatically discarded when the transaction commits
// or rolls back, so there's no risk of leaking tenant context between pool
// checkouts. This is the industry standard pattern used by Supabase,
// PostgREST, and Citus.
//
// The middleware stack has three pieces:
//
//   - Conn(): Global middleware. Starts a transaction per request and sets
//     app.current_org via SET LOCAL when the X-Organization-ID header is
//     present. Commits on success, rolls back on error. Every request gets
//     a transaction; handlers pull it from gin.Context via TxFromContext().
//
//   - RequireOrg(): Lightweight guard. No DB work — just rejects requests
//     without the org header. Applied to route groups that need tenant context.
//
//   - Admin(): Runs SET LOCAL ROLE app_system on the transaction that was
//     already started by Conn(). Applied to admin route groups that need
//     cross-tenant access.
//
// Conn() and Admin() operate on the same transaction. Conn() starts it and
// Admin() upgrades the role. This means we never hold two connections per
// request, and the role upgrade only happens for routes that explicitly
// opt into it.
package main

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/harrisoncramer/rls-example/internal/rls"
)

const txCtxKey = "db_tx"

// RLSMiddleware manages per-request transactions with RLS context.
type RLSMiddleware struct {
	pool *pgxpool.Pool
}

// NewRLSMiddleware creates middleware backed by the given pool.
func NewRLSMiddleware(pool *pgxpool.Pool) *RLSMiddleware {
	return &RLSMiddleware{pool: pool}
}

// Conn is global middleware that starts a transaction for every request. If
// the X-Organization-ID header is present, it sets app.current_org via SET
// LOCAL so queries are scoped to that tenant. If the header is absent, the
// transaction still runs as app_user with no org — RLS denies all access.
//
// The transaction is committed if the handler chain completes without errors,
// and rolled back otherwise. SET LOCAL is automatically discarded in both cases.
func (m *RLSMiddleware) Conn() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		tx, err := m.pool.Begin(ctx)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to begin transaction: %v", err)})
			return
		}

		if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_user"); err != nil {
			tx.Rollback(ctx)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set role: %v", err)})
			return
		}

		if orgHeader := c.GetHeader("X-Organization-ID"); orgHeader != "" {
			orgID, err := uuid.Parse(orgHeader)
			if err != nil {
				tx.Rollback(ctx)
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "X-Organization-ID must be a valid UUID"})
				return
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL app.current_org = '%s'", orgID.String())); err != nil {
				tx.Rollback(ctx)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set org: %v", err)})
				return
			}
		}

		c.Set(txCtxKey, tx)
		c.Next()

		if len(c.Errors) > 0 {
			tx.Rollback(ctx)
		} else {
			tx.Commit(ctx)
		}
	}
}

// RequireOrg is middleware that rejects requests without a valid
// X-Organization-ID header. The org has already been set by Conn() — this
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

// Admin runs SET LOCAL ROLE app_system on the transaction already started by
// Conn(). The handler sees all data across all tenants. Since SET LOCAL is
// transaction-scoped, the role upgrade is automatically discarded on
// commit/rollback.
func (m *RLSMiddleware) Admin() gin.HandlerFunc {
	return func(c *gin.Context) {
		tx := TxFromContext(c)
		ctx := c.Request.Context()

		if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_system"); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to set admin role: %v", err)})
			return
		}

		c.Next()
	}
}

// TxFromContext extracts the per-request transaction from the gin context.
// Handlers use this to run SQLC queries within the RLS-scoped transaction.
func TxFromContext(c *gin.Context) pgx.Tx {
	tx, _ := c.Get(txCtxKey)
	return tx.(pgx.Tx)
}

// ConnFromContext extracts a DBTX from the gin context. This returns the
// transaction as the DBTX interface that SQLC's db.New() accepts.
func ConnFromContext(c *gin.Context) rls.DBTX {
	return TxFromContext(c)
}
