# 家立方 HomeCube · Wave 0 技术方案

> **文档性质**：研发交付物，不是需求文档。需求唯一权威源是 `docs/prd-homecube.md`（23.4）；本方案只回答「14.5 的九项交付在七服务拓扑下怎么落地」，不新增、不裁剪任何需求口径。与本方案冲突的需求表述以 PRD 为准；PRD 未覆盖的技术分叉在第十四章给出定版口径，属 Wave 0 契约。
>
> **范围**：Wave 0 = 七服务骨架 + 底座能力 + 六面接入框架，**10-13 周**（5-6 个 2 周 Sprint）。收口判定 = 14.5 各项验收 + 18.2 Wave 0 附加门禁三项（六面 Hello World 全通、15.3 权限矩阵逐格断言、干净机器一条命令拉起 ≤30 分钟）。
>
> **明确不做**（依 18.2/23.5 拍板结论与 14.6）：任何识别/录入类 AI；隐私政策与用户协议文本；依赖选型定版；真实推送凭证；规则引擎与跨面联动业务（W3）；Kubernetes 与服务网格；分布式事务；独立网关服务；Redis。Wave 0 期间一切外部服务以 `packages/adapter/*` 接口 + 本地实现或桩交付。

---

## 一、运行时拓扑与工程结构

### 1.1 拓扑（卷首第 12 项、22.2 第 1 条）

同机 Docker Compose 常驻 **13 个容器**：

```
                 ┌──────────────────────────────┐
   client ──────▶│ nginx  :443/80  前缀反代 + TLS │  客户端只对一个 base URL
                 └───────┬──────────────────────┘       (17.7 第 5 条)
      /api/homeos/    /api/finance/   …/api/{code}/
                 ┌───────┴──────────────────────┐
                 │ svc-homeos  svc-finance       │
                 │ svc-purchase svc-diet         │  7 个 Go 服务，各自一个镜像
                 │ svc-trip     svc-kin          │  一条发布线、一个健康探针
                 │ svc-growth                    │
                 └───────┬──────────────┬───────┘
                         │              │
              ┌──────────▼───┐   ┌──────▼───────────────┐
              │ postgres16   │   │ nats (JetStream)     │
              │ 7 个 schema   │   │ 7 条流 + 归档流 + 死信流│
              │ 7 个专属账号   │   └──────────────────────┘
              └──────────────┘
```

- **不设网关服务**：Nginx 只做前缀转发、TLS 与静态资源（`web`、`admin` 构建产物），**不做鉴权**；鉴权在每个服务的中间件里由同一 SDK 完成（22.2 第 8 条）。
- 发布顺序恒为 `svc-homeos` 先于六面（SDK 与事件目录的提供方先行），Compose 里 homeos 用 `condition: service_healthy` 前置。
- 单机资源门槛 4C/8G（21.1 新增行）；实测不足时走 11.9 第 ④ 项**同镜像多进程合并部署**降级，不改代码拓扑，且必须写进当期风险记录。

### 1.2 仓库布局（22.4）

```
HomeCube/
├── server/
│   ├── services/                      七个服务，每服务一个 cmd 与镜像
│   │   ├── svc-homeos/                handler/service/repo/model/dto + 本服务订阅器
│   │   └── svc-{finance,purchase,diet,trip,kin,growth}/
│   ├── packages/                      仅这三个约束：无业务语义、可被服务依赖、不得反向依赖服务
│   │   ├── authz/                     JWT 校验 + 权限判定 + pver 缓存（由 svc-homeos 维护语义，15.6）
│   │   ├── bus/                       outbox 投递器、JetStream 封装、durable consumer、死信
│   │   ├── rclient/                   跨服务只读客户端（超时/重试/降级三要素强制声明，14.7）
│   │   ├── sync/                      change_log 追加、delta 查询、幂等表基类
│   │   ├── proj/                      投影表写入器与重建
│   │   ├── obs/                       结构化日志、指标（常量标签 code）、审计上报
│   │   ├── registry/                  域表：code ↔ 服务名/schema/路由/subject/分包/迁移目录
│   │   └── adapter/                   外部服务边界：push/ocr/asr/map/storage/ai（W0 全为桩）
│   ├── contracts/                     跨服务契约，版本化：openapi/{code}.yaml、events/{code}.yaml
│   ├── migrations/                    七条序列：migrations/{code}/{code}_0007_desc.up.sql
│   └── test/                          门禁级集成测试（权限矩阵、并发写、重放、对账）
├── web/                               uni-app(Vue3)：六面 shell + pages/{code} 分包
├── admin/                             Vite + React：最小管理后台
├── deploy/                            compose、nginx.conf、备份/恢复/回滚、env 样例
└── .github/workflows/                 CI 五道门禁（22.5），本仓库自建，不共享 workflow
```

