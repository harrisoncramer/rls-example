-- Phase 1: Set up connection layer and column defaults.
-- After this migration, new inserts auto-populate organization_id
-- from the session variable without changing any queries.

-- Create the app_user role (subject to RLS)
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        CREATE ROLE app_user NOLOGIN;
    END IF;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;

-- Create a bypass role for system operations (migrations, cross-tenant jobs)
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_system') THEN
        CREATE ROLE app_system NOLOGIN BYPASSRLS;
    END IF;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_system;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_system;

-- Set column defaults to auto-populate from the session variable.
-- When a service sets `SET app.current_org = '<uuid>'` on checkout,
-- all inserts into these tables will get organization_id for free.
ALTER TABLE transfer
    ALTER COLUMN organization_id SET DEFAULT current_setting('app.current_org', true)::uuid;

ALTER TABLE ledger_entry
    ALTER COLUMN organization_id SET DEFAULT current_setting('app.current_org', true)::uuid;
