# 家立方 HomeCube · P1 技术方案（底座 + 财务面整面）

> **文档性质**：研发交付物，不是需求文档。需求唯一权威源是 `docs/prd-homecube.md`（23.4）；本方案只回答「14.5 的 P1 十五项交付在七服务目标拓扑、服务随面出生的当期形态下怎么落地」，不新增、不裁剪任何需求口径。与本方案冲突的需求表述以 PRD 为准；PRD 未覆盖的技术分叉在第十五章给出定版口径，属 P1 契约。
>
> **范围**：P1 = **底座 + 财务面整面**的垂直切片，内分两个验收点——**M1**（底座 + 财务单人闭环，26-31 周 = 13-15 个 Sprint）与 **M2**（财务深度域与整面，10-12 周 = 5-6 个 Sprint）。**M1 未过不得开工 M2**（11.7 第 2 条）。收口判定 = 14.5 十五项 + 18.2 的 M1 附加门禁三项 + P1 末附加门禁三项。
>
> **当期形状**：实建 **2 个服务**（`svc-homeos` + `svc-finance`）、2 个 schema、2 条迁移序列、1 个前端分包（`pages/finance`）；另外五个 code 只以 **registry 条目**存在，不建服务、不建 schema、不建分包、不建流（11.7 第 1 条、卷首第 12 项）。
>
> **明确不做**（依 11.3、14.6、23.5）：其余五面任何业务功能；规则引擎与跨面联动业务（P6）；AI 归类与预测（P6）；隐私政策与用户协议文本（P1 末附加门禁）；除 OCR/ASR 外的依赖选型定版（P1 末附加门禁②）；真实推送凭证（P1 末附加门禁③）；Kubernetes、服务网格、独立网关、分布式事务、Redis、JetStream 集群。P1 期间一切未定版外部服务以 `packages/adapter/*` 接口 + 本地实现或桩交付。

---

## 一、运行时拓扑与工程结构

### 1.1 当期拓扑（卷首第 12 项、22.2 第 1 条）

同机 Docker Compose 常驻 **8 个容器**（七服务全出生时为 13 个，本方案不预建）：

```
                ┌────────────────────────────────┐
  client ──────▶│ nginx :443/80  前缀反代 + TLS + 静态 │  客户端只对一个 base URL
                └───────┬────────────────────────┘   (17.7 第 5 条)
     /api/homeos/    /api/finance/    其余五个前缀 → 404「未启用」
                ┌───────┴──────────────┐
                │ svc-homeos  svc-finance │  2 个 Go 服务，各一镜像一发布线
                └───────┬──────────────┬─┘
                        │              │
         ┌──────────────▼───┐   ┌──────▼─────────────────┐
         │ postgres16       │   │ nats (JetStream)       │
         │ 2 schema/2 账号   │   │ HC_HOMEOS HC_FINANCE    │
         │ （目标 7/7）      │   │ + HC_ARCHIVE + HC_DL    │
         └──────────────────┘   └────────────────────────┘
     另加：web（静态）、admin（静态）两个常驻容器，
          与一次性 migrate 初始化容器（重放两条序列后退出，不计入常驻数）
```

- **不设网关服务**：Nginx 只做前缀转发、TLS 与静态资源，**不做鉴权**；鉴权在每个服务的中间件里由同一 SDK 完成（22.2 第 8 条）。未出生面的 `/api/{code}/` 前缀由 Nginx 直接返回「未启用」（17.7 第 2 条），**不得**为凑齐路由而留空服务容器。
- 发布顺序恒为 `svc-homeos` 先于业务面服务（SDK 与事件目录的提供方先行），Compose 里 finance 用 `condition: service_healthy` 前置。
- 单机资源门槛按**当期容器数**实测（21.1 全栈资源行）：P1 是 8 个常驻，七服务全出生时才是 13 个与 4C/8G 目标阈值。实测超过当期阈值走 11.9 第 ④ 项**同镜像多进程合并部署**降级，不改代码拓扑，且写进当期风险记录。

### 1.2 仓库布局（22.4）

```
HomeCube/
├── server/
│   ├── services/                      当期只有两个
│   │   ├── svc-homeos/                handler/service/repo/model/dto + 本服务订阅器
│   │   └── svc-finance/               同上
│   │   （purchase/diet/trip/kin/growth 的目录不创建，出生期才建）
│   ├── packages/                      仅三条约束：无业务语义、可被服务依赖、不得反向依赖服务
│   │   ├── authz/                     JWT 校验 + 权限判定 + pver 缓存（语义由 svc-homeos 维护，15.6）
│   │   ├── bus/                       outbox 投递器、JetStream 封装、durable consumer、死信
│   │   ├── rclient/                   跨服务只读客户端（超时/重试/降级三要素强制声明，14.7）
│   │   ├── sync/                      change_log 追加、delta 查询、幂等表基类
│   │   ├── proj/                      投影表写入器与重建
│   │   ├── obs/                       结构化日志、指标（常量标签 code）、审计上报
│   │   ├── registry/                  域表：code ↔ 服务名/schema/路由/subject/分包/迁移目录
│   │   └── adapter/                   外部服务边界：push/ocr/asr/map/storage/ai
│   ├── contracts/                     openapi/{code}.yaml、events/{code}.yaml，版本化
│   ├── migrations/                    当期两条序列：migrations/{code}/{code}_0007_desc.up.sql
│   └── test/                          门禁级集成测试（权限矩阵、并发写、重放、对账）
├── web/                               uni-app(Vue3)：shell + pages/finance
├── admin/                             Vite + React：最小管理后台
├── deploy/                            compose、nginx.conf、备份/恢复/回滚、env 样例
└── .github/workflows/                 CI 五道门禁（22.5），本仓库自建，不共享 workflow
```

**依赖方向由 CI 静态检查（22.5 第 4 道）**：

- `services/* → packages/*`，允许；`packages/* → services/*`，禁止；
- `services/A → services/B` 的任何 import 禁止——这是唯一能在**编译期**证明服务边界存在的手段，比运行期约定强；
- 跨面调用只能出现两种形态：`rclient.Call`（只读）与 `bus` 订阅/发布；
- **registry 里没有实建服务的那五个域，任何目录、迁移文件、分包、subject 配置出现即门禁 2/4 失败**（不是靠人工记住「这期还不该有它」）。

### 1.3 命名一致性：一个 code，七处生效（16.1、14.5 第 1 项）

`packages/registry` 里一张域表是唯一真源，**P1 一次性登记七个 code**，`registry.Domains()` 被服务启动、迁移工具、前端构建脚本与 CI 共用：

| 维度 | 取值 | P1 实际落地 | 校验点 |
|---|---|---|---|
| 服务名 | `svc-{code}` | 2 个 | 镜像名、Compose 服务名、容器名 |
| schema | `{code}` | 2 个 | 连接串 `search_path={code}`、账号名 `hc_{code}` |
| 路由 | `/api/{code}/*` | 2 组 + 5 个 404 前缀 | Gin 路由组 AST 检查 |
| subject | `{code}.` | 2 条流 | JetStream 流 subjects、事件目录 |
| 表前缀 | `{code}_` | 2 个前缀 | GORM `TableName()`、迁移文件内容 |
| 前端分包 | `pages/{code}` | 1 个（finance） | `pages.json` 三查：**无 `tabBar` 字段**、无未出生面 root、路由全三段式（导航文档 §2.4） |
| 迁移序列 | `migrations/{code}/` | 2 条 | golang-migrate 目录 |

