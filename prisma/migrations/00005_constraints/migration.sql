-- Phase 3: Now that all rows are backfilled, add NOT NULL constraints and indexes.
ALTER TABLE transfer ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE ledger_entry ALTER COLUMN organization_id SET NOT NULL;

CREATE INDEX idx_transfer_organization_id ON transfer(organization_id);
CREATE INDEX idx_ledger_entry_organization_id ON ledger_entry(organization_id);
