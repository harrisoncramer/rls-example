package main

// Adversarial HTTP tests that try to break RLS through the server layer:
// concurrent requests, org header manipulation, rapid switching, and
// attempting to access data across tenants via the API.

import (
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerAdversarial_ConcurrentRequestsDifferentOrgs fires concurrent
// requests for different orgs at the same server and verifies that each
// request only sees its own org's data. This tests that the transaction-based
// middleware properly isolates concurrent requests on the shared pool.
func TestServerAdversarial_ConcurrentRequestsDifferentOrgs(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Concurrent Org 1")
	org2 := createOrg(t, srv, "Concurrent Org 2")

	prog1 := createProgram(t, srv, org1.ID, "Program 1")
	prog2 := createProgram(t, srv, org2.ID, "Program 2")

	createTransfer(t, srv, org1.ID, prog1.ID, 1000, "Org1 transfer")
	createTransfer(t, srv, org2.ID, prog2.ID, 2000, "Org2 transfer")

	const concurrency = 20
	var wg sync.WaitGroup
	errors := make(chan string, concurrency*2)

	for range concurrency {
		wg.Add(2)

		go func() {
			defer wg.Done()
			resp, err := getJSON(srv.URL+"/transfers", map[string]string{"X-Organization-ID": org1.ID.String()})
			if err != nil {
				errors <- err.Error()
				return
			}
			transfers := decodeJSON[[]transferResponse](t, resp)
			if len(transfers) != 1 {
				errors <- "org1 saw wrong number of transfers"
				return
			}
			if transfers[0].Amount != 1000 {
				errors <- "org1 saw wrong transfer amount"
			}
		}()

		go func() {
			defer wg.Done()
			resp, err := getJSON(srv.URL+"/transfers", map[string]string{"X-Organization-ID": org2.ID.String()})
			if err != nil {
				errors <- err.Error()
				return
			}
			transfers := decodeJSON[[]transferResponse](t, resp)
			if len(transfers) != 1 {
				errors <- "org2 saw wrong number of transfers"
				return
			}
			if transfers[0].Amount != 2000 {
				errors <- "org2 saw wrong transfer amount"
			}
		}()
	}

	wg.Wait()
	close(errors)

	for errMsg := range errors {
		t.Errorf("concurrent request error: %s", errMsg)
	}
}

// TestServerAdversarial_RapidOrgSwitching sends sequential requests rapidly
// alternating between two orgs. Verifies no stale state between requests.
func TestServerAdversarial_RapidOrgSwitching(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Rapid Org 1")
	org2 := createOrg(t, srv, "Rapid Org 2")

	createProgram(t, srv, org1.ID, "Org1 Program")
	createProgram(t, srv, org2.ID, "Org2 Program")

	for i := range 50 {
		var orgID uuid.UUID
		var expectedCount int
		if i%2 == 0 {
			orgID = org1.ID
			expectedCount = 1
		} else {
			orgID = org2.ID
			expectedCount = 1
		}

		resp, err := getJSON(srv.URL+"/programs", map[string]string{"X-Organization-ID": orgID.String()})
		require.NoError(t, err)
		programs := decodeJSON[[]programResponse](t, resp)
		assert.Len(t, programs, expectedCount, "iteration %d", i)
	}
}

// TestServerAdversarial_FakeOrgIDReturnsEmpty verifies that using a
// fabricated org ID (one that doesn't exist in the database) returns
// empty results rather than an error.
func TestServerAdversarial_FakeOrgIDReturnsEmpty(t *testing.T) {
	srv := setupTestServer(t)

	createOrg(t, srv, "Real Org")

	fakeOrgID := uuid.New()
	resp, err := getJSON(srv.URL+"/programs", map[string]string{"X-Organization-ID": fakeOrgID.String()})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	programs := decodeJSON[[]programResponse](t, resp)
	assert.Empty(t, programs, "fabricated org ID should return empty results")
}

// TestServerAdversarial_AdminCannotLeakToScoped verifies that after an admin
// request, a subsequent scoped request doesn't inherit the admin context.
// This tests that SET LOCAL properly scopes the role to the transaction.
func TestServerAdversarial_AdminCannotLeakToScoped(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Leak Test Org 1")
	org2 := createOrg(t, srv, "Leak Test Org 2")

	createProgram(t, srv, org1.ID, "Org1 Program")
	createProgram(t, srv, org2.ID, "Org2 Program")

	// Admin request sees all 2 orgs
	resp, err := getJSON(srv.URL+"/admin/organizations", nil)
	require.NoError(t, err)
	orgs := decodeJSON[[]orgResponse](t, resp)
	assert.Len(t, orgs, 2)

	// Immediately after, scoped request should only see org1's data
	resp, err = getJSON(srv.URL+"/programs", map[string]string{"X-Organization-ID": org1.ID.String()})
	require.NoError(t, err)
	programs := decodeJSON[[]programResponse](t, resp)
	assert.Len(t, programs, 1, "scoped request should not inherit admin context")
	assert.Equal(t, org1.ID, programs[0].OrganizationID)
}

// TestServerAdversarial_ScopedCannotLeakToAdmin verifies that after a scoped
// request with org1, an admin request doesn't inherit the org1 filter.
func TestServerAdversarial_ScopedCannotLeakToAdmin(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Leak Test Org A")
	createOrg(t, srv, "Leak Test Org B")

	// Scoped request for org1
	resp, err := getJSON(srv.URL+"/programs", map[string]string{"X-Organization-ID": org1.ID.String()})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Admin request immediately after should see both orgs
	resp, err = getJSON(srv.URL+"/admin/organizations", nil)
	require.NoError(t, err)
	orgs := decodeJSON[[]orgResponse](t, resp)
	assert.Len(t, orgs, 2, "admin request should not inherit scoped org filter")
}

// TestServerAdversarial_CreateAndImmediatelyReadIsolated creates data for
// org1 then immediately tries to read it as org2. Verifies there's no
// timing window where data is visible to the wrong tenant.
func TestServerAdversarial_CreateAndImmediatelyReadIsolated(t *testing.T) {
	srv := setupTestServer(t)

	org1 := createOrg(t, srv, "Creator Org")
	org2 := createOrg(t, srv, "Reader Org")

	prog := createProgram(t, srv, org1.ID, "Secret Program")
	createTransfer(t, srv, org1.ID, prog.ID, 99999, "Secret Transfer")

	// Immediately try to read as org2
	resp, err := getJSON(srv.URL+"/transfers", map[string]string{"X-Organization-ID": org2.ID.String()})
	require.NoError(t, err)
	transfers := decodeJSON[[]transferResponse](t, resp)
	assert.Empty(t, transfers, "org2 should never see org1's data, even immediately after creation")
}

// TestServerAdversarial_HealthzWithoutOrgHeader verifies that the healthz
// endpoint (registered before the Conn middleware) works without any
// org header.
func TestServerAdversarial_HealthzWithoutOrgHeader(t *testing.T) {
	srv := setupTestServer(t)

	resp, err := getJSON(srv.URL+"/healthz", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
