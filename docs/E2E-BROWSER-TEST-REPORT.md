# HomeCube 真实浏览器全功能测试报告

**报告日期**：2026-10-04
**测试方式**：真实 Chromium 浏览器（agent-browser 0.27）+ 真实 PostgreSQL 16 + 真实 NATS JetStream + 真实 Go 服务进程
**测试对象**：H5 客户端全量页面（homeos 12 页 + finance 13 页 = 25 页）与两服务全部已注册 API
**结论**：发现 **11 个真实缺陷**，其中 4 个 P0（功能完全阻断）、7 个 P1/P2。已全部定位根因并修复 10 个，1 个为服务端功能缺口（已明确定界）。

---

## 一、测试环境与可信度声明

| 组件 | 版本 / 形态 | 启动方式 | 可信度依据 |
|---|---|---|---|
| svc-homeos | linux/amd64 由本仓库源码交叉编译 | Docker 容器常驻（`migrate/migrate` 基础镜像 + 静态二进制） | `/healthz` 返回 `schema:homeos up` + `jetstream up`；容器日志有真实 `http_request` 记录 |
| svc-finance | linux/amd64 由本仓库源码交叉编译 | 同上 | `/healthz` 返回 `schema:finance up` + `jetstream up` |
| PostgreSQL | 16-alpine 容器 | compose 常驻 | migration homeos@11 / finance@14 |
| NATS | 2.10-alpine，JetStream 已开 | compose 常驻 | 两个服务启动日志均报 `event_side_started` / `eventbus_ready` |
| H5 前端 | uni-app Vue3，Vite dev server :5173 | 宿主进程 | 页面经 Vite 代理真实打到 8080/8081 |
| 浏览器 | 真实 Chromium（非 headless 模拟） | agent-browser | 坐标级真实鼠标事件（mouse move/down/up） |

**关键说明**：本轮所有"通过"结论都经过**浏览器 UI 交互 + 数据库落库双向验证**，不接受仅页面渲染无异常的判定。每个写入类用例都回查了 `finance.*` 表。

### 环境限制与绕行方式

| 限制 | 影响 | 绕行 |
|---|---|---|
| Docker Hub 拉取超时（`golang:1.27-alpine`、`alpine:3.20` 均 context deadline exceeded） | `make up` 无法构建业务镜像 | 交叉编译 linux 二进制，挂载进本地已有镜像 `migrate/migrate:v4.20.1` 运行；前端改用 Vite dev server + 既有 proxy |
| 宿主后台进程被会话回收 | 反复出现"服务凭空 502" | 改用 Docker 容器常驻，彻底解决 |
| 环境的 safe-delete 钩子 | `npm run build:h5` 在清理 `dist/build/h5/assets`（58 文件）时被拦截 | 改用 `tsc --noEmit` + dev server 实际编译验证；H5 正式构建需人工确认后执行 |

---

## 二、缺陷汇总（按严重度）

### P0 — 功能完全阻断

| # | 缺陷 | 根因 | 位置 | 状态 |
|---|---|---|---|---|
| 1 | **全栈无法启动**：finance 迁移 14 执行失败 | 表级 `CONSTRAINT ... UNIQUE (...) WHERE deleted_at IS NULL` —— PostgreSQL **不允许**表约束带 `WHERE`，部分唯一索引必须用 `CREATE UNIQUE INDEX` | `server/migrations/finance/finance_0014_settings.up.sql:26` | ✅ 已修 |
| 2 | **每一次记账都 HTTP 500** | 幂等键 `${Date.now()}-${random}` 不是 UUID，而 `client_request_id` 列类型是 `uuid` → `invalid input syntax for type uuid` (SQLSTATE 22P02) | `web/src/pages/finance/transaction/create.vue`、`web/src/pages/homeos/quick-add/index.vue` | ✅ 已修（提为共享函数 `newIdempotencyUUID()`） |
| 3 | **快速添加每次都 500** | 同 #2，第二处未同步修 | 同上 | ✅ 已修 |
| 4 | **家庭成员页永久 404** | 前端请求 `GET /members`，服务端只注册了 `GET /members/snapshot`（契约 homeos.yaml:196 也是后者） | `web/src/pages/homeos/family/index.vue:133` | ✅ 已修（并做 `member_id → id` 字段归一） |

