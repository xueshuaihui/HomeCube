# HomeCube 真实浏览器全功能测试报告（第二轮 · 回归 + 补测）

**报告日期**：2026-10-04（第二轮）
**测试方式**：真实 Chromium（agent-browser 0.27，坐标级鼠标事件）+ 真实 PostgreSQL 16 + 真实 NATS JetStream + 真实 Go 服务进程（容器常驻）
**测试对象**：H5 客户端 26 个页面全量 + finance 全部 8 个写入端点 + 上轮 11 个缺陷回归
**核心结论**：上轮 11 个缺陷 **10 个确认修复、1 个部分修复**；但**补测新页面发现 7 个新的 P0 缺陷 —— finance 的 8 个写入端点中 6 个在真实浏览器里 100% 失败**。

---

## 一、测试环境（本轮重建，含上轮未记录的两个坑）

| 组件 | 形态 | 可信度依据 |
|---|---|---|
| svc-homeos | linux/amd64 交叉编译，容器常驻 `-p 127.0.0.1:8080:8080` | `/healthz` → `schema:homeos up` + `jetstream up` |
| svc-finance | 同上，`:8081`，**本轮含新鉴权中间件** | `/healthz` → `schema:finance up` + `jetstream up` |
| PostgreSQL | 16-alpine compose 常驻 | 迁移 homeos@11 / **finance@15**（0015 本轮首次应用） |
| NATS | 2.10 JetStream | 两服务启动日志 `event_side_started` / `eventbus_ready` |
| H5 前端 | Vite dev server `:5173` | 代理链路实测：`localhost:5173/api/homeos/healthz` 与 `/api/finance/healthz` 均返回真实 JSON |

### 本轮踩到并解决的三个环境坑

| 坑 | 现象 | 解法 |
|---|---|---|
| **`--network host` 在 Docker Desktop for Mac 上不生效** | 容器内日志显示 `service_started addr=:8081`，宿主 `curl` 却connection refused | 改用 `-p 127.0.0.1:8081:8081` 显式端口映射 |
| **环境变量名是 `FINANCE_ADDR` 而非 `FINANCE_HTTP_ADDR`，且必须给 `NATS_URL`** | 容器起来即退出，日志 `环境变量 NATS_URL 未设置` | 从 `deploy/env.local` 取真实键名重建 |
| **Vite 只监听 IPv6 `[::1]`，`127.0.0.1:5173` 返回 502** | 前端看似挂了 | 一律用 `http://localhost:5173` 访问 |

> 另：登录端点是 `POST /api/homeos/auth/sms-code`，**不是** `/auth/send-code`（后者 404）。

---

## 二、上轮 11 个缺陷的回归结果

用探针 `/tmp/hc-regress.py` 跑 17 项断言，**全部符合预期**。

| # | 上轮缺陷 | 回归结果 | 证据 |
|---|---|---|---|
| 1 | `finance_0014_settings.up.sql` 表级 UNIQUE WHERE PG 不允许 | ✅ 已修 | 迁移执行成功，settings 端点 200 |
| 2 | 幂等键非 UUID 导致记账 500 | ✅ 已修 | UUID 记账 200，返回 `id`/`visibility` |
| 3 | 快速添加第二处未同步修 | ✅ 已修 | 共享函数 `newIdempotencyUUID()` 生效 |
| 4 | 成员页请求 `/members` 永久 404 | ✅ 已修 | `/members/snapshot` 200；旧路径正确 404 |
| 5 | 报表页请求 `/statistics/summary` 404 | ✅ 已修 | overview/trend/category/member 四端点全 200；旧路径 404 |
| 6 | `/statistics/category` SQL `ft.` 前缀无别名 42P01 | ✅ 已修 | 返回 200 且有真实分类数据 |
| 7 | `/statistics/member` 引用不存在的 `ft.created_by` | ✅ 已修 | 返回 200 + `items:[]`（配合 0015 迁移的 fail-closed 语义） |
| 8 | 预算创建 400（period 编码 + 缺日期） | ⚠️ **部分修复** | period 编码已对，但**日期格式仍错**（见新缺陷 #4） |
| 9 | 预算列表恒为空 | ✅ 已修 | `period=monthly` 返回真实数据 |
| 10 | 分账页请求 `/splits` 404 | ✅ 已修（仅路径） | `/split-settlements` 路径正确，**但写入 body 错**（见新缺陷 #5） |
| 11 | 报表金额放大 100 倍 | ✅ 已修 | 概览「总支出 ¥1249.53」与流水页逐笔加总一致 |

