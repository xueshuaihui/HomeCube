-- finance 序列 0001：本域 schema 边界（§2.1）
-- §2.1「一个 postgres16 实例，当期两个 schema、两个角色」；
-- §2.1「建 schema 与建账号的动作只发生在 make up 与迁移容器里，不由服务进程在运行期自触发」。
-- 因此序列自己只创建 **自己那一个** schema；账号 hc_finance（§1.3 row 2 校验点「账号名 hc_{code}」）
-- 与 GRANT/ALTER DEFAULT PRIVILEGES/REVOKE 属迁移容器的 bootstrap，不进本序列：
-- §2.1 的 GRANT 块含 PASSWORD 字面量，凭据不入库（§2.1「避免账号密码进应用配置」）。
-- 他域 schema 由他域序列各自负责（§2.2「每服务一条独立序列」），本文件不引用 finance/homeos 之外的对象。
-- IF NOT EXISTS：本序列在已建过 schema 的库上重放必须无副作用（判据「重放幂等」）。

-- golang-migrate 版本表（显式创建，见技术方案 §2.2）
CREATE TABLE IF NOT EXISTS schema_migrations_finance (
    version bigint NOT NULL PRIMARY KEY,
    dirty boolean NOT NULL DEFAULT false
);

CREATE SCHEMA IF NOT EXISTS finance;
