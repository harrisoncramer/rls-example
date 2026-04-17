package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SetupRouter creates and configures the gin router. The pool is hardened
// via rls.ConfigurePool so every connection defaults to app_user.
//
// The Conn() middleware runs globally — every request gets a connection.
// If X-Organization-ID is present, the org is set on the connection. If
// absent, the connection is still app_user with no org (sees nothing).
//
// Admin routes explicitly upgrade to app_system. Scoped routes enforce
// the org header via RequireOrg(). A route registered outside either group
// would get a connection but see nothing — safe by default.
func SetupRouter(pool *pgxpool.Pool) *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	rlsMiddleware := NewRLSMiddleware(pool)
	r.Use(rlsMiddleware.Conn())

	h := NewHandler(pool)

	admin := r.Group("/admin")
	admin.Use(rlsMiddleware.Admin())
	{
		admin.POST("/organizations", h.CreateOrganization)
		admin.GET("/organizations", h.ListOrganizations)
	}

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