### 额外验证：上轮 P0 安全问题已解决

上轮记录「svc-finance 无鉴权中间件」为 P0。本轮实测已修复且**真正生效**：

| 场景 | 结果 |
|---|---|
| 无 token 访问 `/api/finance/transactions` | **401** `登录状态无效或不属于当前家庭` |
| 伪造签名 token（合法结构、错误签名） | **401** |
| 垃圾 token（`not-a-jwt`） | **401** |

`handler/auth.go` 的 `FinanceAuth.Middleware()` 已挂在所有业务路由上（`main.go:349-352` 等）。

### 额外验证：0015 迁移生效

```
finance.finance_transaction 新增列：created_by (uuid) / visibility (text default 'shared')
CHECK 约束：ck_finance_transaction_visibility CHECK (visibility IN ('shared','private'))
部分索引：idx_finance_transaction_created_by
```

幂等性实测：同一 `client_request_id` 连续 POST 两次，返回**同一 id** `224f5c53-...`，库内仅 1 行 —— 幂等键机制真实有效。

---

## 三、本轮新发现的缺陷

### P0 — 真实浏览器里功能完全不可用

#### 新 #1：finance 8 个写入端点中 6 个 100% 失败

用探针 `/tmp/hc-write-probe.py` 严格照抄前端代码发送 body，结果 **6/8 失败**：

| 端点 | HTTP | 错误信息 | 前端发送 | 后端要求 |
|---|---|---|---|---|
| `POST /bills` | 400 | `parsing time "2026-10-04" as RFC3339` | `due_at:"2026-10-04"`（日期）<br>`title` / `recurrence` / `description` | `due_at` 需完整时间戳<br>`payee_id`（**前端根本没传**）<br>表与DTO 均无 title/recurrence 列 |
| `POST /loans` | 400 | `LenderName required` `BorrowerName required` | `counterparty_name`<br>`amount_cents`<br>`due_at` | `lender_name` + `borrower_name`<br>`principal_cents`<br>`start_date` + `end_date` |
| `POST /goals` | 400 | `TargetAmountCents required` `Deadline required` | `target_cents`<br>`target_at`（可选，不传则缺 required） | `target_amount_cents`<br>`deadline`（**required**） |
| `POST /ledgers` | 400 | `MemberIDs required` `PeriodStart required` | 仅 `name` | `member_ids[]`<br>`period_start` + `period_end` |
| `POST /budgets` | 400 | `parsing time "2026-10-01" as RFC3339` | `start_date:"2026-10-01"` | 需 RFC3339 时间戳 |
| `POST /split-settlements` | 400 | `TotalAmount required` | `member_id` + `amount_cents` | `total_amount_cents`（**字段名不同**） |
| `POST /accounts` | ✅ 201 | — | — | 契约一致 |
| `POST /categories` | ✅ 201 | — | — | 契约一致 |

**数据库侧交叉验证**（`deleted_at IS NULL`）：

```
bill 0 | goal 0 | loan 0 | ledger 0 | account 17 | category 17
```

四个业务对象**一行都没进去**。

**浏览器 UI 实测复现**（不是只看 curl）：
- 账单页填「物业费 / 268.50 / 2026-10-04」→ 点确定 → `POST /bills 400`，弹层不关闭、无错误提示
- 目标页填「换车基金 / 5000」→ 点确定 → `POST /goals 400`，弹层不关闭