**CI 校验的是「已存在的东西必须合法」与「不该存在的东西必须不存在」两件事**：七域的命名规则全生效（例如有人提交 `migrations/diet/…` 即失败），但只有登记为实建的域才要求文件存在。任一处偏离即门禁 2/4 失败，「架构漂移」因此在合并前被拦住。

---

## 二、数据边界：单集群，当期两 schema

### 2.1 账号即边界（22.2 第 2 条）

一个 `postgres16` 实例，**当期两个 schema、两个角色**（目标形态 7/7）：

```sql
CREATE SCHEMA finance;
CREATE ROLE hc_finance LOGIN PASSWORD '…';
GRANT USAGE ON SCHEMA finance TO hc_finance;
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO hc_finance;
REVOKE ALL ON SCHEMA public FROM hc_finance;
```

- 每个服务只用自己那串 DSN（`user=hc_finance dbname=homecube search_path=finance`），**跨 schema 在权限层就做不到**，不靠代码约定。
- 表名在 schema 内仍带 `{code}_` 前缀（16.1 的双保险）：一旦出现他域前缀的建表，门禁 2 立刻发现——包括「提前替还没出生的面建表」这种 P1 特有形态。
- 无跨 schema 视图、DBLink、扩展共享；`public` schema 不放业务对象。
- 建 schema 与建账号的动作只发生在 `make up` 与迁移容器里，不由服务进程在运行期自触发（避免账号密码进应用配置）。

### 2.2 迁移（14.5 第 1 项、16.6、22.5 第 1 道）

**每服务一条独立序列**，golang-migrate 一个实例一条序列、各自版本表 `schema_migrations_{code}`（落在自己 schema 内）。CI 逐服务在空库重放自己那条序列，并校验代码声明的版本与迁移头部一致。当期两条序列，其余五条随各面出生期新增——**序列数随出生期增加不是异常，是设计**。

约定：
- 迁移只允许 `CREATE TABLE`/`ALTER` 本域前缀的表；出现他域前缀即门禁 2 失败。
- **不存在跨服务外键**（16.3）：引用他域对象只存 `id`，无物理约束，完整性由每日对账兜住。这条纪律让各条序列彻底解耦，发布不需要跨域排序。
- 业务表必备列：`id uuid primary key`（UUIDv7，时间有序抑制索引膨胀）、`family_id uuid not null`、`version bigint not null default 1`、`created_at/updated_at`、`deleted_at/deleted_by`（14.7、22.2 第 9 条）。系统预置数据用可空 `family_id` 表达（财务的预置分类与预置账户即属此类）。
- 金额一律 `amount_cents bigint`；禁止浮点与 `numeric` 混用（14.7）。
- 索引第一列固定 `family_id`；流水表按 `family_id + occurred_at` 分区/索引（21.2），**P1 内就要按 6 万条量级建好索引口径**，不留给 M2 补课。

### 2.3 每个服务的底座侧表（同构命名，落各自 schema）

| 表 | 用途 | 为什么 per-service |
|---|---|---|
| `{code}_outbox` | 发布前落盘，投递器异步搬入 JetStream | 事务边界只在单库单 schema 内成立，跨服务无法同事务写总线 |
| `{code}_event_dedupe` | 消费幂等键 `(event_type, business_id)` | 去重是消费方自己的状态，不能共享别人的表 |
| `{code}_dead_letter` | 死信落库与人工重放 | 死信归属消费方（22.2 第 10 条） |
| `{code}_change_log` | 同步增量游标源 | HomeOS 不代管他服务变更（14.5 第 5 项） |
| `{code}_idempotency` | `client_request_id` 去重 | 同上 |
| `{code}_proj_{src}` | 他域数据的本地投影 | 投影只允许本服务订阅器写，可随时重建（16.3）；**P1 只有 `finance_proj_homeos` 一张** |

`svc-homeos` 额外持有 `homeos_due_registration`、`homeos_notification*`、`homeos_search_index`、`homeos_audit_log`、`homeos_event_archive`（冷存）等全局对象。

---

## 三、跨服务通信

### 3.1 JetStream 流规划（3.4.6、10.4）

| 流 | subjects | 保留 | P1 状态 |
|---|---|---|---|
| `HC_HOMEOS` | `homeos.>` | `max_age=90d`，`R=1` | 建 |
| `HC_FINANCE` | `finance.>` | 同上 | 建 |
| `HC_PURCHASE` … `HC_GROWTH`（5 条） | `{code}.>` | 同上 | **不建**，各面出生期建（registry 已登记命名） |
| `HC_ARCHIVE` | `arch.{code}.>` | `max_age=365d` | 建；归档消费者 `hc-archiver` 从当期在运的流复制，实现 21.4「热 90 天 / 冷 1 年」；回放走这条流 |
| `HC_DL` | `dl.{code}.>` | `max_age=90d` | 建；重试超限后 nack+term，同时写 `{consumer}_dead_letter` |

- 每服务只被授权 publish 自己那条流（`$JS.API.STREAM.PUBLISH.{code}.*` ACL），发他域 subject 直接失败；未出生面的流不存在，因此「提前发他域事件」在中间件层就报 `no stream`，不靠评审拦。
- subject 命名即事件名：`{code}.{object}.{action}`（10.1），小写点分；版本不进 subject 而进信封 `version` 字段，破坏性变更走 `{action}_v2` 新事件名并保留旧名一个期（10.4）。
- 流不启用 workqueue：一条事件常有多个消费者，各消费者独立 durable consumer。

### 3.2 事件目录先于生产者（14.5 第 6 项）

`server/contracts/events/{code}.yaml` **P1 即登记七个 code 的主题域与 10.3 十一条事件**（含各自的启用期字段），未出生面只有目录条目、无生产者、无消费者声明指向不存在的服务。这样做的收益：事件命名与归属在 P1 就被钉死，P2-P6 只扩不改；CI 的门禁 5 从第一天起就能判「发布未登记事件」。**登记目录 ≠ 创建流**，流与订阅器随出生期落地。

### 3.3 发布：事务内 outbox，提交后异步投递

```
业务事务: INSERT 业务行 + INSERT {code}_outbox(subject, envelope, status=pending)
投递器:   每 500ms 批量 100 行 → JetStream Publish（异步 ack）→ 成功置 sent，失败 attempts+1
崩溃恢复: 重启即扫 pending；attempts>10 告警（不丢，只是没送）
```

代价是事件传播多 ≤1 秒的固有延迟，因此 18.1 的收敛口径（P95 ≤5s、P99 ≤60s）在本实现下有余量。**明确不提供「发布即可见」语义**：任何要求同步可见的读都走自己 schema 或本地投影，不假装跨服务强一致。

### 3.4 消费：durable consumer + 幂等 + 退避 + 死信

