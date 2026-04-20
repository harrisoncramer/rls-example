// Handlers for the RLS demo server. Each handler creates a SQLC queries object
// backed by an auto-scoped DBTX, so the usage looks like standard SQLC:
//
//	queries := db.New(rls.Scoped(pool, orgID))
//	programs, err := queries.ListPrograms(ctx)
//
// Scoped handlers never reference organization_id. The org is set on the
// transaction via SET LOCAL app.current_org, and the column default
// current_setting('app.current_org')::uuid handles inserts.
//
// Admin handlers use rls.Admin(pool) which sets the app_system role
// (BYPASSRLS) so they see all data across all tenants.
package main

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/rls"
)

// Handler holds dependencies for all route handlers.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler creates a new handler with the given pool.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// scopedQueries returns SQLC queries scoped to the org from the request header.
func (h *Handler) scopedQueries(c context.Context) *db.Queries {
	return db.New(rls.Scoped(h.pool, OrgFromContext(c)))
}

// adminQueries returns SQLC queries with the admin role (BYPASSRLS).
func (h *Handler) adminQueries() *db.Queries {
	return db.New(rls.Admin(h.pool))
}

func (h *Handler) CreateOrganization(c *gin.Context) {
	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	org, err := h.adminQueries().CreateOrganization(c.Request.Context(), &db.CreateOrganizationParams{
		ID:   uuid.New(),
		Name: body.Name,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, org)
}

func (h *Handler) ListOrganizations(c *gin.Context) {
	orgs, err := h.adminQueries().ListOrganizations(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, orgs)
}

func (h *Handler) CreateProgram(c *gin.Context) {
	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	program, err := h.scopedQueries(c).CreateProgram(c.Request.Context(), body.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, program)
}

func (h *Handler) ListPrograms(c *gin.Context) {
	programs, err := h.scopedQueries(c).ListPrograms(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, programs)
}

func (h *Handler) CreateTransfer(c *gin.Context) {
	var body struct {
		ProgramID   uuid.UUID `json:"program_id" binding:"required"`
		Amount      int32     `json:"amount" binding:"required"`
		Description *string   `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	transfer, err := h.scopedQueries(c).CreateTransfer(c.Request.Context(), &db.CreateTransferParams{
		ProgramID:   body.ProgramID,
		Amount:      body.Amount,
		Description: body.Description,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, transfer)
}

func (h *Handler) ListTransfers(c *gin.Context) {
	transfers, err := h.scopedQueries(c).ListTransfers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, transfers)
}

func (h *Handler) CreateLedgerEntry(c *gin.Context) {
	var body struct {
		TransferID uuid.UUID `json:"transfer_id" binding:"required"`
		Amount     int32     `json:"amount" binding:"required"`
		EntryType  string    `json:"entry_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entry, err := h.scopedQueries(c).CreateLedgerEntry(c.Request.Context(), &db.CreateLedgerEntryParams{
		TransferID: body.TransferID,
		Amount:     body.Amount,
		EntryType:  body.EntryType,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, entry)
}

func (h *Handler) ListLedgerEntries(c *gin.Context) {
	entries, err := h.scopedQueries(c).ListLedgerEntries(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, entries)
}