**依赖方向由 CI 静态检查（22.5 第 4 道）**：

- `services/* → packages/*`，允许；
- `packages/* → services/*`，禁止；
- `services/A → services/B` 的任何 import，禁止——这是唯一能在**编译期**证明服务边界存在的手段，比运行期约定强；
- 跨面调用只能出现两种形态：`rclient.Call`（只读）与 `bus` 订阅/发布。

### 1.3 命名一致性：一个 code，七处生效（16.1）

`packages/registry` 里一张域表是唯一真源，`registry.Domains()` 被服务启动、迁移工具、前端构建脚本与 CI 共用：

| 维度 | 取值 | 校验点 |
|---|---|---|
| 服务名 | `svc-{code}` | 镜像名、Compose 服务名、容器名 |
| schema | `{code}` | 连接串 `search_path={code}`、账号名 `hc_{code}` |
| 路由 | `/api/{code}/*` | Gin 路由组 AST 检查 |
| subject | `{code}.` | JetStream 流 subjects、事件目录 |
| 表前缀 | `{code}_` | GORM `TableName()`、迁移文件内容 |
| 前端分包 | `pages/{code}` | `pages.json` |
| 迁移序列 | `migrations/{code}/` + `{code}_{序号:04d}_` | golang-migrate 目录 |

任一处偏离即门禁 2/4 失败。「架构漂移」因此在合并前被拦住，而不是靠评审记住规则。

---

## 二、数据边界：单集群七 schema

### 2.1 账号即边界（22.2 第 2 条）

一个 `postgres16` 实例、七个 schema、七个角色：

```sql
CREATE SCHEMA finance;
CREATE ROLE hc_finance LOGIN PASSWORD '…';
GRANT USAGE ON SCHEMA finance TO hc_finance;
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO hc_finance;
REVOKE ALL ON SCHEMA public FROM hc_finance;
```

- 每个服务只用自己那串 DSN（`user=hc_finance dbname=homecube search_path=finance`），**跨 schema 在权限层就做不到**，不靠代码约定。
- 表名在 schema 内仍带 `{code}_` 前缀（16.1 的双保险）：一旦出现他域前缀的建表，门禁 2 立刻发现。
- 无任何跨 schema 视图、DBLink、扩展共享；`public` schema 不放业务对象。

### 2.2 迁移（14.5 第 1 项、16.6、22.5 第 1 道）

七条**独立序列**，golang-migrate 每服务一个实例、各自的版本表 `schema_migrations_{code}`（落在自己 schema 内）。CI 逐服务在空库重放自己那条序列，并校验代码声明的版本与迁移头部一致。

约定：
- 迁移只允许 `CREATE TABLE`/`ALTER` 本域前缀的表；出现他域前缀即门禁 2 失败。
- **不存在跨服务外键**（16.3）：引用他域对象只存 `id`，无物理约束，完整性由每日对账兜住。这条纪律让七条序列彻底解耦——发布不再需要跨域排序。
- 业务表必备列：`id uuid primary key`（UUIDv7，时间有序抑制索引膨胀）、`family_id uuid not null`、`version bigint not null default 1`、`created_at/updated_at`、`deleted_at/deleted_by`（14.7、22.2 第 9 条）。系统预置数据用可空 `family_id` 表达。
- 金额一律 `amount_cents bigint`；禁止浮点与 `numeric` 混用。
- 索引第一列固定 `family_id`。

### 2.3 每服务的底座侧表（同构命名，落各自 schema）

| 表 | 用途 | 为什么 per-service |
|---|---|---|
| `{code}_outbox` | 发布前落盘，投递器异步搬入 JetStream | 事务边界只在单库单 schema 内成立，跨服务无法同事务写总线 |
| `{code}_event_dedupe` | 消费幂等键 `(event_type, business_id)` | 去重是消费方自己的状态，不能共享别人的表 |
| `{code}_dead_letter` | 死信落库与人工重放 | 死信归属消费方（22.2 第 10 条） |
| `{code}_change_log` | 同步增量游标源 | HomeOS 不代管他服务变更（14.5 第 5 项） |
| `{code}_idempotency` | `client_request_id` 去重 | 同上 |
| `{code}_proj_{src}` | 他域数据的本地投影 | 投影只允许本服务订阅器写，可随时重建（16.3） |

