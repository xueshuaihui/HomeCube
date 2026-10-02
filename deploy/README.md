# HomeCube Deploy - 全栈联调载体

本目录包含HomeCube项目的完整容器化部署配置，支持本地全栈联调。

## 架构概览

### 常驻容器（7个）
1. **postgres16** - PostgreSQL 16数据库集群
2. **nats** - NATS JetStream消息总线
3. **svc-homeos** - HomeOS底座服务
4. **svc-finance** - 财务面服务
5. **nginx** - 反向代理 + 静态资源
6. **web** - H5前端静态容器
7. **admin** - 管理后台静态容器

### 一次性容器（1个）
- **migrate** - 数据库迁移容器（执行完成后退出）

## 快速开始

### 1. 前置条件

```bash
# 确保已安装
- Docker Desktop
- Node.js (用于生成未出生面前缀)
- Go 1.27.x (仅开发模式需要)

# 准备环境配置文件
cp deploy/env.local.example deploy/env.local
# 编辑 deploy/env.local，将所有 CHANGE_ME 替换为实际值
```

### 2. 构建前端产物

```bash
# Web H5
cd web && npm run build:h5

# Admin (如需要)
cd admin && npm run build
```

### 3. 启动全栈

```bash
make up
```

这将按顺序启动：
1. postgres16 + nats (基础设施)
2. migrate (执行数据库迁移)
3. svc-homeos + svc-finance (业务服务)
4. web + admin (静态容器)
5. nginx (反向代理，依赖所有上游健康)

### 4. 验证部署

```bash
# 检查所有容器状态
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml ps

# 测试健康探针
curl http://127.0.0.1/api/homeos/healthz
curl http://127.0.0.1/api/finance/healthz

# 浏览器访问
open http://127.0.0.1
```

### 5. 运行健康检查测试

```bash
./deploy/test-healthz.sh
```

该脚本测试：
- `/api/homeos/healthz` 和 `/api/finance/healthz` 返回200
- 五个未出生前缀返回"即将上线" (404)
- 停止NATS后健康探针变为503，恢复后回到200

## Schema与账号Provisioning

在postgres16首次初始化时，`deploy/init-schema.sql`自动执行：

```sql
-- 创建两个schema
CREATE SCHEMA homeos;
CREATE SCHEMA finance;

-- 创建两个专属角色
CREATE ROLE hc_homeos LOGIN PASSWORD 'CHANGE_ME';
CREATE ROLE hc_finance LOGIN PASSWORD 'CHANGE_ME';

-- 授予权限
GRANT USAGE ON SCHEMA homeos TO hc_homeos;
GRANT USAGE ON SCHEMA finance TO hc_finance;

-- 设置默认权限
ALTER DEFAULT PRIVILEGES IN SCHEMA homeos GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO hc_homeos;
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO hc_finance;

-- 隔离public schema
REVOKE ALL ON SCHEMA public FROM hc_homeos;
REVOKE ALL ON SCHEMA public FROM hc_finance;
```

## 数据库迁移

迁移由`migrate`容器自动执行，使用golang-migrate CLI：

```bash
# 手动执行迁移
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml up migrate

# 查看迁移状态
docker compose exec postgres16 psql -U postgres -d homecube -c \
  "SELECT * FROM schema_migrations_homeos ORDER BY version;"
docker compose exec postgres16 psql -U postgres -d homecube -c \
  "SELECT * FROM schema_migrations_finance ORDER BY version;"
```

迁移文件位置：
- `server/migrations/homeos/` - homeos序列
- `server/migrations/finance/` - finance序列

## 服务端口映射

| 服务 | 容器内端口 | 宿主端口 | 说明 |
|------|-----------|---------|------|
| nginx | 80 | 80 (NGINX_HTTP_PORT) | HTTP入口 |
| postgres16 | 5432 | 127.0.0.1:5432 | 数据库 |
| nats | 4222 | 127.0.0.1:4222 | 消息总线 |
| nats (monitoring) | 8222 | - | 内部监控 |
| svc-homeos | 8080 | - | 仅容器内 |
| svc-finance | 8081 | - | 仅容器内 |
| web | 80 | - | 仅容器内 |
| admin | 80 | - | 仅容器内 |

## 开发模式

要在容器外运行服务进行调试：

```bash
# 只起基础设施
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml up -d postgres16 nats

# 在另一个终端运行homeos
make dev-homeos

# 在另一个终端运行finance
make dev-finance
```

## 停止与清理

```bash
# 停止所有容器
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml stop

# 完全清理（包括数据卷）
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml down -v
```

## 故障排查

### 容器无法启动

```bash
# 查看日志
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml logs <service-name>

# 检查环境变量
cat deploy/env.local
```

### 迁移失败

```bash
# 查看迁移日志
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml logs migrate

# 手动执行迁移脚本
./deploy/migrate.sh up
```

### 健康检查失败

```bash
# 测试直接访问服务
curl http://127.0.0.1:8080/api/homeos/healthz
curl http://127.0.0.1:8081/api/finance/healthz

# 检查NATS连接
docker compose exec nats wget -q --spider http://127.0.0.1:8222/healthz
```

## 文件清单

```
deploy/
├── docker-compose.yml      # 主编排文件
├── nginx.conf              # Nginx反向代理配置
├── nginx-web.conf          # Web静态容器Nginx配置
├── nginx-admin.conf        # Admin静态容器Nginx配置
├── init-schema.sql         # Schema与账号初始化脚本
├── migrate.sh              # 迁移执行脚本
├── test-healthz.sh         # 健康检查测试脚本
├── env.local.example       # 环境变量模板
├── env.local               # 本地环境变量（被gitignore）
└── README.md               # 本文档
```

## 参考文档

- [技术方案](../docs/p1-tech-plan.md) §1.1, §2.1, §10.1, §十三
- [PRD](../docs/prd-homecube.md) 22.1, 22.2, 22.4
