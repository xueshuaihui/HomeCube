# HomeCube P1 项目进度审核报告

**审核时间**：2026-10-09 21:10 ~ 21:20（GMT+8）
**审核对象**：`feat/p1` 分支 = `452e0e4`（与 `origin/feat/p1` 同步）+ 14 个工作区未提交改动
**审核方式**：全部结论均来自本机实跑命令与逐行读源码，标注了证据位置；未复跑的部分明确写「未验证」
**审核人**：WorkBuddy（自动审核）
**待人工复核**：是

---

## 一、结论摘要

**当前工作区不可交付，且处于「构建不过」的半成品状态。**

三条 hard blocker（P0）中的第一条是纯机械错误——工作区新增的一个文件把本仓 module 路径写成了 `github.com/xuesh/www/HomeCube/...`，而真实 module 是 `github.com/xueshuaihui/HomeCube/server`（`server/go.mod:1`），直接导致 svc-finance 的 4 个包 `setup failed`，**整个 finance 服务当前编译不出二进制**。

与此同时，已提交部分（`452e0e4`）的质量明显提升：finance 鉴权中间件真正挂上了全部 57 条业务路由，前端同源门禁从上一轮的 8 项 FAIL 收敛到 13/13 全过，G1~G5 验收脚本体系建立起来了。**问题集中在「未提交的这一批改动」**，而不是已提交的基线。

| 维度 | 状态 | 说明 |
|---|---|---|
| G1 API 契约（路由自动发现） | ⚠️ 部分 | 路由清单可重新生成（77 条，无重复），但**服务未运行，断言无法重跑**；仓内 `result-g1.json` 是 10-04 的旧结论，仅覆盖 74 条路由 |
| G2 真实浏览器 e2e | ⛔ 未执行 | `127.0.0.1:8081` 返回 502，finance 服务没起 |
| G3 构建与静态门禁 | ❌ 不通过 | 前端三项全绿；**后端 `go vet` / `go test` 因编译失败无效** |
| G4 数据不变量 | ⛔ 未执行 | 需在跑的库 + 服务 |
| G5 越权边界 | ⛔ 未执行 | 需在跑的服务 |
| CI | ❌ 仍缺失 | `.github/workflows/` 只有 `.gitkeep`；4 个门禁脚本零调用方 |

---

## 二、G3 逐项实测结果（本轮跑过的命令）

| 检查 | 命令 | 结果 |
|---|---|---|
| 前端同源+结构 | `cd web && node scripts/check-pages.mjs` | ✅ **13/13 全过**（三查 3/3 + 同源 7/7 + 结构 3 项）。上一轮的 8 项同源⑤硬编码色值 FAIL 已清零 |
| 前端类型检查（app） | `cd web && npx tsc --noEmit -p tsconfig.json` | ✅ 无错误 |
| 前端类型检查（tests） | `cd web && npx tsc --noEmit -p tests/tsconfig.json` | ✅ 无错误 |
| 请求层单测 | `cd web && npm run check:request` | ✅ 6/6 过（**但测的是死代码层，见 S1**） |
| 后端 vet | `cd server && go vet ./...` | ❌ `finance_events.go:30-31` 无法解析 import |
| 后端全量测试 | `cd server && go test ./...` | ❌ 15 包 ok / 4 包无测试 / **4 包 `setup failed`**（svc-finance 的 `cmd`、`internal/handler`、`internal/repo`、`internal/service`） |
| 路由自动发现 | `python3 server/test/acceptance/route_discovery.py` | ✅ 77 条（svc-finance 57 / svc-homeos 20），`duplicates: []`，仅 3 条公开路由（jwks + 2 条 legal），finance 侧 57 条全部在 `protected` 分组内 |

> 注：`go vet` 的 shell exit code 被管道尾部 `tail` 吃掉，不能作为「通过」的依据；判定以上面的 stderr 为准。

---

## 三、P0 阻塞项（必须修完才能谈交付）

### P0-1 · 新增文件 module 路径写错，svc-finance 编译不过

- **证据**：`server/services/svc-finance/internal/repo/finance_events.go:30`
  ```
  "github.com/xuesh/www/HomeCube/server/packages/bus"
  "github.com/xuesh/www/HomeCube/server/services/svc-finance/internal/model"
  ```
  对照 `server/go.mod:1` → `module github.com/xueshuaihui/HomeCube/server`
