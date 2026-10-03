-- Migration: 0006_auth_family (down)
-- Purpose: Rollback authentication and family management tables

BEGIN;

DROP INDEX IF EXISTS idx_homeos_refresh_tokens_user;
DROP INDEX IF EXISTS idx_homeos_sms_codes_phone;
DROP INDEX IF EXISTS idx_homeos_invitations_status;
DROP INDEX IF EXISTS idx_homeos_invitations_code;
DROP INDEX IF EXISTS idx_homeos_members_user;
DROP INDEX IF EXISTS idx_homeos_members_family;

DROP TABLE IF EXISTS homeos_refresh_tokens;
DROP TABLE IF EXISTS homeos_sms_codes;
DROP TABLE IF EXISTS homeos_invitations;
DROP TABLE IF EXISTS homeos_members;
DROP TABLE IF EXISTS homeos_families;
DROP TABLE IF EXISTS homeos_users;

COMMIT;
