package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SetupRouter creates the gin router with two route groups:
//
//   - Admin group: handlers use rls.WithAdminTx to bypass RLS for
//     cross-tenant operations like creating organizations.
//
//   - Scoped group: RequireOrg() validates X-Organization-ID and stores it
//     in the gin context. Handlers use rls.WithScopedTx to get a transaction
//     scoped to that tenant.
//
// Transaction management is in the handlers, not the middleware. The middleware
// only validates headers. This is the same pattern the River workers use.
func SetupRouter(pool *pgxpool.Pool) *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	h := NewHandler(pool)

	admin := r.Group("/admin")
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