**根因判断**：这不是零散笔误，而是**前端、后端、契约三方各自偏离**。核对 `server/contracts/openapi/finance.yaml` 后，责任可逐端点划分：

| 端点 | 契约（`finance.yaml`） | 后端实现 | 前端 | 谁错了 |
|---|---|---|---|---|
| `POST /bills` | `required: [title, amount_cents, due_at]`、`due_at: format: date-time`、`recurring: boolean` | 只要 `payee_id`，**无 title/recurring** | 传了 `title` ✅、`due_at` 用日期非 date-time ❌、传 `recurrence`（契约叫 `recurring`）❌ | **三方各错**：后端丢 title 字段、日期格式前端错、周期字段名前后端都错 |
| `POST /trash/{id}/restore` | `requestBody.properties.family_id`（**在 body 里**，`required: false`） | `c.Query("family_id")` 只读 query | 完全不传 | **后端违约** |

> 契约里 `title` 是 required 而后端 DTO 根本没有这个字段 —— 说明**后端实现已偏离契约**，不只是前端问题。修的时候应以契约为准改后端 DTO（补 `title`/`recurring`、`due_at` 改 `time.Time`），而不是把前端改成迁就现状。

**浏览器 UI 实测复现**（不是只看 curl）：

#### 新 #2：回收站 3 个操作全部 400

前端（`trash/index.vue:86`）：
```ts
await request.post(`/api/finance/trash/${item.id}/restore`)   // 不带任何 family_id
```

后端（`handler/finance.go:1891`）：
```go
// Get family_id from query or request body   ← 注释这么说
familyID := c.Query("family_id")             ← 实现只读 query
if familyID == "" {
    c.JSON(400, gin.H{"error": "family_id is required"})
```

注释与实现不符，实际只认 query。同样问题存在于 `DELETE /trash/:id` 与 `POST /trash/clear-expired`。

契约侧证据（`finance.yaml` `restoreTrashItem`）：`family_id` 明确登记在 `requestBody.properties`（`in: body`、`required: false`），**后端只读 query 是违约实现**，不是前端漏传。前端不传也符合契约（`required: false`），只是恰好暴露了后端的违约。

**浏览器实测**：点「恢复」→ 确认弹窗 → `POST /trash/{id}/restore 400`。

#### 新 #3：回收站确认弹窗显示 `undefined`

`trash/index.vue:82` 用 `getTypeLabel(item.original_type)` 拼提示语，实测渲染为：

> 确定要恢复这条**undefined**记录吗？

`original_type` 的实际取值与 `getTypeLabel` 的映射表对不上。

#### 新 #4：弹层底部操作按钮在小屏视口不可见

**实测数据**：视口 `1280×633`，`.dialog-content` 的 `scrollHeight: 646` 但 `max-height: 80vh` = 506px。

- 弹层渲染在 `47~586`
- 「确定」按钮实际位置 `615~677` —— **完全在视口外**
- `document.elementFromPoint(717, 646)` 返回 `null` —— 该点无任何元素

`.dialog-content` 有 `overflow-y: auto`，滚动后按钮可达（`507~569`），但用户第一眼看不到按钮、也无任何滚动提示，**实际会认为按钮丢失**。

涉及 **8 个页面**（同一套 CSS 复制）：`bill / budget / goal / loan / split / account / category / ledger`。

**根因**：`max-height` + `overflow-y: auto` 加在了整个 `.dialog-content` 上（含底部按钮区），而不是只让表单区滚动、按钮区固定。

### P1 — 功能错误但未完全阻断

#### 新 #5：金额符号口径三处不一致

| 位置 | 显示 | 对同一批数据的口径 |
|---|---|---|
| 流水页「支出 ¥1249.53」 | 正数 | 绝对值 |
| 流水页「结余 ¥**-**1249.53」 | 负号在货币符号后 | 负值 |
| 报表页「总支出 ¥1249.53」 | 正数 | 绝对值 |

`¥-1249.53` 不符合中文金额规范（应为 `-¥1249.53`）。且服务端 `amount_cents` 对 expense 存负数，报表聚合也返回负数（`total_expense: -109831`），前端三处各自做了不同的绝对值处理。