`svc-homeos` 额外持有 `homeos_due_registration`、`homeos_notification*`、`homeos_search_index`、`homeos_event_archive`（冷存）等全局对象。

---

## 三、跨服务通信

### 3.1 JetStream 流规划（3.4.6、10.4）

| 流 | subjects | 保留 | 说明 |
|---|---|---|---|
| `HC_HOMEOS` … `HC_GROWTH`（7 条） | `{code}.>` | `max_age=90d`，单节点 `R=1` | 每服务只被授权 publish 自己那条流（`$JS.API.STREAM.PUBLISH.{code}.*` ACL），发他域 subject 直接失败 |
| `HC_ARCHIVE` | `arch.{code}.>` | `max_age=365d` | 归档消费者 `hc-archiver` 从 7 条流复制，实现 21.4「热 90 天 / 冷 1 年」；回放走这条流 |
| `HC_DL` | `dl.{code}.>` | `max_age=90d` | 重试超限后 nack+term，同时写 `{consumer}_dead_letter` 便于查询与重放 |

- subject 命名即事件名：`{code}.{object}.{action}`（10.1），小写点分，版本不进 subject 而进信封 `version` 字段；破坏性变更走 `{action}_v2` 新事件名并保留旧名一个 Wave（10.4）。
- 流不启用 workqueue 模式：一条事件常有多个消费者（如 `finance.bill.created` 被 homeos 与 kin 同时订阅），各消费者独立 durable consumer。

### 3.2 发布：事务内 outbox，提交后异步投递

```
业务事务: INSERT 业务行 + INSERT {code}_outbox(subject, envelope, status=pending)
投递器:   每 500ms 批量 100 行 → JetStream Publish（异步 ack）→ 成功置 sent，失败 attempts+1
崩溃恢复: 重启即扫 pending；attempts>10 告警（不丢，只是没送）
```

代价是**事件传播多 ≤1 秒的固有延迟**，因此 12.1 的最终一致性收敛口径 P95 ≤5s 在本实现下有余量。**明确不提供「发布即可见」语义**：任何要求同步可见的读都走自己 schema 或本地投影，不假装跨服务强一致。

### 3.3 消费：durable consumer + 幂等 + 退避 + 死信

- consumer 名 `{consumerCode}-{event_type}`（如 `homeos-finance-bill-created`），`filter_subject` 精确匹配，`ack_wait=30s`，`backoff=[1s,10s,60s]`，`max_deliver=4`。
- 进 handler 第一件事：`INSERT ON CONFLICT DO NOTHING INTO {code}_event_dedupe`；已存在即 ack 返回——重复投递不产生重复业务对象是**表约束保证**，不是代码纪律。
- 超限失败 → publish 到 `dl.{consumerCode}.{event_type}` + 写 `{code}_dead_letter`；管理台按服务查看并人工重放（重放即原样重新入队，走同一幂等键）。
- 每日清理 `event_dedupe` 中 7 天前的行，防表膨胀。

### 3.4 事件目录与契约（14.5 第 6 项、22.5 第 5 道）

`server/contracts/events/{code}.yaml` 登记：事件名、`version`、payload JSON Schema、生产者服务、消费者服务列表、幂等键构成。**未在目录登记的事件发布时 CI 失败**（投递前有本地校验，CI 有全量校验），订阅方声明的消费者服务若不存在同样失败。`openapi/{code}.yaml` 与事件目录一起做向后兼容 diff：删字段、改类型、改必填、改 subject 命名一律要求升 version。

### 3.5 跨服务只读调用：声明式客户端（16.3、14.7）

```go
rclient.Call(ctx, rclient.Request{
    Target: "homeos", Path: "/api/homeos/members/snapshot",
    Timeout: 300*time.Millisecond, Retry: 1,
    Degrade: func(ctx, err) any { return cachedOrPlaceholder },
})
```

三个字段任一为零值即编译期不过（struct 无默认）+ CI AST 检查；**Target 只能是服务名，路径只能是 GET**；一次请求内对同一 target 至多 1 跳，且不得出现「A→B→C」的链式同步读（16.3 禁止 fan-out >1）。SDK 内部统一记录 `crossservice_call_seconds` 与降级次数，两侧都算（21.5）。

### 3.6 到期注册的跨服务形态（16.4、14.5 第 4 项）

不在请求链路上调 HomeOS 写接口，全部异步：

