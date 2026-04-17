package main

// These tests exercise the full HTTP stack — gin router, RLS middleware,
// handlers, SQLC queries — against a real Postgres database via Blueprint.
// Each test gets its own copy-on-write database with all migrations applied,
// so tests are fully isolated from each other.
//
// The middleware wraps each request in a transaction and uses SET LOCAL to
// set the role and org context. SET LOCAL is automatically discarded on
// commit/rollback, so there's no risk of leaking tenant context between
// requests. These tests verify that the same RLS guarantees hold when
// accessed through HTTP endpoints, not just through direct database calls.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/harrisoncramer/rls-example/db"
	"github.com/harrisoncramer/rls-example/internal/dbtest"
	"github.com/harrisoncramer/rls-example/internal/rls"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupTestServer creates a blueprint test database and returns an httptest
// server backed by the full gin router. The server is automatically cleaned
// up when the test ends.
func setupTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	tdb, err := dbtest.New(t)
	require.NoError(t, err)

	pool := tdb.GetTestPool(t).Pool
	router := SetupRouter(pool)
	srv := httptest.NewServer(router.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(url string, body any, headers map[string]string) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal body: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}

func getJSON(url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	return v
}

type orgResponse struct {
	ID   uuid.UUID `json:"ID"`
	Name string    `json:"Name"`
}

type programResponse struct {
	ID             uuid.UUID `json:"ID"`
	OrganizationID uuid.UUID `json:"OrganizationID"`
	Name           string    `json:"Name"`
}

type transferResponse struct {
	ID             uuid.UUID `json:"ID"`
	ProgramID      uuid.UUID `json:"ProgramID"`
	OrganizationID uuid.UUID `json:"OrganizationID"`
	Amount         int32     `json:"Amount"`
	Description    *string   `json:"Description"`
}

type ledgerEntryResponse struct {
	ID             uuid.UUID `json:"ID"`
	TransferID     uuid.UUID `json:"TransferID"`
	OrganizationID uuid.UUID `json:"OrganizationID"`
	Amount         int32     `json:"Amount"`
	EntryType      string    `json:"EntryType"`
}

