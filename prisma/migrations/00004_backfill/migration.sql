-- Phase 2: Backfill existing rows from parent FK chains.
-- In production, do this in batches on large tables to avoid long locks.

-- Backfill transfer.organization_id from program
UPDATE transfer t
SET organization_id = p.organization_id
FROM program p
WHERE t.program_id = p.id
  AND t.organization_id IS NULL;

-- Backfill ledger_entry.organization_id from transfer
UPDATE ledger_entry le
SET organization_id = t.organization_id
FROM transfer t
WHERE le.transfer_id = t.id
  AND le.organization_id IS NULL;