- consumer 名 `{consumerCode}-{event_type}`（如 `homeos-finance-transaction-created`），`filter_subject` 精确匹配，`ack_wait=30s`，`backoff=[1s,10s,60s]`，`max_deliver=4`（对应 10.4 的至多重试 3 次）。
- 进 handler 第一件事：`INSERT ON CONFLICT DO NOTHING INTO {code}_event_dedupe`；已存在即 ack 返回——**重复投递不产生重复业务对象是表约束保证，不是代码纪律**。
- 超限失败 → publish 到 `dl.{consumerCode}.{event_type}` + 写 `{code}_dead_letter`；管理台按服务查看并人工重放（重放即原样重新入队，走同一幂等键）。
- 每日清理 `event_dedupe` 中 7 天前的行，防表膨胀。

### 3.5 跨服务只读调用：声明式客户端（16.3、14.7）

```go
rclient.Call(ctx, rclient.Request{
    Target: "homeos", Path: "/api/homeos/members/snapshot",
    Timeout: 300*time.Millisecond, Retry: 1,
    Degrade: func(ctx context.Context, err error) any { return cachedOrPlaceholder },
})
```

三个字段任一为零值即编译期不过（struct 无默认）+ CI AST 检查；**Target 只能是服务名，路径只能是 GET**；一次请求内对同一 target 至多 1 跳，不得出现「A→B→C」链式同步读（16.3 禁止 fan-out >1）。P1 的合法调用对只有 `finance → homeos` 一个方向；`Target` 写成未出生域时启动即失败（registry 里没有实建服务）。SDK 统一记录 `crossservice_call_seconds` 与降级次数，两侧都算（21.5）。

### 3.6 到期注册的跨服务形态（16.4、14.5 第 4 项）

不在请求链路上调 HomeOS 写接口，全部异步：

```
svc-finance: 写自己的实体（含 due_at）→ 同事务 outbox → finance.due.registered
svc-homeos:  消费并按 (source_system, source_id, kind) upsert homeos_due_registration
触发器:      svc-homeos 内 30s 扫描循环 + advisory lock（多实例安全）→ homeos.reminder.fired
回写:        完成/删除 → homeos.todo.completed → svc-finance 消费并更新自己实体；失败进死信并计入对账
撤销:        业务对象软删 → finance.due.revoked → homeos 置 expired
```

P1 落地的四行注册来源：`finance.Bill.due_at`、`finance.Budget` 超支（事件驱动非定时）、`finance.Goal.deadline`（M2）、`finance.Loan→RepaymentPlan.due_at`（M2）——见 16.4 的「随出生期落地」注。**不在 P1 建空注册方**：其余五面的注册行不预插，日历聚合与触发逻辑不为「将来的面」写分支。

后果：注册到日历可见的延迟 = outbox 500ms + 消费 ≈ P95 5 秒内，验收按收敛时间测（18.1 最终一致性口径），不承诺「写完立刻上日历」。业务面**不得**插入 `homeos_calendar_event`/`homeos_todo`/`homeos_reminder`，也**不得**自建定时器与推送链路（16.4）。

---

## 四、鉴权：一份实现，当期两处调用（15.6）

### 4.1 token

JWT access（15 分钟）+ refresh（30 天轮换、落库可撤销），claims：

```
{ sub: account_id, fid: 当前会话家庭, role: owner|member|ward|guest, pver: 权限版本, jti: 会话 id }
```

`svc-homeos` 是唯一签发方与 RS256 私钥持有者；业务面服务只带公钥（启动时从 `/api/homeos/.well-known/jwks.json` 拉取并定时轮换）。W0 阶段验证码用「固定测试验证码 + 站内信桩」，商用短信通道不做（14.6）。切换家庭 = 重签发一对 token，客户端重建上下文（14.5 第 2 项验收 ≤1 秒）。`pver` 是家庭级权限版本，任何权限/成员变更即 +1。

### 4.2 SDK 与判定

`packages/authz` 提供 `Verify(token)` 与 `Can(ctx, scope, resource, action, obj)`，当期两个服务在中间件的同一位置调用（22.2 第 5 条）：

- 判定顺序严格按 15.2：显式拒绝 > 角色默认矩阵 > 家庭级覆盖 > 对象级 ACL。15.3 默认矩阵内置在 `authz/policy.go`，**七行全部内置**（含未出生系统行）；家庭级覆盖从 `GET /api/homeos/permissions/snapshot?fid=` 取，带 `If-None-Match: pver`，绝大多数请求命中 304。
- 进程内缓存 TTL 60 秒（按 `(account_id, fid)`），并订阅 `homeos.permission.updated` 立即失效——这是「≤1 秒生效」的服务端实现，不靠 TTL 碰运气。
- 字段级可见性（15.4）在 **DTO 序列化层**实现，不在 SQL 层过滤：L3 字段不进默认导出与埋点（20.2）只有在 DTO 层才拦得住。财务的「私密标记」行级可见性即在此层实现（15.3 财务隐私默认）。
- **降级**：SDK 取不到快照（homeos 不可达）→ **拒绝写、允许读已缓存**，并落审计（14.5 第 3 项）。

### 4.3 测试即门禁（附加门禁②）

`test/authz/matrix_test.go` 按 15.3 的 **7 系统 × 5 角色逐格生成断言**，表格与用例同源，避免文档改了测试没改。P1 的打法：

- `homeos` 与 `finance` 两行：每格打**两个服务各一次**，证明判定不是只有 homeos 实现；
- 其余五行：按「未启用」判定——请求经 Nginx 返回未启用、直连服务不存在、事件目录有条目但无生产者，三种形态都要断言（15.3 的「逐格断言与本表的关系」段）；
- 越权 100 次全拒并落审计（18.2 附加门禁、14.5 第 3 项）。

**每个面在自己出生期末复验它那一行**，判据不因 P1 已过而豁免（11.9、18.2 出生期复验条）。

---

## 五、同步底座（14.5 第 5 项、3.4.4、21.6）

- **幂等**：所有写接口要求 `client_request_id`，`{code}_idempotency(key, family_id, request_hash, response_snapshot, created_at)` 唯一索引，重放返回首次响应而非重复执行。
- **乐观锁**：实体带 `version`；写请求携带读取时的 version，不匹配返回 **409 + 双版本 payload**（server/client 两份），绝不静默覆盖。冲突只在服务内判定，不存在跨服务锁。
- **变更日志**：业务写操作在事务内向 `{code}_change_log(lsn bigserial, family_id, entity, entity_id, op, version)` 追加，由 `packages/sync` 的 repo 基类完成，业务代码不记得写。
- **delta**：`GET /api/{code}/sync/changes?family_id&since_lsn&limit`，**每服务一条游标**；P1 客户端持有两条游标（`homeos`、`finance`），storage 里按 `(family_id, service)` 存 LSN，切家庭与切服务互不干扰。游标结构按七域设计，但**不为未出生服务预建桶**——桶在订阅到该域接口时才出现。
- **状态机单向**：`draft → pending → synced`，冲突分支 `conflict` 必须由用户处置（21.6）。
- 客户端载体：`storage` 中的 `pending_ops` 队列 + 自研重放器（网络恢复 / 前后台切换 / 定时三触发），按 key 串行去重；队列按服务分桶，某服务不可用时其它桶照常重放。

