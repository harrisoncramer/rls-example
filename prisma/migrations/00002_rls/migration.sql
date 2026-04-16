-- Create an application role that is subject to RLS.
-- In production, each service would have its own role like this.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        CREATE ROLE app_user NOLOGIN;
    END IF;
END $$;

-- Grant table access to the app role
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;

-- Enable RLS on all tenant-scoped tables
ALTER TABLE organization ENABLE ROW LEVEL SECURITY;
ALTER TABLE account ENABLE ROW LEVEL SECURITY;
ALTER TABLE project ENABLE ROW LEVEL SECURITY;

-- Force RLS even for the table owner. Without this, the postgres superuser
-- (which owns the tables) would bypass all policies.
ALTER TABLE organization FORCE ROW LEVEL SECURITY;
ALTER TABLE account FORCE ROW LEVEL SECURITY;
ALTER TABLE project FORCE ROW LEVEL SECURITY;

-- Organization: only visible if id matches the session variable
CREATE POLICY tenant_isolation_select ON organization
    FOR SELECT USING (id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_insert ON organization
    FOR INSERT WITH CHECK (id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_update ON organization
    FOR UPDATE USING (id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_delete ON organization
    FOR DELETE USING (id = current_setting('app.current_org', true)::uuid);

-- Account: scoped by organization_id
CREATE POLICY tenant_isolation_select ON account
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_insert ON account
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_update ON account
    FOR UPDATE USING (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_delete ON account
    FOR DELETE USING (organization_id = current_setting('app.current_org', true)::uuid);

-- Project: scoped by organization_id
CREATE POLICY tenant_isolation_select ON project
    FOR SELECT USING (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_insert ON project
    FOR INSERT WITH CHECK (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_update ON project
    FOR UPDATE USING (organization_id = current_setting('app.current_org', true)::uuid);

CREATE POLICY tenant_isolation_delete ON project
    FOR DELETE USING (organization_id = current_setting('app.current_org', true)::uuid);

-- Create a bypass role for system operations (migrations, cross-tenant jobs, etc.)
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_system') THEN
        CREATE ROLE app_system NOLOGIN BYPASSRLS;
    END IF;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_system;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_system;
