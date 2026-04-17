package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SetupRouter creates the gin router with three layers of defense:
//
//  1. Pool level (rls.ConfigurePool): every connection defaults to app_user.
//     A route that somehow bypasses all middleware sees nothing.
//
//  2. Global Conn() middleware: acquires a connection per request and sets
//     app.current_org from X-Organization-ID when present. If absent, the
//     connection is still app_user with no org — RLS denies all access.
//
//  3. Route-group level:
//     - Admin group: upgrades the connection to app_system (bypass RLS).
//       This is the only way to opt out of tenant scoping.
//     - Scoped group: enforces X-Organization-ID via RequireOrg(). The org
//       was already set by Conn() — RequireOrg just validates it was present.
//
// A new route added to the scoped group automatically inherits RLS enforcement.
// A new route added directly to the engine (outside both groups) gets a
// connection via Conn() but with no org set — it sees nothing. Safety is the
// default; you have to explicitly opt into admin access.
func SetupRouter(pool *pgxpool.Pool) *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	rlsMiddleware := NewRLSMiddleware(pool)
	r.Use(rlsMiddleware.Conn())

	h := NewHandler(pool)

	// Admin routes opt out of RLS by upgrading the connection to app_system.
	// These are for cross-tenant operations like creating organizations,
	// running backfills, or viewing platform-wide analytics.
	admin := r.Group("/admin")
	admin.Use(rlsMiddleware.Admin())
	{
		admin.POST("/organizations", h.CreateOrganization)
		admin.GET("/organizations", h.ListOrganizations)
	}

	// Scoped routes require X-Organization-ID. The Conn() middleware already
	// set app.current_org on the connection — RequireOrg() just rejects
	// requests that didn't provide the header. Handlers never touch
	// organization_id directly; it's auto-populated via column defaults
	// from the session variable.
	scoped := r.Group("", RequireOrg())
	{
		scoped.POST("/programs", h.CreateProgram)
		scoped.GET("/programs", h.ListPrograms)

		scoped.POST("/transfers", h.CreateTransfer)
		scoped.GET("/transfers", h.ListTransfers)

		scoped.POST("/ledger-entries", h.CreateLedgerEntry)
		scoped.GET("/ledger-entries", h.ListLedgerEntries)
	}

	return r
}