// createOrg is a test helper that creates an organization via the admin endpoint.
func createOrg(t *testing.T, srv *httptest.Server, name string) orgResponse {
	t.Helper()
	resp, err := postJSON(srv.URL+"/admin/organizations", map[string]string{"name": name}, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return decodeJSON[orgResponse](t, resp)
}

// createProgram is a test helper that creates a program via the scoped endpoint.
func createProgram(t *testing.T, srv *httptest.Server, orgID uuid.UUID, name string) programResponse {
	t.Helper()
	resp, err := postJSON(
		srv.URL+"/programs",
		map[string]string{"name": name},
		map[string]string{"X-Organization-ID": orgID.String()},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return decodeJSON[programResponse](t, resp)
}

// createTransfer is a test helper that creates a transfer via the scoped endpoint.
// organization_id is auto-populated from the session variable.
func createTransfer(t *testing.T, srv *httptest.Server, orgID, programID uuid.UUID, amount int32, desc string) transferResponse {
	t.Helper()
	resp, err := postJSON(
		srv.URL+"/transfers",
		map[string]any{"program_id": programID.String(), "amount": amount, "description": desc},
		map[string]string{"X-Organization-ID": orgID.String()},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return decodeJSON[transferResponse](t, resp)
}

// TestServer_ScopedEndpointsIsolateData creates two orgs with programs and
// transfers, then verifies that listing through the scoped endpoints only
// returns data for the org specified in the X-Organization-ID header.
func TestServer_ScopedEndpointsIsolateData(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Acme Corp")
	org2 := createOrg(t, srv, "Widget Inc")

	createProgram(t, srv, org1.ID, "Grants Program")
	createProgram(t, srv, org2.ID, "Donations Program")
	createProgram(t, srv, org1.ID, "Second Program")

	// Org1 should see 2 programs
	resp, err := getJSON(srv.URL+"/programs", map[string]string{"X-Organization-ID": org1.ID.String()})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	programs := decodeJSON[[]programResponse](t, resp)
	assert.Len(t, programs, 2)

	// Org2 should see 1 program
	resp, err = getJSON(srv.URL+"/programs", map[string]string{"X-Organization-ID": org2.ID.String()})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	programs = decodeJSON[[]programResponse](t, resp)
	assert.Len(t, programs, 1)
}

// TestServer_TransferOrgIDAutoPopulated creates a transfer through the HTTP
// endpoint and verifies that organization_id was auto-populated from the
// session variable (set by middleware from the X-Organization-ID header),
// not passed explicitly in the request body.
func TestServer_TransferOrgIDAutoPopulated(t *testing.T) {
	srv := setupTestServer(t)

	org := createOrg(t, srv, "Acme Corp")
	prog := createProgram(t, srv, org.ID, "Grants Program")
	xfer := createTransfer(t, srv, org.ID, prog.ID, 5000, "Grant payment")

	assert.Equal(t, org.ID, xfer.OrganizationID,
		"transfer.organization_id should be auto-populated from X-Organization-ID header")
	assert.Equal(t, int32(5000), xfer.Amount)
}

// TestServer_LedgerEntryOrgIDAutoPopulated creates a ledger entry through
// the HTTP endpoint and verifies that organization_id was auto-populated.
// Ledger entries are two hops away from organization in the original schema
// (ledger_entry -> transfer -> program -> organization), but the denormalized
// column + session variable default means the HTTP layer doesn't need to
// know about that chain.
func TestServer_LedgerEntryOrgIDAutoPopulated(t *testing.T) {
	srv := setupTestServer(t)

	org := createOrg(t, srv, "Acme Corp")
	prog := createProgram(t, srv, org.ID, "Grants Program")
	xfer := createTransfer(t, srv, org.ID, prog.ID, 5000, "Grant payment")

	resp, err := postJSON(
		srv.URL+"/ledger-entries",
		map[string]any{"transfer_id": xfer.ID.String(), "amount": 5000, "entry_type": "debit"},
		map[string]string{"X-Organization-ID": org.ID.String()},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	entry := decodeJSON[ledgerEntryResponse](t, resp)

	assert.Equal(t, org.ID, entry.OrganizationID,
		"ledger_entry.organization_id should be auto-populated from X-Organization-ID header")
}

// TestServer_CrossTenantIsolation creates data for two orgs and verifies
// that transfers and ledger entries are fully isolated between them.
func TestServer_CrossTenantIsolation(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Acme Corp")
	org2 := createOrg(t, srv, "Widget Inc")

	prog1 := createProgram(t, srv, org1.ID, "Grants")
	prog2 := createProgram(t, srv, org2.ID, "Donations")

	createTransfer(t, srv, org1.ID, prog1.ID, 5000, "Org1 transfer")
	createTransfer(t, srv, org2.ID, prog2.ID, 3000, "Org2 transfer")

	// Org1 sees only its transfer
	resp, err := getJSON(srv.URL+"/transfers", map[string]string{"X-Organization-ID": org1.ID.String()})
	require.NoError(t, err)
	transfers := decodeJSON[[]transferResponse](t, resp)
	assert.Len(t, transfers, 1)
	assert.Equal(t, int32(5000), transfers[0].Amount)

	// Org2 sees only its transfer
	resp, err = getJSON(srv.URL+"/transfers", map[string]string{"X-Organization-ID": org2.ID.String()})
	require.NoError(t, err)
	transfers = decodeJSON[[]transferResponse](t, resp)
	assert.Len(t, transfers, 1)
	assert.Equal(t, int32(3000), transfers[0].Amount)

	// Org1 sees no ledger entries (none created for org1)
	resp, err = getJSON(srv.URL+"/ledger-entries", map[string]string{"X-Organization-ID": org1.ID.String()})
	require.NoError(t, err)
	entries := decodeJSON[[]ledgerEntryResponse](t, resp)
	assert.Empty(t, entries)
}

// TestServer_AdminSeesAllOrganizations verifies that the admin endpoint
// returns data across all tenants without requiring an org header.
func TestServer_AdminSeesAllOrganizations(t *testing.T) {
	srv := setupTestServer(t)

	createOrg(t, srv, "Org One")
	createOrg(t, srv, "Org Two")
	createOrg(t, srv, "Org Three")

	resp, err := getJSON(srv.URL+"/admin/organizations", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	orgs := decodeJSON[[]orgResponse](t, resp)
	assert.Len(t, orgs, 3)
}

// TestServer_MissingOrgHeaderReturns400 verifies that scoped endpoints
// reject requests without the X-Organization-ID header.
func TestServer_MissingOrgHeaderReturns400(t *testing.T) {
	srv := setupTestServer(t)

	resp, err := getJSON(srv.URL+"/transfers", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestServer_InvalidOrgHeaderReturns400 verifies that scoped endpoints
// reject requests with a non-UUID X-Organization-ID header.
func TestServer_InvalidOrgHeaderReturns400(t *testing.T) {
	srv := setupTestServer(t)

	resp, err := getJSON(srv.URL+"/transfers", map[string]string{"X-Organization-ID": "not-a-uuid"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestServer_HardenedPoolDefaultDeny verifies the safety net: when a pool
// is configured with rls.ConfigurePool, every connection defaults to app_user.
// A handler registered WITHOUT any middleware still runs as app_user with no
// org set, which means RLS denies all access. Data leaks become "see nothing"
// bugs instead of "see everything" bugs.
//
// This test creates a separate gin router with a "naked" endpoint that has no
// middleware, backed by a hardened pool. Even though data exists, the naked
// endpoint returns empty results.
func TestServer_HardenedPoolDefaultDeny(t *testing.T) {
	tdb, err := dbtest.New(t)
	require.NoError(t, err)

	// Create a hardened pool with rls.ConfigurePool applied
	blueprintPool := tdb.GetTestPool(t).Pool
	config, err := pgxpool.ParseConfig(blueprintPool.Config().ConnString())
	require.NoError(t, err)
	rls.ConfigurePool(config)

	hardenedPool, err := pgxpool.NewWithConfig(context.Background(), config)
	require.NoError(t, err)
	t.Cleanup(hardenedPool.Close)

	// Seed some data using the blueprint pool (postgres superuser)
	_, seedErr := blueprintPool.Exec(context.Background(), "SET ROLE app_system")
	require.NoError(t, seedErr)
	_, seedErr = blueprintPool.Exec(context.Background(),
		"INSERT INTO organization (id, name) VALUES (gen_random_uuid(), 'Seed Org')")
	require.NoError(t, seedErr)
	_, seedErr = blueprintPool.Exec(context.Background(), "RESET ROLE")
	require.NoError(t, seedErr)

	// Create a router with a "naked" endpoint — no middleware at all.
	// In production, this simulates a developer forgetting to add
	// the scoped or admin middleware to a new route.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/naked/organizations", func(c *gin.Context) {
		conn, acquireErr := hardenedPool.Acquire(c.Request.Context())
		if acquireErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": acquireErr.Error()})
			return
		}
		defer conn.Release()

		queries := db.New(conn)
		orgs, listErr := queries.ListOrganizations(c.Request.Context())
		if listErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": listErr.Error()})
			return
		}
		c.JSON(http.StatusOK, orgs)
	})

	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	// The naked endpoint should return empty — the hardened pool defaults
	// to app_user, and with no org set, RLS denies everything.
	resp, err := getJSON(srv.URL+"/naked/organizations", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	orgs := decodeJSON[[]orgResponse](t, resp)
	assert.Empty(t, orgs, "hardened pool with no middleware should return no data (default deny)")
}