### P1 — 功能错误但未完全阻断

| # | 缺陷 | 根因 | 位置 | 状态 |
|---|---|---|---|---|
| 5 | **统计报表页永久 404** | 前端请求 `/statistics/summary`，服务端只有 `/overview`、`/trend`、`/category`、`/member` | `web/src/pages/finance/report/index.vue` | ✅ 已修 |
| 6 | **`/statistics/category` 每次调用 500** | SQL 用了 `ft.` 前缀，但 `.Table("finance_transaction")` 没起别名 → `missing FROM-clause entry for table "ft"` (42P01) | `server/services/svc-finance/internal/service/statistics.go` GetCategoryStats | ✅ 已修 |
| 7 | **`/statistics/member` 每次调用 500** | SQL 引用 `ft.created_by`，但 `finance_transaction` **没有该列**（17 列里只有 `deleted_by`）；`finance_proj_homeos` 也无 `id`/`name` | 同上 GetMemberStats | ⚠️ 改为返回空集合（见下"功能缺口"） |
| 8 | **创建预算永远 400** | 两处：① `period` 编码不匹配（前端 `month/quarter/year` vs 后端 `oneof=monthly/quarterly/yearly`）；② 缺必填 `start_date`/`end_date` | `web/src/pages/finance/budget/index.vue` | ✅ 已修 |
| 9 | **预算列表恒为空** | 列表查询同样用错 `period` 编码（实测 `period=month` → `items:[]`，`period=monthly` → 命中） | 同上 | ✅ 已修 |
| 10 | **分账页永久 404** | 前端 `/api/finance/splits`，服务端注册的是 `/split-settlements` | `web/src/pages/finance/split/index.vue` | ✅ 已修 |
| 11 | **报表金额显示放大 100 倍** | 服务端 `GetOverviewStats` 聚合的是 `SUM(amount_cents)`（单位：分），字段名虽无 `_cents` 后缀但值是分；前端误判为"元"又乘了 100 | `web/src/pages/finance/report/index.vue` | ✅ 已修 |

---

## 三、已修复缺陷的验证证据

每项修复都在真实浏览器里复测过，并回查数据库。

| 缺陷 | 修复前实测 | 修复后实测 |
|---|---|---|
| #1 迁移 | `syntax error at or near "WHERE" (column 65)`，`make up` 退出码 1 | 迁移成功，`finance.finance_settings` 表 + `uk_finance_settings_family_id` 部分唯一索引均已建立 |
| #2/#3 幂等键 | `POST /transactions → 500`，`invalid input syntax for type uuid: "1791049719700-jybfx232uc9"` | `POST → 201`，落库 `client_request_id = 8b18769a-8a34-48a4-8257-03d450f14d32`（合法 UUID） |
| #4 成员页 | 页面显示「成员列表加载失败 / HTTP 404 / 错误码：404」 | 显示「测试用户 / 管理员 / 户主 / 已有账号」，1/12 |
| #5 报表页 | 「Request failed: HTTP 404」+ 重试按钮 | 概览正常：总收入 ¥0.00、总支出 ¥514.00、结余 ¥-514.00、餐饮 4 笔 ¥514.00 |
| #6 分类统计 | `missing FROM-clause entry for table "ft" (42P01)` | `{"items":[{"category_id":"...","category_name":"餐饮","amount":-51400,"percentage":100,"count":4}]}` |
| #8/#9 预算 | `POST /budgets → 400`，列表 `items:[]` | 列表显示「¥3000.00 / 已用 ¥0.00 / 0%」 |
| #10 分账 | 「Request failed: HTTP 404」 | 「暂无分账记录 / 创建分账」（空态正常） |
| #11 单位 | 总支出显示 `¥51400.00` | 显示 `¥514.00` |

