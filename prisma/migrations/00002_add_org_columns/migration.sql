-- Phase 0: Add nullable organization_id to indirectly-scoped tables.
-- This is a metadata-only operation in Postgres (no table rewrite).
ALTER TABLE transfer ADD COLUMN organization_id UUID REFERENCES organization(id);
ALTER TABLE ledger_entry ADD COLUMN organization_id UUID REFERENCES organization(id);
