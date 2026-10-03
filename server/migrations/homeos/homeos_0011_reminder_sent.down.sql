-- Migration: 0011_reminder_sent (down)
-- Purpose: Drop the deduplication table for the due-trigger scanner.

BEGIN;

DROP INDEX IF EXISTS homeos.homeos_reminder_sent_registration_idx;
DROP INDEX IF EXISTS homeos.homeos_reminder_sent_reg_fire_uidx;
DROP TABLE IF EXISTS homeos.homeos_reminder_sent;

COMMIT;