### 顺带补齐的功能缺口

`/statistics/category` 此前不返回笔数，页面显示成「餐 · 笔」（数字为空）。已在服务端 SELECT 里加 `COUNT(*) as count` 并补 `Count` 字段，前端映射 `count` → 现在正确显示「4 笔」。

---

## 四、未修复项：服务端功能缺口（需产品决策）

### 成员维度统计（PRD 15.4）在当前 schema 下不成立

**这不是可以顺手补一句 SQL 的笔误，是数据模型缺列**：

1. `finance_transaction` 的 17 列里**没有记录操作人的列**（只有 `deleted_by`，无 `created_by` / `member_id`），写侧也没有写入点 —— "谁记的"这个事实在库里根本不存在；
2. 成员投影表 `finance_proj_homeos` 只有 `(family_id, member_id, role, pver, updated_at)` 五列，**既无 `id` 也无 `name`**，连 JOIN 和"成员名"都取不到。

旧实现假设了 `ft.created_by` 与 `fm.id`/`fm.name`，实测 `column ft.created_by does not exist (42703)`，即该接口**每次调用都 500**。

**本次处理**：改为返回空集合 `[]`，页面走空态 —— 给一个恒 500 的接口或编造的归属都不合适。

**建议**：finance 侧补 `member_id uuid` 列（写侧从 JWT 的 member 快照落库），并给 `finance_proj_homeos` 投影补 `id`/`name`，之后该接口才能真正实现。

---

## 五、页面覆盖矩阵（25 页全量）

### homeos 主包（12 页）

| 页面 | 路由 | 渲染 | 数据 | 交互 | 判定 |
|---|---|---|---|---|---|
| 登录 | `homeos/auth/login` | ✅ | — | ✅ 验证码倒计时 60s、登录成功 | 通过 |
| 家庭加入 | `homeos/auth/family-join` | ✅ | — | 未深测（依赖邀请码） | 渲染通过 |
| 创建家庭 | `homeos/family/create` | ✅ | — | 表单齐全（名称/时区/货币） | 通过 |
| 家庭成员 | `homeos/family/index` | ✅ | ✅ 修复后 | — | 修复后通过 |
| 邀请成员 | `homeos/family/invite` | ✅ | ❌ 404 | — | **服务端未实现** |
| 开通更多面 | `homeos/family/modules` | ✅ | ✅ | ✅ 已启用 1 面 | 通过 |
| 首页 | `homeos/home/index` | ✅ | ✅ 真实家庭名/问候语/财务卡片 | 时间窗月/季/年 | 通过 |
| 家庭动态 | `homeos/dynamics/index` | ✅ | ✅ 空态 | 全部已读 | 通过 |
| 消息 | `homeos/messages/index` | ✅ | ✅ 空态 | 三分类筛选 | 通过 |
| 我的 | `homeos/mine/index` | ✅ | ✅ 手机号/家庭/角色 | ✅ 主题切换、退出登录（二次确认） | 通过 |
| 快速添加 | `homeos/quick-add/index` | ✅ | — | ✅ 文本解析「午餐 58」→ ¥58.00 + 备注「午餐」，提交落库 | 修复后通过 |
| 隐私政策 | `homeos/legal/detail` | ✅ | ✅ 占位文案 | — | 通过（占位） |

### finance 分包（13 页）