#### 新 #6：写入失败无任何用户可见反馈

6 个写入端点失败时，页面表现完全一致：
- 弹层不关闭
- `uni.showToast` 在 `catch` 里被调用，但 toast 文案是 `err.message`，而 `utils/request.ts` 抛出的错误对象上没有可读 `message`
- 控制台无 error（说明连 `console.error` 都没执行到，或被吞掉）

实测截图 `screenshots/r2-03-goal-400.png`：表单原样保留，用户无法判断是网络问题、校验问题还是服务端 bug。

#### 新 #7：登录错误提示不区分「未发码」与「码错误」

未先调 `sms-code` 直接 `POST /auth/login` → 返回 `invalid_code 验证码错误或已过期`，与「验证码真的输错」完全同义。用户会反复重输正确验证码。

---

## 四、页面渲染覆盖（26/26）

全部 26 个页面在真实浏览器里渲染正常，**无白屏、无 404、无 JS 运行时错误**：

| 分组 | 页面 | 状态 |
|---|---|---|
| homeos（12） | home / quick-add / dynamics / messages / mine / family / family-create / family-invite / family-modules / auth-login / auth-family-join / legal-detail | ✅ 全部渲染 |
| finance（13） | flow / transaction-create / budget / report / category / account / settings / bill / loan / goal / ledger / split / trash | ✅ 全部渲染 |
| 其他 | — | — |

真实数据渲染正确的页面：首页（户主/测试家庭/已启用 1 面）、流水页（22 笔）、家庭成员（1/12，测试用户/管理员/户主）、回收站（1 条，含删除与到期时间、剩余 30 天）、财务设置（CNY/2 位小数）、报表三 Tab。

---

## 五、截图

| 文件 | 内容 |
|---|---|
| `screenshots/r2-01-report.png` | 报表概览（总收入 ¥0 / 总支出 ¥1249.53，金额口径问题） |
| `screenshots/r2-02-flow.png` | 流水页（结余显示 `¥-1249.53`，符号口径问题） |
| `screenshots/r2-03-goal-400.png` | 目标创建 POST 400 后弹层原样保留、无提示 |

---

## 六、遗留功能缺口（非本轮缺陷，但需明确定界）

| # | 缺口 | 影响 |
|---|---|---|
| 1 | **`finance_proj_homeos` 无任何写入方** | 每次请求都刷 `member_projection_empty` WARN。`handler/auth.go` 的注释已诚实记录：token 验签守住家庭边界，但 finance 无法独立复核成员归属。PRD 16.3 跨域成员投影未实现 |
| 2 | **22 条流水 `created_by` 全为 NULL** | 0015 迁移按 fail-closed 设计（NULL = 未知作者 → 仅 owner 可删/可看私密）。存量数据需回填才有 L3 意义 |
| 3 | **两份请求层并存** | `api/request.ts`（119 行，零调用方，单测测的是它）与 `utils/request.ts`（239 行，23 页面在用，零单测） |
| 4 | **`resolveSessionFamilyId` 在 4 个页面各写一份** | `bill / split / transaction-create` 等重复实现，改动需同步多处 |
| 5 | **`npm run check:pages` 仍报 8 项 FAIL** | 全部是同源⑤硬编码色值（bill/ledger/split/trash 的 `#FF3B30` 与 `rgba(`）。PRD 22.5 第 4 道「任一命中即阻断合并」 |

---

## 七、修复优先级建议

**P0（阻断合并）**

