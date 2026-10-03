-- Migration: 0006_auth_family
-- Purpose: Create tables for authentication, family management, and invitations
-- Per PRD 3.4.1 (auth), 14.5 #2 (family CRUD), 17.8 (module selection)

BEGIN;

-- Users table (simplified for P1-M1)
CREATE TABLE IF NOT EXISTS homeos_users (
    id UUID PRIMARY KEY,
    phone VARCHAR(20) UNIQUE NOT NULL,
    name VARCHAR(100),
    avatar VARCHAR(500),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Families table
CREATE TABLE IF NOT EXISTS homeos_families (
    id UUID PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    owner_id UUID NOT NULL REFERENCES homeos_users(id),
    timezone VARCHAR(50) NOT NULL DEFAULT 'Asia/Shanghai',
    currency VARCHAR(10) NOT NULL DEFAULT 'CNY',
    avatar VARCHAR(500),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Members table (links users to families with roles)
CREATE TABLE IF NOT EXISTS homeos_members (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL REFERENCES homeos_families(id),
    user_id UUID REFERENCES homeos_users(id), -- NULL for non-account members (children/elderly)
    role VARCHAR(20) NOT NULL CHECK (role IN ('owner', 'member', 'ward', 'guest')),
    relation VARCHAR(50), -- Family relationship (e.g., "父亲", "母亲", "孩子")
    name VARCHAR(100), -- For non-account members
    avatar VARCHAR(500),
    guardian_id UUID REFERENCES homeos_members(id), -- For wards, points to their guardian
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP,
    deleted_by UUID,
    UNIQUE(family_id, user_id)
);

-- Invitations table (three-state: pending/accepted/expired)
CREATE TABLE IF NOT EXISTS homeos_invitations (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL REFERENCES homeos_families(id),
    code VARCHAR(20) UNIQUE NOT NULL,
    role VARCHAR(20) NOT NULL CHECK (role IN ('owner', 'member', 'ward', 'guest')),
    inviter_id UUID NOT NULL REFERENCES homeos_members(id),
    invitee_phone VARCHAR(20),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'expired')),
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- SMS codes table (for verification)
CREATE TABLE IF NOT EXISTS homeos_sms_codes (
    id UUID PRIMARY KEY,
    phone VARCHAR(20) NOT NULL,
    code VARCHAR(10) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    used BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Refresh tokens table (for revocation)
CREATE TABLE IF NOT EXISTS homeos_refresh_tokens (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES homeos_users(id),
    token_hash VARCHAR(255) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_homeos_members_family ON homeos_members(family_id);
CREATE INDEX idx_homeos_members_user ON homeos_members(user_id);
CREATE INDEX idx_homeos_invitations_code ON homeos_invitations(code);
CREATE INDEX idx_homeos_invitations_status ON homeos_invitations(status);
CREATE INDEX idx_homeos_sms_codes_phone ON homeos_sms_codes(phone);
CREATE INDEX idx_homeos_refresh_tokens_user ON homeos_refresh_tokens(user_id);

COMMIT;
