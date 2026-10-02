-- =============================================================================
-- HomeCube · Schema & Role Provisioning Script
--
-- 出处：tech plan §2.1（单集群、每服务一 schema 一账号；建 schema/建账号只发生在
-- make up 与迁移容器里）；PRD 22.2 第 2 条（账号即边界）。
--
-- 本脚本由 postgres:16-alpine 镜像的 docker-entrypoint-initdb.d 机制自动执行：
-- 容器首次初始化时（pgdata 卷为空），所有 /docker-entrypoint-initdb.d/*.sql 文件按
-- 字母顺序执行。后续启动若卷已有数据则跳过。
--
-- 用法（手动执行）：
--   docker compose exec postgres16 psql -U postgres -d homecube -f /docker-entrypoint-initdb.d/init-schema.sql
--
-- 交付内容：
--   1. 创建 homeos 和 finance 两个 schema
--   2. 创建 hc_homeos 和 hc_finance 两个专属角色
--   3. 授予 schema USAGE 权限
--   4. 设置默认权限（ALTER DEFAULT PRIVILEGES）
--   5. 撤销 public schema 的访问权限
-- =============================================================================

-- ---- homeos schema 与账号 ---------------------------------------------------

-- 创建 homeos schema（若不存在）
CREATE SCHEMA IF NOT EXISTS homeos;

-- 创建 hc_homeos 角色（若不存在）
-- 口令 CHANGE_ME 是占位符，真实值由 deploy/env.local 的 POSTGRES_PASSWORD 控制
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'hc_homeos') THEN
        CREATE ROLE hc_homeos LOGIN PASSWORD 'CHANGE_ME';
    END IF;
END
$$;

-- 授予 homeos schema 的 USAGE 权限
GRANT USAGE ON SCHEMA homeos TO hc_homeos;

-- 设置默认权限：在 homeos schema 中创建的表，hc_homeos 自动拥有 CRUD 权限
ALTER DEFAULT PRIVILEGES IN SCHEMA homeos GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hc_homeos;

-- 同样为序列设置默认权限（如果将来使用 serial/bigserial）
ALTER DEFAULT PRIVILEGES IN SCHEMA homeos GRANT USAGE, SELECT ON SEQUENCES TO hc_homeos;

-- 撤销 hc_homeos 对 public schema 的所有权限（隔离边界）
REVOKE ALL ON SCHEMA public FROM hc_homeos;


-- ---- finance schema 与账号 --------------------------------------------------

-- 创建 finance schema（若不存在）
CREATE SCHEMA IF NOT EXISTS finance;

-- 创建 hc_finance 角色（若不存在）
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'hc_finance') THEN
        CREATE ROLE hc_finance LOGIN PASSWORD 'CHANGE_ME';
    END IF;
END
$$;

-- 授予 finance schema 的 USAGE 权限
GRANT USAGE ON SCHEMA finance TO hc_finance;

-- 设置默认权限：在 finance schema 中创建的表，hc_finance 自动拥有 CRUD 权限
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hc_finance;

-- 同样为序列设置默认权限
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT USAGE, SELECT ON SEQUENCES TO hc_finance;

-- 撤销 hc_finance 对 public schema 的所有权限（隔离边界）
REVOKE ALL ON SCHEMA public FROM hc_finance;


-- ---- 验证输出 ---------------------------------------------------------------

-- 显示已创建的 schema
\echo 'Created schemas:'
SELECT schema_name FROM information_schema.schemata WHERE schema_name IN ('homeos', 'finance') ORDER BY schema_name;

-- 显示已创建的角色
\echo 'Created roles:'
SELECT rolname FROM pg_catalog.pg_roles WHERE rolname IN ('hc_homeos', 'hc_finance') ORDER BY rolname;

-- 显示 schema 所有权
\echo 'Schema ownership:'
SELECT nspname, pg_catalog.pg_get_userbyid(nspowner) AS owner
FROM pg_catalog.pg_namespace
WHERE nspname IN ('homeos', 'finance')
ORDER BY nspname;

\echo 'Schema & role provisioning completed successfully!'
