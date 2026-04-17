CREATE TABLE organization (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ(6) NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ(6) NOT NULL DEFAULT now()
);

CREATE TABLE program (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organization(id),
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ(6) NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ(6) NOT NULL DEFAULT now()
);

-- transfer is indirectly scoped: transfer -> program -> organization
CREATE TABLE transfer (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    program_id UUID NOT NULL REFERENCES program(id),
    amount INTEGER NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ(6) NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ(6) NOT NULL DEFAULT now()
);

-- ledger_entry is two hops away: ledger_entry -> transfer -> program -> organization
CREATE TABLE ledger_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_id UUID NOT NULL REFERENCES transfer(id),
    amount INTEGER NOT NULL,
    entry_type VARCHAR(50) NOT NULL,
    created_at TIMESTAMPTZ(6) NOT NULL DEFAULT now()
);

CREATE INDEX idx_program_organization_id ON program(organization_id);
CREATE INDEX idx_transfer_program_id ON transfer(program_id);
CREATE INDEX idx_ledger_entry_transfer_id ON ledger_entry(transfer_id);