验收样本是 18.2#3 的双设备并发写 200 次，脚本以两个虚拟客户端身份跑，不依赖真机。

---

## 六、搜索与投影（14.5 第 7 项、16.3、17.6）

- **索引唯一持有方是 `svc-homeos`**：它订阅当期在运流的可索引对象事件，写 `homeos_search_index(ref_domain, ref_id, family_id, title, keywords tsvector, updated_at)`。客户端只调 `GET /api/homeos/search` 一个接口，**不在前端并行打多个服务拼装结果**（17.7 第 4、5 条）。
- 索引的 `ref_domain` 取值域按 registry 的七域定义，**P1 实际只有 `homeos` 与 `finance` 两个写入来源**；未出生面的查询返回空并提示「未启用」而非报错（14.5 第 7 项验收）。
- 中文分词在应用层做 **bigram 切分**后写入 `simple` 配置的 tsvector，不引 `zhparser`（换分词器要改 Postgres 镜像，属 22.1 第 2 条评审项）。P1 的样本库 = 6 万流水 + HomeOS 三对象，按 21.1 上限 ×1.5 构造，判 P95 ≤500ms（端到端口径，含事件传播与投影写入）；**跨六面命中 ≥45/50 在 P6 判定**（18.2#4）。
- L3 内容不进索引（20.1）；L2 只索引脱敏字段——这两条写在**索引写入器的字段白名单**里，不是调用方自觉。
- **高频跨面渲染走本地投影**：`{code}_proj_{src}` 只允许本服务订阅器写入，可随时全量重建（重建脚本随 M1 交付，否则投影表会变成第二数据源）。P1 有两张：`finance_proj_homeos`（成员/权限快照）与 **`homeos_proj_finance`（按月聚合的收支与预算剩余快照，供 `home/summary` 的 `finance.headline` 与角标计算——首页格子不得为了渲染而去同步调 `svc-finance`）**，其余投影表随 P2-P6 各面出生。
- 动态流与搜索共用 `{code}_change_log`，避免两套变更捕获。`finance.*` 事件写 `homeos_dynamic` 时标注 `on_behalf_of`（代管场景，14.5 第 2 项）。
- **每日对账**（18.6 的 P1 部分）：投影 vs 来源逐表比行数与抽样字段；孤儿逻辑引用（存了 id 但对方无此行）进对账报表并告警；`homeos_due_registration` 与财务来源对象的存在性也在这条对账里。这是无物理外键的必然配套，不是可选优化，**每个出生期末为阻断项**。

---

## 七、通知下发（14.5 第 4 项，P1 只做到站内信可测）

链路：svc-homeos 触发 → `homeos_notification`（站内信，落库即算投递成功）→ `packages/adapter/push` 异步投递。

```
adapter/push/
├── provider.go        // interface: Send(ctx, Device, Msg) (Receipt, error)
├── stub.go            // P1-M1 默认实现：写日志 + 返回伪造回执，保证链路可测
└── (apns.go / vendor.go 在 P1 末附加门禁③凭证与 19.3 选型定版后新增，不改接口)
```

`homeos_push_device`（token 轮换需 upsert）、`homeos_notification_delivery`（每次投递一行，`dedupe_key` 唯一索引去重）、回执埋点按 19.5 字典。18.2#2 的服务端触发成功率 ≥99.9% **在 P1-M1 以站内信链路测得**，真实通道接入后同口径复测（18.2 P1 末附加门禁③）——这是门禁延后拍板的直接技术后果，不新增指标。**各面不自建推送链路**（16.4、17.7 第 1 条）。

**通知分型与未读（3.6、11.3 第 ④ 项，本轮自 family-ledger 对照补入 P1）**：`homeos_notification.type` 是受控枚举 `budget_alert` / `system` / `reminder`，新增取值须同时改事件目录与客户端文案库；未读口径唯一为 `read_at IS NULL`，`GET /api/homeos/notifications` 按 `type` 返回分组与未读计数，D 行角标即该计数。**L3 内容不进 `content`**（二十章），分型只作用于渲染与路由，不作用于投递优先级。

---

## 八、财务面落地拆法（M1 闭环域 + M2 深度域）

### 8.1 M1 段：单人闭环（14.5 第 10 项）

| 对象 | 表 | 关键实现点 |
|---|---|---|
| 账本（报表分组） | `finance_ledger` | 只是标签 + 成员范围 + 时间窗，**不是数据隔离域、不做账本级权限边界**（4.5.1、15.3） |
| 账户 | `finance_account` | 预置 + 自定义，余额为派生值：按 `amount_cents` 累计，**不做增量计数器**（并发写口径 18.3#2）；M1 建列 `is_archived`/`archived_at`，**归档前余额非 0 一律拒绝**（4.5.9、18.3#10），归档只移出默认账户位与汇总，历史流水与净资产照计 |
| 分类 | `finance_category` | `family_id` 可空 = 系统预置字典 + 家庭自定义；二级；图标集名、禁 emoji；跨面唯一费用字典（16.2）；M1 建列 `is_active`/`sort_order`/`version`，**停用不影响历史流水的显示与统计**，排序由记账选择器/报表图例/筛选 chips 三处共用（4.5.9、18.3#10） |
| 流水 | `finance_transaction` | `type` 收支转账三类同一张表，`transfer_group_id` 成对；分区与索引按 21.2 |
| 预算 | `finance_budget` + `finance_budget_period` | 月度 + 分类 + 固定/周期，超支走 `finance.budget.exceeded` 事件，**不自建定时器** |
| 账单 | `finance_bill` | 新建对象，`due_at` 经 16.4 注册；周期记账规则可作语义基线但账单是新建（4.5.5） |
| 附件 | `finance_attachment` | 单机磁盘 `./uploads/{family_id}/`，限额 ≤3 张/笔、≤5MB/张、jpg/png/webp（3.7） |
| 回收站 | 软删列 + `deleted_at` 索引 | 30 天可恢复，**M1 必做**（4.5.1） |

接口按 3.7 与 4.7 的 M1 行交付，**导出必须三参数全生效**（`type/days/format`，4.5.3 硬约束，18.3#7 的回归防护项）。录入侧 AI：小票 OCR 与语音记账随本期交付，供应商按 19.3「随面定版」在 M1 开工前定版；识别失败必须 100% 回落手输，手工路径同批次（11.3 AI 交付期口径）。

**时间窗与统计口径全部在服务端（18.3#9 是本轮新增的判定条）**：财务与首页的读接口统一接受 `period`（`YYYY-MM`，家庭时区口径），流水/报表/预算/首页格子四处读的是 shell 持有的同一个值；环比、累计余额、月度合计由预聚合表按 `period` 计算后下发，**上期为空返回「无对比期」标识而不是 0 或除零**，客户端不得自行逐期累加（该条在 18.3#9 里以「前端二次累加检查为 0」判定）。首页 `home/summary` 的 `finance.headline` 即取这套服务端结果，与 `finance/asset`（M2 净资产）同源（4.5.8、17.2）。