- **扩散面**：全仓 `grep xuesh/www/HomeCube` 只命中这 2 行（其余 .go 文件全部正确），但因为是包内文件，**任何 import 该包的包都编译不过** → cmd / handler / repo / service 四包全挂
- **修法**：2 行改回 `github.com/xueshuaihui/...`
- **验证状态**：✅ 已实测（go vet / go test 输出）

### P0-2 · `MarkAsPaid` 加了第 4 个参数，8 处调用方未同步

- **新签名**：`repo/finance.go:788` → `MarkAsPaid(ctx, familyID, id string, paidBy string)`
- **未改的调用方**：
  - `internal/handler/finance.go:996`（**生产路径** PayBill）
  - `internal/repo/finance_test.go:851`
  - `internal/repo/finance_bill_revoke_test.go:96 / 157 / 180 / 196 / 198 / 217`
- **后果**：Go 实参个数不匹配，硬编译错误。当前被 P0-1 的 import 错误掩盖，**修完导入会立刻暴露**
- **修法**：handler 传 `sess.MemberID`；测试传 `""` 或固定 member_id
- **验证状态**：✅ 已实测（grep 全部调用点 + 新签名）

### P0-3 · 周期记账会重复发布 `finance.transaction.created`

- **证据链**：
  - `repo/finance.go:437` 起，`CreateTransaction` 内**无条件**新增 `writeTransactionCreatedEvent(ctx, tx, ...)`
  - `service/recurring.go:92` 调用 `s.repo.CreateTransaction(...)` 之后，**104-115 行仍然自己再插一条**同 subject 的 outbox 行
  - 而 `repo/finance.go:437-441` 的注释自称：「周期记账原来在 service/recurring.go 里自己插一条 outbox，现在由这里统一发，**以免一次执行发两遍**」——注释描述了意图，`recurring.go` 却没有被改
- **后果**：一次周期规则执行 = 2 条 outbox 行（一条带 `is_recurring` / `recurring_rule_id` 溯源，一条不带），下游是否按 business_id 去重会产生两种落库结果
- **修法**：删除 `service/recurring.go:103-115`（连同 `bus.MarshalEnvelope` / `InsertOutboxMessageWithFamily` 调用），保留 `transactionCreatedEnvelope` 供契约测试使用
- **验证状态**：✅ 已实测（读源码）

### P0-4 · `finance_0016` 装了列，但读方与写方都没跟上

- **迁移意图**：`finance_0016_proj_homeos_account.up.sql` 给 `finance.finance_proj_homeos` 加 `account_id` 列，注释写明「消费侧：svc-finance internal/consumer 的 homeos.member.created/updated/deleted 订阅器」
- **实际情况**：
  - `server/services/svc-finance/internal/` 下只有 `handler / model / repo / service`，**没有 consumer 目录** → 该列永远 NULL，注释描述的写方不存在
  - 唯一读者 `handler/auth.go:76` 仍是 `Where("family_id = ? AND member_id IS NOT NULL").Limit(1)`——无 `order`，**按家庭取任意一行**当作调用者；`auth.go:77-79` 的注释还停留在「投影表没有 account_id 列」
  - 投影表本就无写入方，`auth.go:87` 每次请求都记 `member_projection_empty` WARN，`auth.go:92` 退化透传 `account_id`
- **连带风险（值得单独决策）**：`created_by` 今天的实际值是 **account_id**（`resolveMember` 透传，`handler/finance.go:610` 用 `sess.MemberID`），而 `finance_0015` 与列注释的口径说它是 **member_id**（与 `authz.Session.MemberID` 同源）。投影一旦落地，`member_id ≠ account_id`，**存量行的 `created_by` 会与新的判定口径整体错位**，`handler/scope.go:142` 与 `handler/finance.go:601` 的 `*tx.CreatedBy == sess.MemberID` 会集体失效
- **修法（需产品/架构确认，二选一）**：
  1. 0016 先撤下，等 svc-finance 侧 consumer 一起上；
  2. 保留列，但把 `resolveMember` 收窄为 `family_id + account_id` 精确匹配，并明确 `created_by` 存 account_id、同步改列注释与契约
- **验证状态**：✅ 已实测（目录结构 + 源码 + 迁移文件注释）

### P0-5 · demo_item 只落了表/模型/repo，没有端点也没有消费者