| 页面 | 路由 | 渲染 | 数据 | 写入 | 判定 |
|---|---|---|---|---|---|
| 流水 | `finance/flow/index` | ✅ | ✅ 7 笔流水、支出/收入/结余 | ✅ 记一笔入口 | 通过 |
| 记一笔 | `finance/transaction/create` | ✅ | ✅ 分类账户自动带出 | ✅ 128.50 落库 | 修复后通过 |
| 账户管理 | `finance/account/index` | ✅ | ✅ | ✅ 新建「工商银行」落库 | 通过 |
| 分类管理 | `finance/category/index` | ✅ | ✅ | ✅ 新建「餐饮」落库 | 通过 |
| 预算 | `finance/budget/index` | ✅ | ✅ 修复后 | ✅ 新建 ¥3000 落库 | 修复后通过 |
| 统计报表 | `finance/report/index` | ✅ | ✅ 概览/趋势/排行三 Tab | — | 修复后通过 |
| 账本 | `finance/ledger/index` | ✅ | ✅ 空态 | 创建入口 | 通过 |
| 账单 | `finance/bill/index` | ✅ | ✅ 空态 + 三状态筛选 | 添加入口 | 通过 |
| 借贷 | `finance/loan/index` | ✅ | ✅ 空态 + 借出/借入汇总 | 添加入口 | 通过 |
| 储蓄目标 | `finance/goal/index` | ✅ | ✅ 空态 + 汇总 | 创建入口 | 通过 |
| AA 分账 | `finance/split/index` | ✅ | ✅ 修复后 | 创建入口 | 修复后通过 |
| 回收站 | `finance/trash/index` | ✅ | ✅ 空态 + 三类型筛选 | — | 通过 |
| 设置 | `finance/settings/index` | ✅ | ✅ 修复后（货币/小数位/预警阈值） | — | 修复后通过 |

---

## 六、真实数据闭环验证

测试共产生真实落库数据（回查 `finance` schema 确认）：

| 对象 | 数量 | 验证点 |
|---|---|---|
| 账户 | 2 | 现金类，创建即返回 `¥0.00` 余额 |
| 分类 | 2 | 「餐饮」带图标字符 |
| 流水 | 7 | 4 笔 128.50（记账页）+ 1 笔 58.00（快速添加，备注「午餐」）+ 1 笔 50.00（备注「午餐」）+ 1 笔 100.00（备注「私密支出」，切「收入」Tab 时落库，验证类型切换生效） |
| 预算 | 1 | `monthly` 周期，2026-10-01 ~ 2026-10-31，¥3000 |

**跨端一致性抽查**：流水页显示「支出 ¥572.00 / 结余 ¥-572.00」，与当时 DB 中 `4×12850 + 5800 = 57200` 分完全吻合。该数字是**测试中途快照**；全部 7 笔落库后 DB 总额为 `-72200` 分（¥722.00），两者不矛盾，只是采集时点不同。

---

## 七、回归验证（修复未引入新问题）

| 检查 | 命令 | 结果 |
|---|---|---|
| Go 静态检查 | `go vet ./...` | ✅ 通过（exit 0） |
| Go service 层单测 | `go test ./services/svc-finance/internal/service/` | ✅ ok |
| 前端工程检查 | `npm run check` | ✅ 6/6 pass |
| TypeScript 类型检查 | `npx tsc --noEmit` | 4 个错误，**与修复前完全一致**（已用 `git stash` 对比确认），且均在我未触碰的文件（`src/api/request.ts` 的 `ApiMethod` 含 `PATCH` 与 uni 类型不匹配、`tests/request.test.ts` 缺 node 类型声明） |

**存量失败（非本次引入）**：`TestUpdateFinanceSettings_FullUpdate` 失败 —— 测试用 `family_id: "test-family-004"`（非 UUID），而 handler 的 binding 是 `required,uuid`，必然 400。已用 `git stash` 验证该测试在修复前即失败。

---

## 八、遗留问题清单（不阻塞，但建议处理）

