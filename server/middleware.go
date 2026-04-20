// Middleware for extracting and validating tenant context from HTTP headers.
//
// The middleware does NO database work — it only parses the X-Organization-ID
// header and stores the org UUID in the gin context. Transaction management
// is handled by the handlers themselves using rls.WithScopedTx / rls.WithAdminTx,
// which keeps the transaction lifecycle colocated with the queries that use it.
//
// This is the same pattern the River workers use: the caller decides when to
// start and commit a transaction, not the transport layer.
package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const orgCtxKey = "org_id"

// RequireOrg validates the X-Organization-ID header, parses it as a UUID,
// and stores it in the gin context. Rejects requests with a missing or
// invalid header.
func RequireOrg() gin.HandlerFunc {
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
		c.Set(orgCtxKey, orgID)
		c.Next()
	}
}

// OrgFromContext extracts the organization ID stored by RequireOrg().
func OrgFromContext(c *gin.Context) uuid.UUID {
	v, _ := c.Get(orgCtxKey)
	return v.(uuid.UUID)
}