```
业务服务:  写自己的实体（含 due_at）→ 同事务 outbox → {code}.due.registered
svc-homeos: 消费并按 (source_system, source_id, kind) upsert homeos_due_registration
触发器:    svc-homeos 内 30s 扫描循环 + advisory lock（多实例安全）→ homeos.reminder.fired
回写:      完成/删除 → homeos.todo.completed → 归属服务消费并更新自己实体；失败进死信并计入对账
撤销:      业务对象软删 → {code}.due.revoked → homeos 置 expired
```

后果：注册到日历聚合的可见延迟 = outbox 500ms + 消费 ≈ **P95 5 秒内**，W0 验收按收敛时间测（18.2#2 的触发成功率仍以站内信链路测得），不承诺「写完立刻上日历」。业务面**不得**插入这三张表，也**不得**自建定时器与推送链路（16.4）。

---

## 四、鉴权：一份实现，七处调用（15.6）

### 4.1 token

JWT access（15 分钟）+ refresh（30 天轮换、落库可撤销），claims：

```
{ sub: account_id, fid: 当前会话家庭, role: owner|member|ward|guest, pver: 权限版本, jti: 会话 id }
```

`svc-homeos` 是唯一签发方与 RS256 私钥持有者；其余六服务只带公钥（启动时从 `/api/homeos/.well-known/jwks.json` 拉取并定时轮换）。切换家庭 = 重签发一对 token，客户端重建上下文（≤1 秒）。`pver` 是家庭级权限版本，任何权限/成员变更即 +1。

### 4.2 SDK 与判定

`packages/authz` 提供 `Verify(token)` 与 `Can(ctx, scope, resource, action, obj)`，七个服务在中间件的同一位置调用（22.2 第 5 条）：

- 判定顺序严格按 15.2：显式拒绝 > 角色默认矩阵 > 家庭级覆盖 > 对象级 ACL。15.3 默认矩阵内置在 `authz/policy.go`，家庭级覆盖从 `GET /api/homeos/permissions/snapshot?fid=` 取，带 `If-None-Match: pver`，绝大多数请求命中 304。
- 进程内缓存 TTL 60 秒（按 `(account_id, fid)`），并订阅 `homeos.permission.updated` 立即失效——这就是「≤1 秒生效」的服务端实现，而不是靠 TTL 碰运气。
- 字段级可见性（15.4）在 **DTO 序列化层**实现，不在 SQL 层过滤：L3 字段不进默认导出与埋点的要求（20.2）只有在 DTO 层才拦得住。
- **降级**：SDK 取不到快照（homeos 不可达）→ **拒绝写、允许读已缓存**，并落审计（14.5 第 3 项）。

### 4.3 测试即门禁

15.3 是 7 系统 × 5 角色的表格，`test/authz/matrix_test.go` 逐格断言（每格一条「期望 allow/deny + 实际打到哪个服务」），表格与用例同源生成，避免文档改了测试没改。**每个业务面的格子都要经该面自己的服务打一次**，证明不是只有 homeos 实现了判定。这是 Wave 0 附加门禁②。

---

## 五、同步底座（14.5 第 5 项、3.4.4、21.6）

- **幂等**：所有写接口要求 `client_request_id`，`{code}_idempotency(key, family_id, request_hash, response_snapshot, created_at)` 唯一索引，重放返回首次响应。
- **乐观锁**：实体带 `version`；写请求携带读取时的 version，不匹配返回 **409 + 双版本 payload**，绝不静默覆盖。冲突只在服务内判定，不存在跨服务锁。
- **变更日志**：业务写操作在事务内向 `{code}_change_log(lsn bigserial, family_id, entity, entity_id, op, version)` 追加，由 `packages/sync` 的 repo 基类完成，业务代码不记得写。
- **delta**：`GET /api/{code}/sync/changes?family_id&since_lsn&limit`，**每服务一条游标**；客户端 storage 里按 `(family_id, service)` 存 LSN，切家庭与切服务互不干扰。
- **状态机单向**：`draft → pending → synced`，冲突分支 `conflict` 必须由用户处置（21.6）。
- 客户端载体：`storage` 中的 `pending_ops` 队列 + 自研重放器（网络恢复 / 前后台切换 / 定时三触发），按 key 串行去重；队列按服务分桶，某服务不可用时其它桶照常重放。

验收样本是 18.2#3 的双设备并发写 200 次，测试脚本以两个虚拟客户端身份跑，不依赖真机。

---

## 六、搜索与投影（14.5 第 7 项、16.3、17.6）