### 8.2 M2 段：深度域（14.5 第 11 项）

借还款与还款计划（`finance_loan` + `finance_repayment_plan`）、储蓄目标（`finance_goal`）、AA 分账与结算中心（`finance_split` + `finance_settlement`）、资产负债与净资产（`finance_liability` + 报表聚合）+ 多币种、发票与报销、报表增强、信用卡。四条实现纪律：

1. **AA 零误差**（18.3#6）：以「分」为整数单位，`Σ各参与人净额 = 0` 且 `Σ分摊 = 原始金额`，余数分配规则固定且可复现（按成员 id 排序取前 N 名各 +1 分），服务内用不变量断言而不是事后校对。
2. **净资产依赖负债对象先补齐**（4.5.4）：报表口径不得为净资产单独存一份金额副本。
3. **多币种**：汇率源属 M2 依赖（19.1），每日更新一次，记账按当日汇率、可手改，降级用上次有效汇率并标注更新时间。
4. **深度域的到期注册同批接通**：还款计划每期前 3 天、目标截止前 30 天（16.4），不在 M2 之后补。

### 8.3 财务面 demo 表：跨服务五步（附加门禁①）

M1 期间 `svc-finance` 交付一张 demo 表 + 五个接口，用途是**把跨服务链路走通**，不是业务功能：

| 步 | 动作 | 证明的跨服务能力 |
|---|---|---|
| 1 注册 | 本服务写 `finance_demo_item` 并发出 `finance.due.registered` | outbox → JetStream → svc-homeos 消费 → 注册表幂等落库 |
| 2 写 | `POST /api/finance/demo-items`（带 `client_request_id`） | 本服务内幂等 + 乐观锁 + `version` |
| 3 只读 | svc-homeos 首页**财务格子的 `headline`** 取财务 demo（只读接口） | `rclient` 三要素、超时降级、鉴权 SDK 跨服务生效 |
| 4 事件 | `finance.demo_item.created` 被 homeos 消费进索引与动态 | 目录校验、durable consumer、去重表 |
| 5 提醒 | 到点触发 → `homeos.todo.completed` 回写财务 | 事件驱动触发 + 异步回写 + 死信兜底 |

**判据看端到端**：第 1 步到日历可见的收敛时间、第 3 步的降级正确性、第 5 步的回写到达都要有埋点数字，不是「接口返回 200」。五步全绿即 M1 附加门禁①；**同一判据在 P2-P6 每个面出生期末复验一次**（18.2 出生期复验条、11.9），所以这套 demo 是模板而不是 P1 的临时把戏。

demo 表不进 16.2 权威表；`migrations/finance/` 里单独一个 demo 目录，**P1-M2 首张任务卡即删除 demo 表与接口**（真实财务对象已具备同等链路能力），门禁 2 在此期间对 `finance_demo_item` 收紧白名单。

---

## 九、客户端 shell 与管理台（14.5 第 8 项、十七章）

> 页面清单、路由与分包、深链映射、步数预算与交互流的完整规格在 `docs/p1-page-structure-navigation.md`，本章只写服务端要为它兑现什么。

- **单一 base URL**：`https://{host}/api/{code}/*`，由 Nginx 前缀反代；客户端不持有各服务地址、不做服务发现（17.7 第 5 条）。
- **路由模型**：页面全部为三段式独立路由 `pages/{code}/{feature}/{action}`，**不注册原生 `tabBar`**（一级五项与六面 chip 由 shell 自定义组件渲染，一级页无栈互切、二级页压栈），破坏性动作走统一弹层不成页（17.7 第 6、7 条）。`pages.json` 的 CI 三查见导航文档 2.4。
- **未出生面显示「即将上线」占位、不可进入，不建空分包**（11.3 第 ⑧ 项、17.7 第 2 条）；chip 与格子顺序固定为财务/采购/饮食/出行/家人/成长，不随出生期变化（17.1）。
- **首页六面矩阵（2×3，六格恒在）是首页主视觉**：P1 呈现 1 个可用格 + 5 个描边占位格，**原「卡片摘要位只渲染已出生面」的口径已由 17.2 取代**。四区（家庭/今日/矩阵/动态）由 `GET /api/homeos/home/summary?period=` **一个请求**返回，`faces[]` 恒为六项且顺序恒定，每项带 `availability`（`available`/`unavailable`/`not_born`/`disabled`）、`headline`、`badge`、`as_of`；**状态句与累计值一律由服务端算出**（前端二次累加在 18.3#9 里判 0），聚合走投影与只读接口，**不在前端并行打七个服务拼装**（17.2、17.7 第 4 条）。
- **`period` 是 shell 级时间窗**：首页今日区与财务格子、流水、报表、预算读同一值，四处一致是 18.3#9 的判定项；环比缺上期返回「无对比期」而非 0 或除零。
- 分包严格按 `pages/{code}`，主包承载 shell 与 HomeOS 全部页面（导航、六面矩阵、＋、深链、状态组件、格式化、埋点 SDK，17.7 第 8 条）；`pages.json` 由 CI 校验（门禁 4 的前端项），**出现未出生面的分包即失败**。
- 全局「＋」是**独立路由页** `pages/homeos/quick-add/index`（17.4），不是 action sheet：按当前会话家庭与权限过滤可建对象，无权限项**不显示**而非置灰；目标面手选在 P1-P5 只列已出生面，手选路径 ≤2 步以保「记账 ≤3 步」（4.9）；未提交草稿不落任何业务对象。
- 深链 `homecube://{code}/{entity}/{id}`；未出生面的深链返回「未启用」；进入 L3 对象触发会话内二次确认（17.6、20.2）。
- 离线：核心写走 `pending_ops`；跨家庭切换、邀请、权限修改、AI 录入离线不可用（21.6），四类入口在 shell 层统一禁用，不各自判断。
- 某面服务不可用时该面入口显示不可用态、其余面照常可用；依赖它的投影显示最后一次值并标注时效（12.3）。
- i18n：文案不硬编码、语言资源按 `code` 分文件，**英文本地化本身在 P6 末统一交付**（12.4）。
- 图标统一图标集，`check-no-emoji` 进 CI（14.7）。
- **`svc-homeos` 要为六面矩阵兑现的契约**：`home/summary` 的 `faces[]` 由 registry 域表 + 出生期配置 + `feature_flags` 三源合成，**恒返回六项、顺序恒定**；`availability` 四值判定在服务端完成（客户端不得自行推断某面是否出生），`unavailable` 时带最后一次投影值与 `as_of`（12.3）。新增一面在服务端只改 registry 与出生期配置，**不改这个接口的形状**——这是 17.2「格子集合恒定」在契约层的对应物。
- **设备锁 PIN（3.4.8、11.3 第 ⑪ 项）**：PIN 散列与比对全部在客户端本地，**服务端不存 PIN、不下发任何可离线校验的凭据**；服务端只提供「忘记口令 → 手机号验证码重验 → 允许重设 PIN」这条链路（复用 3.1 身份域的验证码接口，不新增认证因子）。L3 字段的解密仍按二十章在服务端按权限判定，PIN 只是本地解锁载体，**不构成新的授权层**。
- **启动静默检查更新**：`GET /api/homeos/app/version` 只读、无鉴权门槛、可被 Nginx 侧缓存；返回当前版本与更新说明，客户端静默比对、可跳过更新引导，**检查失败不阻断首屏**（3.4.8）。P1 期间该接口只需返回静态配置，出包与灰度通道属 P1 末附加门禁③。

