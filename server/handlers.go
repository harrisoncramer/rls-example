package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/harrisoncramer/rls-example/db"
)

// Handler holds dependencies for all route handlers. In Chariot, each domain
// (disbursements, transfers, etc.) has its own handler struct with its own
// service dependencies. For this demo, one struct covers all endpoints.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler creates a new handler with the given pool.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

func (h *Handler) CreateOrganization(c *gin.Context) {
	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	conn := ConnFromContext(c)
	queries := db.New(conn)

	org, err := queries.CreateOrganization(c.Request.Context(), &db.CreateOrganizationParams{
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
	conn := ConnFromContext(c)
	queries := db.New(conn)

	orgs, err := queries.ListOrganizations(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, orgs)
}

func (h *Handler) CreateProgram(c *gin.Context) {
	orgID, _ := uuid.Parse(c.GetHeader("X-Organization-ID"))

	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	conn := ConnFromContext(c)
	queries := db.New(conn)

	program, err := queries.CreateProgram(c.Request.Context(), &db.CreateProgramParams{
		OrganizationID: orgID,
		Name:           body.Name,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, program)
}

func (h *Handler) ListPrograms(c *gin.Context) {
	conn := ConnFromContext(c)
	queries := db.New(conn)

	programs, err := queries.ListPrograms(c.Request.Context())
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

	conn := ConnFromContext(c)
	queries := db.New(conn)

	transfer, err := queries.CreateTransfer(c.Request.Context(), &db.CreateTransferParams{
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
	conn := ConnFromContext(c)
	queries := db.New(conn)

	transfers, err := queries.ListTransfers(c.Request.Context())
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

	conn := ConnFromContext(c)
	queries := db.New(conn)

	entry, err := queries.CreateLedgerEntry(c.Request.Context(), &db.CreateLedgerEntryParams{
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
	conn := ConnFromContext(c)
	queries := db.New(conn)

	entries, err := queries.ListLedgerEntries(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, entries)
}