- **索引唯一持有方是 `svc-homeos`**：它订阅七条流的可索引对象事件，写 `homeos_search_index(ref_domain, ref_id, family_id, title, keywords tsvector, updated_at)`。客户端只调 `GET /api/homeos/search` 一个接口，**不在前端并行打七个服务拼装结果**。
- 中文分词在应用层做 **bigram 切分**后写入 `simple` 配置的 tsvector，不引 `zhparser`（换分词器要改 Postgres 镜像，属 22.1 第 2 条评审项）。W0 用 bigram 足够达到 18.2#4 的 P95 ≤500ms（样本库按 21.1 上限 ×1.5：6 万流水 + 5000 库存 + 500 菜谱 + 1000 成长记录；此处 500ms 是端到端口径，含事件传播与投影写入）。
- L3 内容不进索引（20.1）；L2 只索引脱敏字段——这两条写在**索引写入器的字段白名单**里，不是调用方自觉。
- **他面高频渲染走本地投影**：`{code}_proj_{src}` 只允许本服务的订阅器写入，可随时全量重建（重建脚本随 W0 交付，否则投影表会变成第二数据源）。
- 动态流与搜索共用 `{code}_change_log`，避免两套变更捕获。
- **每日对账**（18.6 的 W0 部分）：投影 vs 来源逐表比行数与抽样字段；孤儿逻辑引用（存了 id 但对方无此行）进对账报表并告警。这是无物理外键的必然配套，不是可选优化。

---

## 七、通知下发（14.5 第 4 项，W0 只做到站内信可测）

链路：svc-homeos 触发 → `homeos_notification`（站内信，落库即算投递成功）→ `packages/adapter/push` 异步投递。

```
adapter/push/
├── provider.go        // interface: Send(ctx, Device, Msg) (Receipt, error)
├── stub.go            // W0 默认实现：写日志 + 返回伪造回执，保证链路可测
└── (apns.go / vendor.go 在 19.3 选型与凭证定版后新增，不改接口)
```

`homeos_push_device`（token 轮换需 upsert）、`homeos_notification_delivery`（每次投递一行，`dedupe_key` 唯一索引去重）、回执埋点按 19.5 字典。18.2#2 的服务端触发成功率 ≥99.9% **在 Wave 0 以站内信链路测得**，真实通道接入后同口径复测——这是门禁延后拍板的直接技术后果，不新增指标。

---

## 八、六面 demo：跨服务五步（附加门禁①）

每面在 Wave 0 只交付一张表 + 五个接口，用途是**把跨服务链路走通**，不是业务功能：

| 步 | 动作 | 证明的跨服务能力 |
|---|---|---|
| 1 注册 | 本服务写 `demo_item` 并发出 `{code}.due.registered` | outbox → JetStream → svc-homeos 消费 → 注册表幂等落库 |
| 2 写 | `POST /api/{code}/demo-items`（带 `client_request_id`） | 本服务内幂等 + 乐观锁 + `version` |
| 3 只读 | svc-homeos 首页摘要卡拉本面 demo 数据（只读接口） | `rclient` 三要素、超时降级、鉴权 SDK 跨服务生效 |
| 4 事件 | `{code}.demo_item.created` 被 homeos 消费进索引与动态 | 目录校验、durable consumer、去重表 |
| 5 提醒 | 到点触发 → `homeos.todo.completed` 回写本服务 | 事件驱动触发 + 异步回写 + 死信兜底 |

六面 × 五步全绿即门禁①。**判据看端到端**：第 1 步到日历可见的收敛时间、第 3 步的降级正确性、第 5 步的回写到达，都要有埋点数字，不是「接口返回 200」。

演示表不进 16.2 权威表；`migrations/{code}/` 里单独一个 demo 目录，**Wave 1 每面首张任务卡即删除 demo 表与接口**，门禁 2 在此期间对 `*_demo_item` 收紧白名单。

---

## 九、客户端与管理台（14.5 第 8 项、十七章）

- **单一 base URL**：`https://{host}/api/{code}/*`，由 Nginx 前缀反代；客户端不持有各服务地址、不做服务发现（17.7 第 5 条）。
- 一级导航全 App 唯一：底部五项（HomeOS 所有）+ 顶部六面横向切换器；面内二级 Tab 各面自定义（W0 只出框架与占位）。
- 分包严格按 `pages/{code}`，主包只放 shell 与 HomeOS 首页；`pages.json` 由 CI 校验（第 4 道门禁的前端项）。**分包与路由域一一对应**：`pages/{code}` 只调本域接口与显式声明的他域只读接口。
- 全局「＋」按当前会话家庭与权限过滤可建对象（17.4），无权限项**不显示**而非置灰。
- 深链 `homecube://{code}/{entity}/{id}`；进入 L3 对象触发会话内二次确认（17.6、20.2）。
- 离线：核心写走 `pending_ops`；跨家庭切换、邀请、权限修改、AI 录入离线不可用（21.6），四类入口在 shell 层统一禁用。
- 某面服务不可用时该面入口显示不可用态、其余面照常可用；依赖它的投影显示最后一次值并标注时效（12.3）。
- 图标统一图标集，`check-no-emoji` 进 CI（14.7）。

