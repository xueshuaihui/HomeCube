-- Migration: 0008_due_registration (down)
-- 反向于 up：先删索引与注释、再删表，逐条 IF EXISTS，可整支重放。
-- 本支不动 0007 的 home/dynamics/message 读模型，也不动 homeos_proj_finance（0004）。

BEGIN;

DROP TABLE IF EXISTS homeos.homeos_due_registration;

COMMIT;