`admin/`（Vite + React）P1 范围五项：家庭与成员查询、权限矩阵断言结果、**按服务**的死信查看/重放、审计与最小指标看板、对账报表入口。不做业务功能，不做用户侧配置。

---

## 十、部署、备份与 CI（14.5 第 1、9、12 项，22.4、21.3、22.5）

### 10.1 Compose 与一条命令

`deploy/` 内 compose：`nginx` + `nats`(JetStream) + `postgres16` + `svc-homeos` + `svc-finance` + `web`/`admin` 静态容器 + 一次性 `migrate` 初始化容器（按当期序列逐服务重放后退出）。

```
make up          # 干净机器 → 可用环境：建当期 schema 与账号 → 迁移 → 种子 → 健康检查（≤30 分钟，附加门禁③）
make seed-perf   # 按 21.1 上限 ×1.5 构造压测样本（6 万流水 + HomeOS 三对象，CI 缓存）
make backup      # 当期各 schema 一份 pg_dump -Fd + NATS 流快照 + uploads tar → 加密落 deploy/data/backups
make restore     # 恢复到指定备份点：整集群 → 逐服务重放迁移 → 起当期服务与消费组（演练入口）
make rollback    # 只切回上一版本镜像 tag，校验备份完整性并打印，不自动动数据
```

- RPO ≤15 分钟靠 **WAL 连续归档**（`archive_command` 每分钟搬运一段），全量每日 1 次 + 每月留存 12 份（21.3）。
- **不允许只恢复单个 schema**：跨面逻辑引用会立刻失配（21.3 恢复顺序行）。
- M1 必须完成**首次真实恢复演练**（含全栈 Compose 重建）并记录耗时——14.5 第 9 项的门禁。
- 回滚刻意保持薄：数据库向后兼容由迁移纪律保证，不执行 down 迁移；**部署包上传与目标机执行由人手动完成**，脚本不做远程编排。
- 健康检查：每服务 `/healthz` 报告「本 schema 可连 + JetStream 已连」，`/metrics` 带常量标签 `code`（22.2 第 10 条）；当期两个服务都要可拉，每新增一个服务即在期末复验这一条（14.5 第 9 项）。

### 10.2 CI 五道门禁（22.5）

| # | 门禁 | 实现 | 失败即 |
|---|---|---|---|
| 1 | 迁移 | 逐服务在空库重放自己那条序列 + 版本表与代码声明一致 + 迁移文件只触碰本 schema | 阻断合并 |
| 2 | 数据归属 | 新增表名与 `TableName()` 比对 16.2 白名单；表前缀与所在服务一致；**未出生域的表/迁移目录/分包出现即失败** | 阻断合并 |
| 3 | 验收指标 | 18.2 与 18.3 各条自动化用例（并发写 200、重放 1000×3、越权 100 次、搜索样本 P95、收敛时间、AA 两项等式）产出报告 | 阻断灰度/发布 |
| 4 | 分域与鉴权 | 路由前缀 AST、handler 内裸 SQL 与自写判定分支禁用（必须经 authz SDK）、业务表 `version`+软删、跨 schema SQL / 跨服务 import / 请求链路同步写调用禁用、`rclient` 三要素齐备、消费者幂等键存在、`pages.json` 分包命名、`check-no-emoji` | 阻断合并 |
| 5 | 契约 | `contracts/openapi/*` 与 `contracts/events/*` 向后兼容 diff；发布未登记事件、引用不存在的服务、破坏性变更未升 version | 阻断合并 |

当期只有一个业务服务，但**五道门禁从 M1 起全部接驳且规则按七域生效**：P2-P6 每新增一个服务，门禁清单不变、覆盖范围自然扩大（11.9 的「服务随面出生的重复验证风险」的处置方式）。按服务并行：某服务的 lint/test/build 只在该服务目录变更时跑。

### 10.3 可观测与合规素材（14.5 第 9 项）

- 指标最小集（全部带 `family_id` 与 `code`，当期各服务分别出图）：接口 P95/P99、错误率、`/healthz` 存活、outbox 积压、死信数、事件端到端收敛时间、跨服务调用耗时与降级次数、同步冲突数、投递回执率、依赖调用量（未定版项为 0，OCR/ASR 有真实调用量）。告警阈值写进 `deploy/alerts.yml`（21.5）。
- 审计事件按 21.5 十类落 `homeos_audit_log`，财务经 `homeos.audit.recorded` 事件上报；连续 5 次越权通知管理员。
- **合规素材底稿四项**：L2/L3 字段清单（由 20.1 表生成）、埋点字典（19.5）、保留期表（21.4）、第三方 SDK 与数据出境清单（P1 含 OCR/ASR 两家，其余为空表 + `adapter/` 接口清单）。P1 末附加门禁①只判文本定版，不判素材收集（11.9）。

---

## 十一、P1 排期与拆卡

**M1 = 26-31 周（13-15 个 Sprint）**：底座段 10-13 周（S1-S6）在前，财务闭环段 16-18 周（S7-S14）在后，两段同期串行但**底座段不含业务功能、财务段不改底座主干**。
**M2 = 10-12 周（S15-S19）**。人类验收切片上限 = **当期 1 面 × 每面 2 个功能域**（11.7 第 4 条），超出即顺延，不许压缩验收。