`admin/`（Vite + React）Wave 0 范围五项：家庭与成员查询、权限矩阵断言结果、**按服务**的死信查看/重放、审计与最小指标看板、对账报表入口。不做业务功能，不做用户侧配置。

---

## 十、部署、备份与回滚（14.5 第 1、9 项，22.4、21.3）

### 10.1 Compose 与一条命令

`deploy/` 内 compose：`nginx` + `nats`(JetStream) + `postgres16` + 7 个 `svc-{code}` + `web`/`admin` 静态容器 + 一次性 `migrate` 初始化容器（按七条序列逐服务重放后退出）。

```
make up          # 干净机器 → 可用环境：建 7 schema 与账号 → 迁移 → 种子 → 健康检查
make seed-perf   # 按 21.1 上限 ×1.5 构造压测样本（CI 缓存，不每次重跑）
make backup      # 7 个 schema 各一份 pg_dump -Fd + NATS 流快照 + uploads tar → 加密落 deploy/data/backups
make restore     # 恢复到指定备份点：整集群 → 逐服务重放迁移 → 起 7 服务与消费组（演练入口）
make rollback    # 只切回上一版本镜像 tag，校验备份完整性并打印，不自动动数据
```

- RPO ≤15 分钟靠 **WAL 连续归档**（`archive_command` 每分钟搬运一段），全量每日 1 次 + 每月留存 12 份（21.3）。
- **不允许只恢复单个 schema**：跨面逻辑引用会立刻失配（21.3 恢复顺序行）。
- Wave 0 必须完成**首次真实恢复演练**（含全栈 Compose 重建）并记录耗时——这是 14.5 第 9 项的门禁。
- 回滚刻意保持薄：数据库向后兼容由迁移纪律保证，不执行 down 迁移；**部署包上传与目标机执行由人手动完成**，脚本不做远程编排。
- 健康检查：每服务 `/healthz` 报告「本 schema 可连 + JetStream 已连」，`/metrics` 带常量标签 `code`（22.2 第 10 条）。

### 10.2 CI 五道门禁（22.5）

| # | 门禁 | 实现 | 失败即 |
|---|---|---|---|
| 1 | 迁移 | 逐服务在空库重放自己那条序列 + 版本表与代码声明一致 + 迁移文件只触碰本 schema | 阻断合并 |
| 2 | 数据归属 | 扫新增表名与 `TableName()` 比对 16.2 白名单；表前缀与所在服务不一致即失败；同构实体失败 | 阻断合并 |
| 3 | 验收指标 | 18.2 各条自动化用例（并发写 200、重放 1000×3、越权 100 次、搜索样本 P95、收敛时间）产出报告 | 阻断灰度/发布 |
| 4 | 分域与鉴权 | 路由前缀 AST、handler 内裸 SQL 与自写判定分支禁用（必须经 authz SDK）、业务表 `version`+软删、跨 schema SQL / 跨服务 import / 请求链路同步写调用禁用、`rclient` 三要素齐备、消费者幂等键存在、`pages.json` 分包命名、`check-no-emoji` | 阻断合并 |
| 5 | 契约 | `contracts/openapi/*` 与 `contracts/events/*` 向后兼容 diff；发布未登记事件、引用不存在的事件、破坏性变更未升 version | 阻断合并 |

按服务并行：某服务的 lint/test/build 只在该服务目录变更时跑，六面互不阻塞（这正是每面一个服务换来的组织收益）。

### 10.3 可观测与合规素材（14.5 第 9 项）

- 指标最小集（全部带 `family_id` 与 `code`，七服务分别出图）：接口 P95/P99、错误率、`/healthz` 存活、outbox 积压、死信数、事件端到端收敛时间、跨服务调用耗时与降级次数、同步冲突数、投递回执率、依赖调用量（W0 只有桩，为 0）。告警阈值写进 `deploy/alerts.yml`（21.5）。
- 审计事件按 21.5 十类落 `homeos_audit_log`，六面经 `homeos.audit.recorded` 事件上报（跨服务越权尝试也要记，字段带 `code`）；连续 5 次越权通知管理员。
- **合规素材底稿四项**：L2/L3 字段清单（由 20.1 表生成）、埋点字典（19.5）、保留期表（21.4）、第三方 SDK 与数据出境清单（当前为空表 + `adapter/` 接口清单）。Wave 1 门禁只判文本定版，不判素材收集。

