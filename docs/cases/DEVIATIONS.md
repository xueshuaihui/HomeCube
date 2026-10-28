# 契约偏差登记（DEVIATIONS）

> **为什么有这个文件**：有一类条目「代码主体在、但 HTTP 面与 PRD 不符」。把它标 `已实现` 会让 19.2#1 被误判可验收；把它标 `未实现` 会重复派卡去重建已有功能。这类条目单列，登记为 `DEV-NN`。
> **与三条线的关系**：L1 需求缺口 → `SPEC-GAP`（BACKLOG §一）；L2 实现缺口 → `IMPL`（BACKLOG §二）；`契约偏差` → 本文件。三者互不替代。

## 一、边界规则（唯一判据，防滥用）

| 代码事实 | 归类 | 登记处 | 派工形态 |
|---|---|---|---|
| handler 在 + 路由在，但**路径/方法/响应体**与 PRD 不符 | `契约偏差` | 本文件 `DEV-NN` | 对齐为主，不重建 |
| handler 在 + **路由完全未注册**（HTTP 不可达） | `契约偏差` | 本文件 `DEV-NN` | 接线为主，不重建 |
| **handler 也不在** | `未实现` | `BACKLOG.md` `IMPL-NN` | 从零交付 |

> 一句话：**「handler 在、路由/方法不在」→ DEV；「handler 也不在」→ IMPL。**
> 判这一条**必须先 grep 全仓确认 handler 是否存在**，不能按 PRD 缺什么就推什么——本文件 §五 记录了一次因此撤回的 DEV。

## 二、纪律（lint G-G4 会拦）

1. 每条 `契约偏差` 的 case 必须 **同时**满足：`实现现状（快照）：契约偏差（… 文件:行号 …）` **且** 有 `- 偏差登记：DEV-NN`。
2. `契约偏差` 的 `执行状态` 只允许 **`失败`** 或 **`未执行`**；**禁止「通过（除路径外）」这类软话**。
3. 本文件是该偏差的**唯一状态源**。BACKLOG 里若有同一件事的工单行（承载工时与依赖），状态一律以本文件为准，不得两处各写一份。
4. DEV 状态改 `关闭` 后，被它阻塞的 case 必须**同批**改 `已实现(HTTP)`；仍留 `契约偏差` 即 G-G4 报错——这是防止「偏差修了、case 没更新」双向漂移的唯一手段。
5. 每条 DEV 必须至少挂 1 条 case。**没有 case 指向的 DEV 是幽灵登记**，lint 会 warn。

## 三、状态定义

| 状态 | 含义 | 对 case 的约束 |
|---|---|---|
| `开` | 偏差仍在，PRD 路径/方法不可用 | case 不得判 `通过`；`执行状态` 只能 `未执行` 或 `失败` |
| `关闭` | 已对齐（或旧路径在兼容期内明确保留并登记） | case 必须已改 `已实现(*)`，否则 G-G4 报错 |

## 四、登记表

| 编号 | 差异类型 | 阻塞 case | 代码现状（证据） | 契约要求 | 派卡 | 状态 |
|---|---|---|---|---|---|---|
| DEV-01 | **路径单复数** | HOM-SESS-004（P0） | `cmd/svc-homeos/main.go:228` 注册 `POST /family/switch` | PRD 3.7:419 `POST /api/homeos/families/switch` | C-H-01 | 开 |
| DEV-02 | **路由未注册** | FIN-TX-011、FIN-TX-012（P1） | 四个 tag handler 在 `handler/finance.go:1770/1797/1979/2027`；`cmd/svc-finance/main.go` 全文 `tag` 零命中 | PRD 4.7:646 `Tag` 实体 + 4.4/4.5.1「按标签筛选」（**4.8 接口表未登记任何 tag 接口**，见 §4.2） | C-F-01 | 开 |

---

### 4.1 DEV-01 · `POST /family/switch`（单数）vs PRD `POST /families/switch`（复数）

**差异类型**：路径单复数（资源集合名写错）

| 项 | 内容 |
|---|---|
| 代码实际路径 | `/api/homeos/family/switch`（`cmd/svc-homeos/main.go:228` `protected.POST("/family/switch", …)`，组前缀为 `services.RoutePrefix`；实际注册结果见 `server/test/acceptance/routes.json:378`） |
| 业务实现 | `internal/handler/family.go:381` `SwitchFamily`（响应体 `:359 SwitchFamilyResponse` 含轮换后的 token pair 与新家庭快照） |
| 已有测试 | `internal/handler/auth_test.go:431` 断言「切到非本家庭 → 403 + 落审计」；`:442` 起校验审计条数为下限 |
| PRD 要求 | 3.7:419 `GET /api/homeos/families` / `POST /api/homeos/families/switch`，P1-M1 新建 |
| 契约文件 | **`server/contracts/openapi/homeos.yaml` 未登记任何 `/family*/switch`**（该文件 20 条 path 中无 switch）——契约文件本身也缺这一条 |
| 追溯表 | `traceability.md:15` 按**复数**登记，与代码不一致 |
| 影响 | 按 PRD 路径打请求 → 404，HOM-SESS-004 的「切换后重签发」不可验收；19.2#1 多家庭上下文重建无法端到端证明 |

**关闭动作**（顺序不可颠倒）：

