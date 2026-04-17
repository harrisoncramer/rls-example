-- Phase 4: Enable RLS on all tenant-scoped tables.

ALTER TABLE organization ENABLE ROW LEVEL SECURITY;
ALTER TABLE program ENABLE ROW LEVEL SECURITY;
ALTER TABLE transfer ENABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_entry ENABLE ROW LEVEL SECURITY;

-- Force RLS even for the table owner (postgres superuser)
ALTER TABLE organization FORCE ROW LEVEL SECURITY;
ALTER TABLE program FORCE ROW LEVEL SECURITY;
ALTER TABLE transfer FORCE ROW LEVEL SECURITY;
ALTER TABLE ledger_entry FORCE ROW LEVEL SECURITY;

-- Organization: id matches session variable
CREATE POLICY tenant_isolation_select ON organization
    FOR SELECT USING (id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_insert ON organization
    FOR INSERT WITH CHECK (id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_update ON organization
    FOR UPDATE USING (id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_delete ON organization
    FOR DELETE USING (id = current_setting('app.current_org', true)::uuid);

-- Program: scoped by organization_id
CREATE POLICY tenant_isolation_select ON program
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_insert ON program
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_update ON program
    FOR UPDATE USING (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_delete ON program
    FOR DELETE USING (organization_id = current_setting('app.current_org', true)::uuid);

-- Transfer: scoped by denormalized organization_id
CREATE POLICY tenant_isolation_select ON transfer
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_insert ON transfer
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_update ON transfer
    FOR UPDATE USING (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_delete ON transfer
    FOR DELETE USING (organization_id = current_setting('app.current_org', true)::uuid);

-- Ledger entry: scoped by denormalized organization_id
CREATE POLICY tenant_isolation_select ON ledger_entry
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_insert ON ledger_entry
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_update ON ledger_entry
    FOR UPDATE USING (organization_id = current_setting('app.current_org', true)::uuid);
CREATE POLICY tenant_isolation_delete ON ledger_entry
    FOR DELETE USING (organization_id = current_setting('app.current_org', true)::uuid);