| # | 问题 | 影响 | 建议 |
|---|---|---|---|
| 1 | 流水页支出显示 `-¥128.50` | 支出已用红色表意，再叠负号是双重表达；且「结余」负号与中式记账习惯不符 | 流水列表按 `Math.abs()` 展示金额，收支方向只靠颜色区分；结余保留符号 |
| 2 | 成员页副标题显示「1/12」而非「测试家庭 · 1/12」 | `homeStore` 的 `loadSession` 只落 family_id + role，不带家庭名 | store 补落家庭名（`/home/summary` 已返回 `family.name`） |
| 3 | 预算列表分类名显示「未命名分类」 | 预算接口返回 `category_id` 但分类名字典在预算加载后才取，顺序颠倒 | 先 `loadCategoryDict` 再 `fetchBudgets`，或后端在预算响应里带 `category_name` |
| 4 | 邀请成员页 404 | 服务端只实现了 `/family/invite/accept`，生成与列表接口（`POST /members/invite`、`GET /family/invites`）未注册 | 补服务端接口（前端已按契约写好） |
| 5 | 成员维度统计无法实现 | 见第四节「功能缺口」 | finance 补 `member_id` 列 |
| 6 | `deploy/scripts/p1-e2e-test.sh` 有未提交改动 | 工作区残留（非本次修改，diff 显示是建家后 token 刷新的修复） | 确认后提交或回滚 |
| 7 | 埋点/深链未验证 | 本轮只测了页面渲染与业务交互 | 需补 4-gate 里的 G4 e2e 覆盖 |

---

## 九、复现方式

```bash
# 1. 基础容器
cd /Users/xuesh/www/HomeCube
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml up -d postgres16 nats

# 2. 交叉编译 linux 二进制
cd server
GOTOOLCHAIN=local GOOS=linux GOARCH=amd64 ~/sdk/go/bin/go build \
  -o /tmp/hc-homeos-linux ./services/svc-homeos/cmd/svc-homeos
GOTOOLCHAIN=local GOOS=linux GOARCH=amd64 ~/sdk/go/bin/go build \
  -o /tmp/hc-finance-linux ./services/svc-finance/cmd/svc-finance

# 3. 以容器常驻运行（避开宿主进程被回收）
#    镜像用本地已有的 migrate/migrate:v4.20.1，entrypoint 指向编译产物，
#    DSN 指向 host.docker.internal，UPLOAD_DIR 必须是相对路径 ./uploads/{family_id}
#    （服务启动时会校验绝对路径并拒绝）

# 4. 迁移
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml \
  run -T --rm migrate up

# 5. 前端
cd web && npx uni    # :5173，已配 /api/homeos→8080、/api/finance→8081 代理
```

**测试账号**：手机号 `13800138000`，固定验证码 `123456`（P1 本地 stub 通道，见 `server/services/svc-homeos/internal/auth/auth.go:396` 的 `DevFixedSMSCode`；接口响应里的 `channel` 字段会诚实标出 `local_stub`）。

---

## 十、测试过程截图

| 文件 | 内容 |
|---|---|
| `screenshots/01-home.png` | 首页（已启用财务面、真实家庭数据） |
| `screenshots/02-finance-settings.png` | 财务设置（修复后：货币/小数位/预警阈值正常加载） |
| `screenshots/03-finance-flow.png` | 流水页（4 笔 128.50，支出/收入/结余汇总） |
| `screenshots/04-budget.png` | 预算页（修复后：¥3000.00 正常显示） |
| `screenshots/05-members.png` | 家庭成员页（修复后：测试用户/管理员/户主/已有账号） |
| `screenshots/06-report.png` | 统计概览（修复后：¥514.00、4 笔） |
| `screenshots/07-report-trend.png` | 趋势 Tab（修复后：2026-10 收入/支出/结余） |
| `screenshots/08-report-rank.png` | 排行 Tab（修复后：支出榜有数据、收入榜正确空态） |
| `screenshots/09-quick-add.png` | 快速添加（文本解析出金额与备注） |
| `screenshots/10-flow-final.png` | 快速添加落库后的流水页（¥572.00 汇总吻合） |