- **已存在**：`finance_0017_demo_item` 迁移、`model.FinanceDemoItem`（`model/finance.go:398`）、`repo.CreateDemoItem`（`finance_events.go:239`）、事件信封 `demoItemCreatedEnvelope`
- **缺失**：`CreateDemoItem` 全仓**零调用方**；handler 无 demo 路由；svc-homeos 无 `finance.demo_item.created` 消费者
- **后果**：`contracts/events/finance.yaml:123` 登记的 `finance.demo_item.created` **仍然发不出去**，P1 跨服务五步验证链路依然缺最后一环。而契约原文要求 M2 收口（S19）时删除 demo 表/接口/事件——等于把「限时工件」先建了一半
- **修法**：补一个最小 handler 端点 + homeos 侧消费者，或整体推迟到确定做五步验证时再一起上
- **验证状态**：✅ 已实测（全仓 grep 调用方 = 0）

---

## 四、已提交部分（`452e0e4`）的确实进展

这些是本次审核**逐项确认无误**的改进，不是报告自述：

1. **finance 鉴权中间件真正生效**：`cmd/svc-finance/main.go:263` `protected := group.Group("", mw.Handler())`，57 条业务路由全部挂在其后；`main.go:247-261` 装配 `handler.FinanceAuth`，`OnDenied` 同步挂给 finance 与 voice 两个 handler。上一轮报告口径：无 token / 伪造签名 / 垃圾 token 三种情况均 401（**本轮未复跑，服务未起**）
2. **前端同源门禁清零**：上一轮 8 项同源⑤硬编码色值 FAIL（bill/ledger/split/trash）已全部清除，实测这四个文件已无 `#RRGGBB` / `rgba()` 字面量
3. **金额符号单一口径（未提交但已改）**：`web/src/utils/format.ts` 的 `formatAmount` 改为符号在 `¥` 之前（`-¥1406.32`），与服务端 `svc-homeos/internal/handler/faces.go:242-259` 对齐；`flow/index.vue` 的 `amountText` 同步去掉自行拼符号
4. **6 条契约事件发布点补齐（未提交）**：`repo/finance_events.go`（340 行）覆盖 `transaction.created/updated/deleted`、`budget.exceeded`、`bill.paid`、`demo_item.created`，全部经 `bus.InsertOutboxMessageWithFamily` 与业务写**共用同一事务句柄**；`finance.transaction.updated` 的 `updated_fields` 只取调用方真实改动（`repo/finance.go:533-540`），不把 `version/updated_at` 噪声混进契约对象
5. **homeos 侧投影消费者增强（未提交）**：`consumer/finance_transaction_projection.go` 支持「只改期不换钱」时保留镜像金额只换月桶、`recompute` 从单桶扩到全家庭（避免旧月桶残留），并新增 300+ 行同包测试
6. **G1~G5 验收脚本体系建立**：`server/test/acceptance/` 下路由**自动发现**（`route_discovery.py` 从 `cmd/*/main.go` 抽注册语句，不手写清单）、`api_acceptance.py`、`authz_acceptance.py`、`data_acceptance.py`；Makefile 暴露 `acceptance` / `acceptance-verbose` / `acceptance-g3` / `acceptance-g5`。这一条直接回应了上一轮 `/statistics/summary`、`/members` 这类「不存在的路径被漏测」的教训

---

## 五、存量问题清单（延续多轮，仍未闭环）

| # | 问题 | 证据 | 状态 |
|---|---|---|---|
| S1 | **两份请求层并存，单测测的是死代码** | `web/src/api/request.ts` 在 `src/` 下零引用（只被 `tests/request.test.ts` 与 `tests/tsconfig.json:15` 的 include 引用）；真实在用的是 `web/src/utils/request.ts`（24 个文件引用）。`npm run check:request` 6/6 验的是没人用的那一层，真实层零单测 | 未解决 |
| S2 | **两层路径约定相反** | `api/request.ts:50` 的 `assertPathShape` 禁止调用方自带 `/api` 前缀；`utils/request.ts` 不拼前缀，页面全部手写完整路径（如 `pages/finance/trash/index.vue:64` 的 `request.get('/api/finance/trash')`） | 未解决 |
| S3 | **`resolveSessionFamilyId` 13 份拷贝** | 13 个 finance 页面各写一份同名函数（flow/bill/ledger/loan/goal/budget/split/trash/settings/report/account/category/transaction-create） | 未解决 |
| S4 | **CI 从未跑过** | `.github/workflows/` 仅 `.gitkeep`；`.github/scripts/` 下 `validate-migrations.sh` / `check-data-ownership.sh` / `check-frontend-homology.sh` / `check-contracts.sh` 四个门禁脚本，全仓引用只命中注释与文档（`git grep` 实证），**零调用方** | 未解决 |
| S5 | **`created_by` 全 NULL** | `handler/finance.go:333` 明确注释「created_by / visibility 的语义由另一张卡决定，这里刻意不填、不改」→ L3 私密过滤与「作者可删」两条规则在实际数据上恒定 fail-closed | 设计待办（与 P0-4 同源） |
| S6 | **`finance_proj_homeos` 无写入方** | 见 P0-4 | 未解决 |
| S7 | **成员统计无数据可返回** | 上一轮结论：`finance_transaction` 无「谁记的」列、`finance_proj_homeos` 无 id/name。`0015` 补了 `created_by` 列但无人填，问题实质未变 | 部分 |