---

## 十一、Wave 0 排期与拆卡

**10-13 周 = 5-6 个 Sprint（2 周一个）**。Wave 0 验收对象是底座，按「九项清单」而非「面」验收，每 Sprint 末一次集中走查；人类验收切片上限 3 面 × 2 功能域（11.7 第 2 条）。

| Sprint | 目标 | 关键卡 | 出口判据 |
|---|---|---|---|
| S1（2 周） | 七服务骨架可跑 | registry 域表 + 7 服务脚手架 + Nginx 前缀反代 + 7 schema 与 7 账号 + 迁移工具链 + Compose 全栈 + 门禁 1/2/4 接驳 + Jarvis Workbench 建 HomeCube 项目与任务模板 | `make up` 干净机器 ≤30 分钟；任一服务单独重启其余不受影响；门禁 1/2/4 全绿 |
| S2（2 周） | 总线与契约 | 三条流族 + outbox 投递器 + durable consumer 框架 + 去重表 + 死信流与表 + 事件目录与契约 diff + 门禁 5 接驳 + `HC_ARCHIVE` 归档消费者 | 重复投递不产生重复对象（重放 1000×3）；服务停 5 分钟事件不丢；未登记事件 CI 拒绝 |
| S3（2 周） | 身份与鉴权 SDK | 账号/验证码、家庭与成员、邀请三态、多家庭切换（重签发 token）、JWKS 分发、`packages/authz` + 七服务中间件接入、`pver` 与 `permission.updated` 失效、降级策略、15.3 逐格断言跨服务 | 附加门禁②通过（含六面服务各自打一遍矩阵）；权限变更 ≤1 秒跨服务生效；越权 100 次全拒并落审计 |
| S4（2 周） | 到期与通知（跨服务） | `rclient` 声明式客户端 + 16.4 注册/撤销/回写三事件 + svc-homeos 注册表与扫描触发 + 站内信 + push adapter/stub + `dedupe_key` 与回执埋点 | 六面各注册一类对象并触发；收敛 P95 ≤5s；18.2#2 ≥99.9%（站内信口径）；回写失败进死信且计入对账 |
| S5（2 周） | 同步与检索 | 七份 `change_log`/幂等表 + delta 接口 + `version` 乐观锁与 409 双版本 + 客户端 per-`(family_id, service)` 队列与重放 + `homeos_search_index` + bigram + 投影写入器与重建 + 每日对账 | 18.2#3、#4 通过（P95 ≤500ms 端到端）；游标互不干扰；对账报表可出且零孤儿引用 |
| S6（1-3 周，缓冲） | 六面接入与收口 | uni-app shell（五项 Tab + 六面切换器 + 全局＋+ 分包）、**六面 demo 五步**、admin 五功能、备份与首次恢复演练、合规素材四项、14.5 逐项验收报告 | **附加门禁①③通过**；Wave 0 验收报告提交人类 |

收口后**同时冻结三样东西**（22.2 第 7 条）：`svc-homeos` 对外契约（3.7 + 16.4）、七服务 OpenAPI、事件目录。冻结后 Wave 1 六面才允许并行开工（11.7 第 1、3 条），此后破坏性变更必须升 version 并过契约门禁。

任务卡按 22.3 建在 Jarvis Workbench 的 HomeCube 项目下，每卡必填「归属服务」与「归属数据源」，生效前走强制人工审核。

---

## 十二、风险与本方案刻意保守之处