| Sprint | 目标 | 关键卡 | 出口判据 |
|---|---|---|---|
| S1（2 周） | 骨架可跑 | registry 七域登记 + 实建 2 服务 + Nginx 前缀反代（未出生 5 前缀返回未启用）+ 2 schema/2 账号 + 两条迁移序列与工具链 + Compose + 门禁 1/2/4 接驳 + Jarvis Workbench 建 HomeCube 项目与任务模板 | `make up` 干净机器 ≤30 分钟；`svc-homeos` 单独重启不影响 `svc-finance`；门禁 1/2/4 全绿 |
| S2（2 周） | 总线与契约 | 两条源流 + 归档流 + 死信流 + outbox 投递器 + durable consumer 框架 + 去重表 + 七域事件目录（含 10.3 十一条与启用期字段）+ 契约 diff + 门禁 5 接驳 | 重放 1000 事件 ×3 不产生重复对象；服务停 5 分钟事件不丢；未登记事件 CI 拒绝 |
| S3（2 周） | 身份 | 账号/验证码（测试码 + 站内信桩）、家庭、成员、邀请码/链接/二维码三态、多家庭列表与切换（重签发 token）、JWKS 分发 | 3.4.1 场景走查；切换后上下文重建 ≤1 秒 |
| S4（2 周） | 鉴权 SDK | `Permission` 表 + `authz/policy.go` 内置七行矩阵 + 两服务中间件接入 + `pver` 与 `permission.updated` 失效 + 字段级可见性落在 DTO 层 + 降级策略 + 逐格断言用例 | **附加门禁②通过**；权限变更 ≤1 秒跨服务生效；越权 100 次全拒并落审计 |
| S5（2 周） | 到期与通知 | 三对象 + `homeos_due_registration` 与回调表 + 16.4 三事件（registered/revoked/todo.completed）+ 30s 扫描触发 + 站内信（**含 `type` 三枚举 `budget_alert`/`system`/`reminder` 与未读计数接口**）+ push adapter/stub + `dedupe_key` 与回执埋点 + **`home/summary` 的 `faces[]` 合成（registry × 出生期 × `feature_flags`，恒六项）** | 财务四类对象可注册可触发；收敛 P95 ≤5s；18.2#2 ≥99.9%（站内信口径）；回写失败进死信且计入对账；**`faces[]` 六项齐、顺序对、`availability` 四值各有样例** |
| S6（2 周） | 同步与检索 | 两份 `change_log`/幂等表 + delta 接口 + `version` 乐观锁与 409 双版本 + 客户端 per-`(family_id, service)` 队列与重放 + `homeos_search_index` + bigram + 投影写入器与重建脚本 + 每日对账 | 18.2#3、#4（当期口径）通过；游标互不干扰；对账报表可出且零孤儿引用；**底座段收口 = 14.5 第 1-9 项 + 附加门禁②③** |
| S7-S8 | 财务主干 | `finance_account`/`category`/`transaction`/`ledger` 四表 + 记账写路径（幂等、乐观锁、软删）+ 流水筛选与游标分页 + 单条查询 + 回收站 + **`is_active`/`sort_order`/`is_archived`/`archived_at` 四列与停用·排序·归档·恢复四个接口** | 18.3#1 记账 ≤3 步 ≤5s；18.3#8 跨家庭越权 100 次全拒；**18.3#10 五项断言全过（含余额非 0 归档被拒）** |
| S9-S10 | 余额与统计 | 余额派生计算 + 统计四视图（概览/趋势/分类/成员）+ 预聚合表 + 分区与索引（21.2）+ **`period` 参数贯通首页/流水/报表/预算四处 + 环比（缺上期返回「无对比期」）+ 累计余额服务端逐期累加** | 18.3#2 ≤300ms（1000 条）与 6 万条 ≤500ms；18.3#3 报表 ≤1s；**18.3#9 四处时间窗一致率 100%、抽样 30 格逐值一致、前端二次累加为 0** |
| S11-S12 | 预算与账单 | `finance_budget` + 周期记账 + 超支事件 + `finance_bill` 注册到期中心 + 票据附件（单机磁盘 + adapter/storage） | 18.3#4 漏报 0 误报 0、回退不重复提醒；账单可注册可触发 |
| S13 | 导出与录入 AI | 导出三参数全生效 + 小票 OCR + 语音记账 + 手工降级同批（19.3 随面定版已在前完成） | 18.3#7 行数一致无缺列无乱码 + 三参数回归防护；OCR/语音失败可 100% 手输完成同一笔账 |
| S14（1-3 周，缓冲） | **M1 收口** | **财务面跨服务五步 demo** + 前端 shell 收口验收（D 行五项 + B 行六面 chip + **首页六面矩阵** + ＋ 独立路由页 + `pages/finance` 分包 + 未出生占位 + **CI 三查：无 `tabBar`／无未出生面 root／路由全三段式**）+ `GET /api/homeos/app/version` + 设备锁「忘记口令重验」链路 + admin 五功能 + 备份首次恢复演练 + 合规素材四项 + S1 单人路径走通 S6 财务段 + 14.5 第 1-10 项验收报告 | **附加门禁①③通过**；**首页六格恒在（1 实心 + 5 描边）**；已建路由数与导航文档一致（主包 29、财务 29）；M1 验收报告提交人类；**M1 未过不得开工 M2** |
| S15-S16 | 借贷与目标 | `finance_loan` + `finance_repayment_plan` + `finance_goal` + 两者接入到期中心 + 还款登记回写 | 还款计划每期前 3 天可触发；目标截止前 30 天可触发；回写收敛实测 |
| S17-S18 | 分账与净资产 | `finance_split`/`settlement` + AA 余数规则 + 负债对象 + 净资产与资产负债报表 + 多币种 + 信用卡 + 发票与报销 | 18.3#6 两项等式全成立、余数可复现；**18.3 全部 10 条达标（M1 的九条全部重跑，不豁免复测）** |
| S19（1-2 周，缓冲） | **M2 收口与 P1 末门禁** | 删除 demo 表与接口、`svc-finance` 契约冻结、功能覆盖走查对照主流记账 App、P1 末附加门禁三项（合规文本、依赖选型整表、推送凭证与出包）+ P1 验收报告 | **4.9 全部达标 + 覆盖走查 ≥80%（18.6）+ P1 末三项门禁通过**；通过才对外发布，并作为 P2 开工前置 |

**任务卡**按 22.3 建在 Jarvis Workbench 的 HomeCube 项目下，每卡必填「归属服务」「归属数据源（16.2）」「外部依赖（19.1，无则填『无』）」，生效前走强制人工审核。**M1 与 M2 的卡不得混排在同一期开工**（11.7 第 2 条）。

---

## 十二、风险与本方案刻意保守之处

1. **缺陷形态**：跨服务逻辑的 bug 从「写错」变成「时序与补偿错」。所有跨服务判据一律写成收敛时间与死信处置，不写「调用成功」；对账从 S6 起是常驻卡而非一次性卡。
2. **P1 是双段切片，最大风险是段间返工**：财务段（S7-S14）会用到底座段（S1-S6）的鉴权、到期、同步、搜索四块，任何一块口径在财务段被推翻都要重做底座。处置：S6 末的底座段收口即**冻结底座契约**（22.2 第 7 条），财务段发现缺口一律走 22.1 第 2 条偏离评审，不允许在财务段顺手改底座。
3. **OCR/ASR 提前进 P1**：依赖选型的两级定版（19.3）意味着 M1 开工前必须定这两家供应商，是全案唯一在 P1 就要签的真实外部依赖。处置：先定版、再排 S13；未定版期间代码只允许出现在 `adapter/ocr`、`adapter/asr` 接口后，业务域不感知供应商。
4. **单机资源**：8 个容器在家庭自托管机器上的占用是真实风险，S1 末即实测并回填 21.1（当期阈值，不是七服务阈值）。
5. **NATS 单节点 `R=1`**：流不跨节点复制，磁盘故障可能丢掉「已 ack 但未归档」的窗口。缓解：outbox 在 Postgres 侧（有 WAL 归档）是发布方的权威留存，归档流进每日备份范围；不做 JetStream 集群（14.6）。
6. **发布顺序耦合**：SDK 与事件目录由 homeos 服务提供，其契约破坏性变更会同时打断财务面。处置：契约门禁 5 + 语义化版本 + 旧 subject 保留一个期 + homeos 先行发布。
7. **pver 变更风暴**：一次批量权限调整会让所有服务缓存失效并回源拉快照。缓解：快照接口按 `fid` 聚合（一次拉全量而非逐成员），回源带抖动与 singleflight。
8. **bigram 召回上限**：当期只判 P95 ≤500ms，跨六面命中在 P6 判；若届时命中 <45/50 才评审 `zhparser`（需换镜像，走 22.1 第 2 条）。
9. **demo 表变影子数据源**：S14 建、S19 删，中间若忘了删就会长期成为第二数据源。处置：S19 首张卡即删除，门禁 2 对 `finance_demo_item` 收紧白名单，且 P2 出生期复验五步时用真实采购对象，不再新建 demo 表。
10. **墙钟串行**：P1 的 26-31 + 10-12 周是后续五期的前置，任何滑期整段后移（11.9 墙钟风险）。处置：M1/M2 两个验收点让底座缺陷最迟在 S6 末暴露，S14 与 S19 各留 1-3 周缓冲，且**缓冲只用于顺延，不用于压缩完整度**。