1. `main.go` 注册 `POST /families/switch`（同一 handler），`/family/switch` 保留为**旧别名**并在注释写明 `Deprecated: 兼容期至 <版本>`；
2. 补 `contracts/openapi/homeos.yaml` 的 `/families/switch`（请求体 `family_id`、响应 `access_token`/`refresh_token`/家庭快照三点必须写全）；
3. `server/test/acceptance/routes.json` 重抓，确认两条路径都在册（旧别名不得判失败）；
4. 补 http 场景 `sess_004_switch_reissues_token`（新 token 的 `family_id` 为 B、旧 token 打 B 被拒）；
5. case `HOM-SESS-004` 改 `已实现(HTTP)` + 证据带新行号；本文件状态改 `关闭`。

**验收口径**：新旧两条路径返回**同一结构**；旧 token 打新家庭 403（不得因同一账号放行）；兼容期结束前删除别名须走一次 PRD 变更，不得静默删除。

### 4.2 DEV-02 · 标签四接口（handler 已写、路由未挂载）

**差异类型**：路由未注册（HTTP 不可达）

| 项 | 内容 |
|---|---|
| handler（已在盘） | `internal/handler/finance.go:1770` `CreateTag`（声明 `POST /api/finance/tags`）、`:1797` `ListTags`（`GET /api/finance/tags?family_id=`）、`:1979` `UpdateTag`（`PUT /api/finance/tags/:id`）、`:2027` `DeleteTag`（`DELETE /api/finance/tags/:id`） |
| 仓储层 | `internal/repo/finance_tag_test.go`（测试在盘，覆盖建/列/改/删） |
| 路由 | `cmd/svc-finance/main.go` 全文大小写不敏感检索 `tag` → **零命中**。故四条路由均未注册，HTTP 不可达 |
| PRD 要求 | 4.7:646 `Tag` 实体（`finance_tag`，`Transaction.tags` 经 id 引用）；4.4/4.5.1「标签」与「按标签筛选」；19.3 闭环域含「标签可按筛选命中」 |
| **需求缺口（新发现）** | **PRD 4.8 接口表（648-667）没有登记任何 tag 接口**，`contracts/openapi/finance.yaml` 也只有 schema 里的 `tags` 字段、无 `/finance/tags` 路径。→ 已登记 `SPEC-GAP-08`（建议路径即 handler 注释所写的四条，待确认后写进 4.8） |
| 影响 | FIN-TX-011 只能停在「已实现(分层)」、FIN-TX-012 停在「待自动化」；19.3「标签可按筛选命中」当前**无 HTTP 面可判** |

**关闭动作**：先在 4.8 与 openapi 补登记（SPEC-GAP-08 确认后），再在 `main.go` 挂 4 条路由（放在 `protected` 组，鉴权口径与 `categories` 一致），补 http 场景「建标签 → 记账挂标签 → 按标签筛选命中 2 条 → 删标签后流水标签置空」，改 FIN-TX-011/012 为 `已实现(HTTP)`。

**验收口径**：`GET /api/finance/tags` 返回本家庭标签；按标签筛选的命中数与直接按 id 过滤的仓储结果**逐条一致**（不能用「数量非 0」代替）。

---

## 五、撤回记录（保留编号，不复用）

### DEV-03（原计划拟给 `PUT /ledgers/{id}`）——**撤回，不成立**

计划 §7.1 曾把 BACKLOG 的 **IMPL-12**（`PUT /api/finance/ledgers/{id}`）按「handler 部分在、方法缺」迁为 `DEV-03`。**按本文件 §一的边界规则复核后不成立**：

- 全仓检索 `UpdateLedger` → **零命中**（`repo/finance.go` 只有 `CreateLedger:618` / `ListLedgersByFamily:648`；`handler/finance.go` 只有 `CreateLedger:638` / `ListLedgers:669`）；
- `cmd/svc-finance/main.go:286-287` 只注册 `POST /ledgers` 与 `GET /ledgers`；
- 即 **handler 也不在**，属「从零交付」→ 归 `未实现` / `IMPL-12`，**不进本文件**。

因此：`FIN-LED-003` 维持 `未实现`（它本来就是这么写的），`IMPL-12` 保留在 BACKLOG，编号 `DEV-03` **作废不复用**。

> 这条撤回本身就是 §一 存在的价值：如果只按「PRD 说要有 PUT、代码没有」推，就会把一张 1.5 人日的从零开发卡，错标成一张「改个方法名」的对齐卡，然后在 W4 才发现工作量翻倍。

### IMPL-01/02/03（登录链路）——**证据失效，撤销**

原描述「`server/` 下 `auth/login`、`sms` 零命中；`main.go` 无 auth 路由组」经 `audit_baseline.py` 与人工复核已失效：`cmd/svc-homeos/main.go:152-167` 注册了完整 `authGroup`，`handler/auth.go` 832 行、`handler/auth_test.go` 993 行 10 个测试。三条已从 BACKLOG 移出（见 BACKLOG §二 的「已撤销」小节）。

其中 IMPL-02 的实质内容（`/families/switch` 对齐）已由上表 **DEV-01** 承接。

---

## 六、关闭流程（每次关闭照此走，缺一步不算关）

```
① 代码对齐（含旧路径兼容期与注释）
② 契约文件补齐（openapi yaml）＋ routes.json 重抓
③ checks 里加/改 http 场景，run_checks.py 跑绿
④ case 改 已实现(HTTP)：证据换成新行号
⑤ 本文件状态改 关闭，并在「关闭记录」留一行（日期 / 人 / 证据 run-id）
⑥ lint.sh 全绿
```

## 七、关闭记录

| 编号 | 关闭日期 | 关闭人 | 证据（run-id / PR） |
|---|---|---|---|
| — | — | — | 暂无（当前 2 项均为「开」） |