1. **缺陷形态改变**：跨面逻辑的 bug 从「写错」变成「时序与补偿错」。所有跨面判据一律写成收敛时间与死信处置，不写「调用成功」；S2 起把 18.6 对账作为常驻卡而非一次性卡。
2. **单机资源**：13 个容器在家庭自托管机器上的占用是真实风险，S1 结束即实测并回填 21.1；不足则走同镜像多进程合并部署（11.9 ④），代码拓扑不动。
3. **NATS 单节点 `R=1`**：流不跨节点复制，磁盘故障可能丢掉「已 ack 但未归档」的窗口。缓解：outbox 在 Postgres 侧（有 WAL 归档）是发布方的权威留存，归档流进每日备份范围；不做 JetStream 集群（14.6）。
4. **发布顺序耦合**：SDK 与事件目录由 homeos 服务提供，若其契约破坏性变更会同时打断六面。缓解：契约门禁 5 + 语义化版本 + 旧 subject 保留一个 Wave；发布顺序固定 homeos 先行。
5. **pver 变更风暴**：一次批量权限调整会同时让七个服务的缓存失效并回源拉快照。缓解：快照接口按 `fid` 聚合（一次拉全量而非逐成员），回源带抖动与单飞（singleflight）。
6. **跨服务只读调用叠加端点延迟**：请求链路最多 1 跳是硬约束，超过即门禁失败；首页类聚合一律走投影或前端并行请求（16.3）。
7. **bigram 召回上限**：若 18.2#4 跨面命中 <45/50，才评审 `zhparser`（需换镜像，走 22.1 第 2 条偏离评审）。
8. **demo 表变影子数据源**：六面演示表若不删会长期存在，Wave 1 每面首张卡即删除，门禁 2 收紧白名单。

---

## 十三、联调与环境

- 本地：`make up` 起全栈，`make dev-{code}` 把某服务跑出容器外调试（其余六个走容器），端口与 env 在 `deploy/env.local.example`。
- 契约测试：`test/contract/` 用目录与 OpenAPI 做双端断言——提供方改动若破坏消费方，CI 在合并前失败，不靠联调期发现。
- 事件回放：`make replay?stream=HC_ARCHIVE&from=<时间>&subject=<前缀>`，仅运维口，回放走同一幂等键，不产生重复对象（18.2#5）。
- 时间旅行调试：所有事件信封带 `causation_id`/`correlation_id`（10.2），管理台死信详情页可反查链路。

---

## 十四、技术决定的定版口径

以下十四处是 Wave 0 契约的一部分，冻结至底座接口冻结窗口结束（22.2 第 7 条）；变更须走 22.1 第 2 条偏离评审。前四项为服务拓扑定版前已定、并在新拓扑下仍然成立的口径。

| # | 决定 | 内容 | 已接受的代价 |
|---|---|---|---|
| A | 迁移序列 | 七条独立序列 + per-service 版本表（golang-migrate per-service） | 无全局版本视图；跨服务变更靠契约测试而非排序保证 |
| B | 会话与权限载体 | JWT access(15min)+refresh(30d 轮换)，claims 带 `fid`/`role`/`pver`，RS256 + JWKS 分发 | 自管会话撤销表；纯 session 每请求一查被排除 |
| C | 中文检索 | 应用层 bigram + `simple` tsvector，不改 Postgres 镜像 | 召回弱于词典分词；命中不足阈值才评审 zhparser |
| D | 客户端离线队列 | `storage` 中 `pending_ops` + 自研重放器（三触发） | 容量受 storage 上限约束；本地 SQLite 需原生插件且 H5 不具备 |
| E | 服务边界 | 一面一服务 + homeos 底座服务，共 7 个；不设网关服务 | 13 容器与联调成本上升；靠一条命令全栈与并行 CI 抵偿 |
| F | 数据隔离 | 单集群 7 schema + 7 专属账号，账号权限即边界 | 无跨 schema JOIN；聚合读只能在 homeos 内做 |
| G | 总线载体 | NATS JetStream：7 条源流 + 归档流 + 死信流，`R=1` | 单节点无流复制；发布方权威留存靠 outbox |
| H | 一致性模型 | 无分布式事务；事务内 outbox + 消费端幂等表 + 每日对账 | 跨面可见有 ≤5s 延迟；引用完整性靠对账而非约束 |
| I | 鉴权实现 | 单一 SDK（`packages/authz`）+ 七服务同一中间件位置；不可达时拒写允读 | homeos 是鉴权面单点，需健康检查与缓存兜底 |
| J | 跨面读 | 三选一：只读 HTTP（≤300ms/≤1 跳/必带降级）／本地投影／逻辑 id | 数据有多份视图副本，必须可重建 + 对账 |
| K | 到期注册 | 一律事件异步（`{code}.due.registered` → homeos），回写走 `homeos.todo.completed` | 日历可见非实时；注册失败靠死信与对账发现 |
| L | 同步游标 | per-service `{code}_change_log`，客户端游标 `(family_id, service)` | 客户端要管七条游标；无全局一致性点 |
| M | 搜索归属 | 索引只在 `svc-homeos`，经事件投影喂入，单接口对外 | 面服务不可用时该面结果为空并可降级 |
| N | 发布单位 | 每服务一镜像一发布线，homeos 先行；降级才允许多进程合并 | 发布次数变多；灰度按家庭白名单不变 |