1. **对齐 finance 写入契约** —— 6 个端点的前端 body 必须改到与后端 DTO 一致。**建议先补 `finance.yaml` 契约登记**（若已登记则以契约为准改前端；若未登记则说明契约缺失本身是根因），再逐页修。涉及 `bill / loan / goal / ledger / budget / split` 6 个页面。
2. **日期格式统一** —— 6 个端点里 3 个栽在 `2026-10-04` vs RFC3339。建议前端统一走一个 `toRFC3339(dateStr)` 工具（`utils/format.ts` 已有 `newIdempotencyUUID()` 先例）。
3. **回收站 3 端点的 `family_id`** —— 二选一：前端补 query，或后端改成兼容 body（注释本来就写的是 body）。同时修正注释与实现不符。
4. **弹层按钮固定** —— 8 个页面把 `max-height/overflow` 从 `.dialog-content` 移到内部表单区，底部 `.dialog-actions` 固定不滚动。
5. **写入失败必须可见** —— `utils/request.ts` 抛出的错误要带可读 `message`，让 toast 真正显示服务端原因。

**P1**

6. 金额符号口径三处统一（`¥-x` → `-¥x`）。
7. `getTypeLabel` 映射表对齐 `original_type` 实际取值。
8. 登录区分「未发码」与「码错误」。

**P2**

9. 合并两份请求层，删死代码并把单测移到真实层。
10. 抽出共享的 `resolveSessionFamilyId`。
11. 清 8 处硬编码色值（解除 PRD 22.5 阻断）。

---

## 附录：本轮测试用命令

```bash
# 1. 起基础设施
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml \
  up -d postgres16 nats

# 2. 交叉编译（--network host 在 Docker Desktop Mac 上不生效，必须用 -p 端口映射）
cd server
GOTOOLCHAIN=local GOOS=linux GOARCH=amd64 ~/sdk/go/bin/go build \
  -o /tmp/hc-finance-linux ./services/svc-finance/cmd/svc-finance
GOTOOLCHAIN=local GOOS=linux GOARCH=amd64 ~/sdk/go/bin/go build \
  -o /tmp/hc-homeos-linux ./services/svc-homeos/cmd/svc-homeos

# 3. 迁移（注意 finance 已到 15）
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml run -T --rm migrate up

# 4. 容器常驻。键名取自 deploy/env.local：
#    HOMEOS_ADDR / FINANCE_ADDR（不是 *_HTTP_ADDR）、NATS_URL 必填
docker run -d --name hc-test-finance -p 127.0.0.1:8081:8081 \
  --add-host host.docker.internal:host-gateway \
  -v /Users/xuesh/www/HomeCube:/app \
  -v /Users/xuesh/www/HomeCube/deploy:/deploy \
  -v /tmp/hc-finance-linux:/hc-finance -w /app \
  --entrypoint /hc-finance \
  -e NATS_URL="nats://host.docker.internal:4222" \
  -e FINANCE_DSN="host=host.docker.internal port=5432 user=hc_finance password=test123 dbname=homecube search_path=finance" \
  -e HOMEOS_DSN="host=host.docker.internal port=5432 user=hc_homeos password=test123 dbname=homecube search_path=homeos" \
  -e UPLOAD_DIR='./uploads/{family_id}' \
  -e HOMEOS_JWT_PRIVATE_KEY_PEM=/deploy/test-rs256-key.pem \
  -e HOMEOS_JWT_KEY_ID=test-key-1 \
  -e HOMEOS_ADDR=":8080" -e FINANCE_ADDR=":8081" \
  migrate/migrate:v4.20.1
# homeos 同理，换二进制、端口 8080、entrypoint /hc-homeos

# 5. 前端（一律用 localhost，不要用 127.0.0.1 —— Vite 只监听 IPv6 [::1]）
cd web && npx uni    # :5173

# 6. 登录
curl -X POST http://127.0.0.1:8080/api/homeos/auth/sms-code \
  -H 'Content-Type: application/json' -d '{"phone":"13800138000"}'
curl -X POST http://127.0.0.1:8080/api/homeos/auth/login \
  -H 'Content-Type: application/json' -d '{"phone":"13800138000","code":"123456"}'
```

**测试账号**：手机号 `13800138000`，固定验证码 `123456`（local_stub 通道，响应里的 `channel` 字段会诚实标出）。
**测试家庭**：`660e8400-e29b-41d4-a716-446655440001`（role = owner）。