---

## 十三、联调与环境

- 本地：`make up` 起全栈，`make dev-{code}` 把某服务跑出容器外调试（`dev-homeos` / `dev-finance`），端口与 env 在 `deploy/env.local.example`。
- 契约测试：`test/contract/` 用事件目录与 OpenAPI 做双端断言——提供方改动若破坏消费方，CI 在合并前失败，不靠联调期发现。
- 事件回放：`make replay?stream=HC_ARCHIVE&from=<时间>&subject=<前缀>`，仅运维口，回放走同一幂等键，不产生重复对象（18.2#5）。
- 时间旅行调试：所有事件信封带 `causation_id`/`correlation_id`（10.2），管理台死信详情页可反查链路。

---

## 十四、本方案与 PRD 的对应关系

| 本方案章节 | 回答的 PRD 条目 |
|---|---|
| 一、拓扑与工程结构 | 14.5 第 1 项、卷首第 12 项、22.2、22.4 |
| 二、数据边界 | 14.5 第 1 项、16.1、16.2、16.6、22.5 门禁 1/2 |
| 三、跨服务通信 | 14.5 第 6 项、3.4.6、10.1-10.4、16.3、16.4 |
| 四、鉴权 | 14.5 第 2、3 项、15.2-15.6、18.2 附加门禁② |
| 五、同步底座 | 14.5 第 5 项、3.4.4、21.6、18.2#3 |
| 六、搜索与投影 | 14.5 第 7 项、16.3、17.6、18.2#4、18.6 对账 |
| 七、通知下发 | 14.5 第 4 项、3.4.2、18.2#2、19.1 推送行 |
| 八、财务面落地 | 14.5 第 10、11、12 项、四章、4.9、18.3、18.2 附加门禁① |
| 九、客户端与管理台 | 14.5 第 8 项、十七章、12.4 |
| 十、部署与 CI | 14.5 第 1、9 项、21.3、21.5、22.4、22.5 |
| 十一、排期与拆卡 | 11.3 P1 两行、11.7、22.3 |
| 十四（本章） | 全局追溯 |
| 十五、定版口径 | PRD 未覆盖的技术分叉，属 P1 契约 |

---

## 十五、技术决定的定版口径

以下十四处是 P1 契约的一部分：底座段口径冻结至 **P1-M1 末**（22.2 第 7 条），财务面口径冻结至 **P1-M2 末**；变更须走 22.1 第 2 条偏离评审。标 ★ 的三处是「服务随面出生」模型带来的口径修订，与七服务目标形态并存而非替代。

| # | 决定 | 内容 | 已接受的代价 |
|---|---|---|---|
| A | 迁移序列 | 每服务一条独立序列 + 独立版本表（golang-migrate per-service），**序列数随出生期增加** | 无全局版本视图；跨服务变更靠契约测试而非排序保证 |
| B | 会话与权限载体 | JWT access(15min)+refresh(30d 轮换)，claims 带 `fid`/`role`/`pver`，RS256 + JWKS 分发 | 自管会话撤销表；纯 session 每请求一查被排除 |
| C | 中文检索 | 应用层 bigram + `simple` tsvector，不改 Postgres 镜像 | 召回弱于词典分词；命中不足阈值才评审 zhparser |
| D | 客户端离线队列 | `storage` 中 `pending_ops` + 自研重放器（三触发） | 容量受 storage 上限约束；本地 SQLite 需原生插件且 H5 不具备 |
| E ★ | 服务边界与出生时机 | 目标形态一面一服务 + homeos 底座服务共 7 个，不设网关服务；**当期只建 2 个**，其余五个只有 registry 条目，目录/schema/分包/流一律不预建，CI 按 registry 校验命名合法性并禁止提前出现 | P2-P6 每期都要重走一遍五道门禁与接入清单（11.9 的重复验证风险）；换来的是当期交付面唯一、骨架不空转 |
| F | 数据隔离 | 单集群 + 每服务一个 schema 与一个专属账号，账号权限即边界 | 无跨 schema JOIN；聚合读只能在 homeos 内做 |
| G | 总线载体 | NATS JetStream：每服务一条源流 + 归档流 + 死信流，`R=1`，流随服务出生 | 单节点无流复制；发布方权威留存靠 outbox |
| H | 一致性模型 | 无分布式事务；事务内 outbox + 消费端幂等表 + 每日对账 | 跨服务可见有 ≤5s 延迟；引用完整性靠对账而非约束 |
| I | 鉴权实现 | 单一 SDK（`packages/authz`）+ 各服务同一中间件位置，内置矩阵含七个系统行；不可达时拒写允读 | homeos 是鉴权面单点，需健康检查与缓存兜底 |
| J | 跨面读 | 三选一：只读 HTTP（≤300ms/≤1 跳/必带降级）／本地投影／逻辑 id | 数据有多份视图副本，必须可重建 + 对账 |
| K | 到期注册 | 一律事件异步（`{code}.due.registered` → homeos），回写走 `homeos.todo.completed` | 日历可见非实时；注册失败靠死信与对账发现 |
| L ★ | 同步游标与搜索归属 | per-service `{code}_change_log`，客户端游标 `(family_id, service)`，**桶随出生期增加，不预建**；索引只在 `svc-homeos`，`ref_domain` 取值域按七域定义但 P1 只有两个写入源 | 客户端要管随期增长的游标；无全局一致性点；未出生域搜索返回空且提示未启用 |
| M ★ | 验收与发布单位 | 每服务一镜像一发布线，homeos 先行，降级才允许多进程合并；**期末验收对象 = 当期那一个面**，18.2 附加门禁①②③在每个出生期末以同一判据复验，不因重复而豁免 | 发布次数随期增加；复验成本固定；灰度按家庭白名单不变 |
| N | 客户端导航载体 | **不注册 uni-app 原生 `tabBar`**：一级五项与六面 chip 由 shell 自定义组件渲染，一级页无栈互切、二级页压栈；页面全部三段式独立路由 `pages/{code}/{feature}/{action}`，破坏性动作走统一弹层不成页（17.7 第 6、7 条）；`pages.json` 的三查进 CI（导航文档 §2.4） | 放弃原生 TabBar 的系统级手势与渲染红利，切面全靠 JS；页面数随功能线性增长（财务面 42 条路由），必须靠成页判定表与封闭动词表约束，否则路由表会失控 |