---

## 六、报告时效性核查（历史文档的结论哪些不能引用）

| 文档 | 自述结论 | 本轮核查 |
|---|---|---|
| `docs/P1-FINAL-PROGRESS-REPORT.md` | 「70% 完成（19/27 测试通过）」 | ⚠️ **27 项口径来自 `deploy/scripts/p1-e2e-test.sh` 的自定义用例**，与 G1（77 条路由）不同源，两者不可互相引用；落款 Qoder Agent、审核状态「待人类工程师复核」，从未被复核 |
| `docs/P1-TEST-PROGRESS-81.md` | 「81% 通过率」 | ⚠️ 同上，同源问题 |
| `server/test/acceptance/result-g1.json` | 147 条断言、`failed: 0` | ❌ **已过期**。文件时间戳 2026-10-04 09:34，覆盖 74 条路由；本轮重新发现为 **77 条**（新增 homeos 的三条邀请路由：`POST /api/homeos/members/invite`、`GET /api/homeos/family/invites`、`DELETE /api/homeos/family/invites/:id`）→ 现有结论缺 6 条断言。重跑前不得引用 |
| `docs/P1-ACCEPTANCE-REPORT.md` | 「14 Sprint 全部完成」 | ❌ 与 S4 直接冲突：CI 从未运行过 |
| `docs/E2E-BROWSER-TEST-REPORT-ROUND2.md` | 上一轮 11 个缺陷 10 个修复、新发现 7 个（4 个 P0） | ⚠️ 该轮 7 个缺陷的修复状态**本轮未验证**（服务未起），其中「finance 6 个写入端点 100% 失败」是 P1 能否闭环的关键项，必须重跑 |

---

## 七、建议的收口顺序

1. **修 P0-1**（2 行 import）→ 立刻跑 `go build ./...`，暴露 P0-2
2. **修 P0-2**（8 处调用方）→ `go vet ./... && go test ./...` 必须回到全绿（当前基线：除 svc-finance 四包外全部 ok）
3. **删 `service/recurring.go:103-115`**（P0-3）→ 跑 `svc-finance/internal/service` 的 recurring 契约测试
4. **决策 P0-4 / P0-5**（这两项不是「改代码」，是「定口径」）：
   - P0-4：0016 撤下等 consumer，还是收窄 `resolveMember` 并统一 `created_by` 的 ID 空间？
   - P0-5：补 demo 端点+消费者，还是整体推迟到 M2 前？
5. **起服务重跑四道门禁**：`make acceptance`（G1）→ `make acceptance-g5` → `make acceptance-g3` → `deploy/scripts/p1-e2e-test.sh`（G2）+ `data_acceptance.py`（G4）。环境坑见 `.workbuddy/memory/2026-10-04.md`：必须 `-p 127.0.0.1:8081:8081` 显式映射（`--network host` 在 Docker Desktop for Mac 无效）、`NATS_URL` 必填、Vite 只听 IPv6 用 `localhost` 访问
6. **合两份请求层**：保留 `utils/request.ts`，把 `tests/request.test.ts` 迁过去（S2 的路径约定同时定案），删除 `api/request.ts`
7. **补 `.github/workflows/`**，接上已有的 4 个门禁脚本（S4）
8. **沉淀 S3**：抽 `resolveSessionFamilyId` 到 `utils/session.ts`

---

## 八、审核未覆盖的范围（明确声明）

- G2 / G4 / G5 三门禁**未执行**，本轮不对其做任何结论
- 上一轮 e2e 报告的 7 个新缺陷是否修复，**未验证**
- 生产/预发环境、迁移在真实 PostgreSQL 上的可执行性**未验证**（`0016` / `0017` 仅在源码层阅读）
- `web/package-lock.json` 在上一次提交中被改动（+101 行），本轮未做依赖审计
