# 家立方 HomeCube · Wave 0 技术方案

> **文档性质**：研发交付物，不是需求文档。需求唯一权威源是 `docs/prd-homecube.md`（23.4）；本方案只回答「14.5 的九项交付怎么落地」，不新增、不裁剪任何需求口径。与本方案冲突的需求表述以 PRD 为准；PRD 未覆盖的技术分叉在末章集中列出，需人类拍板后回填本节。
>
> **范围**：Wave 0 = 底座骨架 + 六面接入框架，8-10 周（4-5 个 2 周 Sprint）。收口判定 = 14.5 各项验收 + 18.2 Wave 0 附加门禁三项（六面 Hello World 全通、15.3 权限矩阵逐格断言、干净机器一条命令拉起 ≤30 分钟）。
>
> **明确不做**（依 18.2/23.5 拍板结论）：任何识别/录入类 AI；隐私政策与用户协议文本；依赖选型定版；真实推送凭证；规则引擎与跨面联动业务（W3）。Wave 0 期间一切外部服务以 `adapter/` 接口 + 本地实现或桩交付。

---

## 一、工程结构

### 1.1 仓库布局

单仓 monorepo，四个顶层产物（对应 14.5 第 1 项、22.2 第 1 条）：

```
HomeCube/
├── server/                     Go 1.24 + Gin + GORM + PostgreSQL 16
│   ├── cmd/server/             入口：加载配置 → 注册七个域路由 → 启动调度器
│   ├── internal/
│   │   ├── app/                业务域，每域一套 handler/service/repo/model/dto
│   │   │   ├── homeos/         身份·家庭·权限·到期·通知·同步·搜索·动态·设置
│   │   │   ├── finance/ purchase/ diet/ trip/ kin/ growth/
│   │   │   └── ...             W0 只建目录 + 域注册骨架 + 各自一张演示表
│   │   ├── platform/           跨域能力（唯一允许被各 app 依赖的层）
│   │   │   ├── authn/ authz/   token 签发校验、权限中间件
│   │   │   ├── bus/            事件总线：发布、订阅注册、投递、重试、死信
│   │   │   ├── due/            到期注册契约与到期中心（16.4）
│   │   │   ├── sync/           乐观锁、幂等、变更日志、delta 拉取
│   │   │   ├── search/         索引与关键词检索
│   │   │   ├── proj/           投影表写入器与每日对账
│   │   │   ├── obs/            指标、审计、结构化日志、trace id
│   │   │   └── adapter/        外部服务边界：push/ocr/asr/map/storage/ai
│   │   └── pkg/                无业务语义的工具（金额分、时间、分页、ID）
│   ├── migrations/             七条独立迁移序列，每域一个子目录
│   └── test/                   门禁级集成测试（权限矩阵断言、并发写、重放）
├── web/                        uni-app(Vue3)：六面 shell + 分包
├── admin/                      Vite + React：最小管理后台
├── deploy/                     docker-compose、备份/恢复/回滚脚本、环境变量样例
└── .github/workflows/          CI 四道门禁（22.5），本仓库自建，不共享 workflow
```

依赖方向单向且由 CI 拦截：`app/{域} → platform → pkg`；`app/A` 与 `app/B` 之间不得 import（跨面只能走 16.3 三种方式）。这条比 22.2 第 2 条更严，因为它是**唯一能在编译期证明分域存在**的手段。

### 1.2 命名一致性（16.1 的五处前缀）

`code` 一次配置，五处生效。实现为 `platform/registry` 里的一张域表：

| code | 路由域 | 表前缀 | 事件前缀 | 前端分包 | 迁移目录 |
|---|---|---|---|---|---|
| `homeos` `finance` `purchase` `diet` `trip` `kin` `growth` | `/api/{code}` | `{code}_` | `{code}.` | `pages/{code}` | `migrations/{code}/` |

CI 门禁 4 的静态检查项即读取此表：路由注册路径、GORM `TableName()`、迁移文件名、`pages.json` 分包路径四处必须同时命中，任一偏离即失败。这样「架构漂移」在合并前就被拦，而不是靠评审记住规则。

---

## 二、服务端落地

### 2.1 迁移序列（对应 14.5 第 1 项、16.6、22.5 门禁 1）

七条**独立序列**，每域一个目录，文件名 `{code}_{序号:04d}_{描述}.{up|down}.sql`，例如 `finance_0007_add_split_index.up.sql`。工具用 golang-migrate，每个域实例指定各自的版本表（`schema_migrations_homeos` 等）。

理由：16.6 要求「归属在迁移层可见」，单序列 + 全局递增号做不到「某域回滚到自己在某版本」，而七条序列天然满足，并且门禁 1 可以逐域重放校验。代价是跨域外键必须在同一发布窗口内手工排序，Wave 0 阶段各域表极少，可接受。

约定：
- 迁移只允许 `CREATE TABLE`/`ALTER` 本域前缀的表；出现他域前缀即门禁 2 失败。
- 业务表必备列：`id uuid primary key`（UUIDv7，时间有序以抑制索引膨胀）、`family_id uuid not null`、`version bigint not null default 1`、`created_at/updated_at`、`deleted_at/deleted_by`（14.7、22.2 第 7 条）。系统预置数据用可空 `family_id` 表达。
- 金额一律 `amount_cents bigint`；禁止浮点与 `numeric` 混用（14.7）。
- 索引第一列固定 `family_id`（列表/统计查询天然命中）。

### 2.2 身份与权限（14.5 第 2、3 项）

**账号**：手机号 + 验证码（W0 用「固定测试验证码 + 站内信桩」，商用短信通道 14.6 不做）。`homeos_account`、`homeos_member`、`homeos_family`、`homeos_invitation`（码/链接/二维码三态共用一行，`expires_at`、`used_by`）、`homeos_membership`（`family_id, account_id, role, status`，多家庭即多行）。

**会话**：JWT access（15 分钟）+ refresh（30 天轮换，落库可撤销）。claims：

```
{ sub: account_id, fid: 当前会话家庭, role: owner|member|ward|guest,
  pver: 权限版本, jti: 会话 id }
```

`fid` 直接进 token 是 22.2 第 6 条与 15.2 的实现前提：**中间件从 token 取 `fid` 注入 ctx，业务 handler 拿不到也不要写 SQL 判家庭**。切换家庭 = 重签发一对 token，切换后客户端重建上下文（14.5 第 2 项验收 ≤1 秒）。

`pver`（家庭级权限版本号，任何权限/成员变更即 +1）用于解决「旧 token 带旧角色快照」的失效问题：中间件比对 `pver` 与库内值，不等则强制刷新权限加载。这就是 18.2#6「权限变更 ≤1 秒生效」的实现路径，而不是缓存 TTL 碰运气。

**权限模型**：`Permission(scope, resource, action, condition)` 一表 + 15.3 默认矩阵作为代码内置策略（`authz/policy.go`），家庭级覆盖写库，判定顺序严格按 15.2：显式拒绝 > 角色默认矩阵 > 家庭级覆盖 > 对象级 ACL。字段级可见性（15.4）在 **DTO 序列化层**实现，不在 SQL 层过滤——L3 字段不进默认导出与埋点的要求（20.2）只有在 DTO 层才拦得住。

**测试即门禁**：15.3 是 7 系统 × 5 角色的表格，直接由 `test/authz/matrix_test.go` 逐格断言（每格一条 `期望 allow/deny + 实际请求`），表格与用例同源生成，避免「文档改了测试没改」。这是 Wave 0 附加门禁②。

### 2.3 到期中心与注册契约（14.5 第 4 项、16.4）

表：`homeos_calendar_event`、`homeos_todo`、`homeos_reminder`、`homeos_due_registration`。

`due_registration` 是 16.4 契约的落库形态，字段即契约映射：`{ source_system, source_id, source_family_id, kind, title, due_at|start_at|end_at, participants[], repeat, priority, done_returns_to, visibility, status }`，唯一键 `(source_system, source_id, kind)` 保证重复注册幂等。

**注册入口只有一个**：`platform/due.Register(domain, req)`。业务面不得插入这三张表（归属表 16.2 已定）。删除语义按 16.4：业务对象软删 → 同事务级联把注册项置 `expired`；到期中心「完成」→ 调 `done_returns_to` 注册的回调回写业务面对象，回写失败进死信而非静默。回调由各域在启动时注册到 `due` 的回调表，避免 `platform` 反向 import `app`。

**触发器**：单进程扫描循环（30 秒一次，`SELECT ... FOR UPDATE SKIP LOCKED` 取到期批次），不引入外部调度与 MQ（22.1 第 2 条）。多实例部署时 advisory lock 保证单写；这是 Wave 0 的容量假设，21.1 的实例上限内成立。

### 2.4 通知下发（14.5 第 4 项，Wave 0 只做到站内信可测）

链路：到期触发 → `homeos_notification`（站内信，落库即算投递成功）→ `platform/adapter/push` 异步投递。

```
adapter/push/
├── provider.go        // interface: Send(ctx, Device, Msg) (Receipt, error)
├── stub.go            // Wave 0 默认实现：写日志 + 返回伪造回执，保证链路可测
└── (apns.go / vendor.go 在 19.3 选型与凭证定版后新增，不改接口)
```

`homeos_push_device`（`dedupe` 无关，token 轮换需 upsert）、`homeos_notification_delivery`（每次投递一行，`dedupe_key` 唯一索引在 `notification` 维度去重）、回执埋点按 19.5 字典。

18.2#2 的服务端触发成功率 ≥99.9% **在 Wave 0 以站内信链路测得**（14.5 第 4 项已按此改写），真实通道接入后同一口径复测。这是本次延后拍板的直接技术后果，不新增指标。

### 2.5 同步底座（14.5 第 5 项、3.4.4、21.6）

- **幂等**：所有写接口要求 `client_request_id`；`homeos_idempotency(key, family_id, request_hash, response_snapshot, created_at)` 唯一索引，重放返回首次响应而非重复执行。
- **乐观锁**：实体带 `version`；写请求携带读取时的 version，不匹配返回 **409 + 双版本 payload**（server/client 两份），绝不静默覆盖（14.5 第 5 项验收）。
- **变更日志**：所有业务写操作在事务内向 `homeos_change_log(lsn bigserial, family_id, domain, entity, entity_id, op, version)` 追加一行，由 `platform/sync` 的 repo 基类统一完成，业务代码不记得写。
- **delta 拉取**：`GET /api/homeos/sync/changes?family_id&since_lsn&limit`，客户端持有 per-family LSN 游标；离线队列重放成功后按返回的 lsn 推进游标。状态机单向：`draft → pending → synced`，冲突分支 `conflict` 必须由用户处置（21.6）。
- 客户端载体：uni-app 侧 `storage` 里的 `pending_ops` 队列 + 重放器（网络恢复/前后台切换/定时三触发），重放串行同 key 去重。

Wave 0 的验收样本是 18.2#3 的双设备并发写 200 次，测试脚本以两个虚拟客户端身份跑，不依赖真机。

### 2.6 事件总线骨架（14.5 第 6 项、十章、22.1 第 2 条）

同进程投递 + 落库，不引 MQ。表：`homeos_event_log`、`homeos_event_subscription`、`homeos_event_dead_letter`、`homeos_event_ignore`（EventIgnore，10.4）。

发布：业务事务提交后调 `bus.Publish(evt)`，**先落 `event_log(status=pending)` 再投递**，进程崩溃由扫描循环补投——这是 at-least-once 的全部含义。事件结构严格照 10.2 十字段，`idempotency_key = {type}:{business_id}` 上有唯一索引（消费侧去重）。

消费：订阅器在启动时注册 `(event_prefix, handler)`；handler 报错按指数退避重试至多 3 次（10.4），超限写死信，死信可在管理台查看与人工重放（3.7 两接口）。

**Wave 0 不建规则引擎**（3.4.7、11.2、卷首第 8 条）。因此 W0 的验收是 18.2#5 的「重放 1000 事件 ×3 不产生新业务对象 + 按 `causation_id` 可还原链路」，S1-S6 场景的联动段留空。

事件目录（W0 交付物）由 `bus` 的注册表生成，新增事件未登记即 CI 失败——这是 10.1「先登记再发布」的可执行版本。

### 2.7 搜索与投影（14.5 第 7 项、16.3）

- 投影表 `proj_*` **只允许 `platform/proj` 写入**，由各域订阅器声明映射；可随时全量重建（重建脚本随 Wave 0 交付，否则投影表会变成第二数据源）。每日对账任务比对 `proj_*` 与来源（18.6 的 W0 部分）。
- `homeos_search_index(ref_domain, ref_id, family_id, title, keywords tsvector, updated_at)`。中文分词在**应用层做二元切分**（bigram）后写入 `simple` 配置的 `tsvector`，不引 `zhparser`：换分词器要改 Postgres 镜像，属 22.1 第 2 条评审项，Wave 0 用 bigram 足够达到 18.2#4 的 P95 ≤500ms（样本库 6 万流水 + 5000 库存 + 500 菜谱 + 1000 成长记录，按 21.1 上浮 50% 构造）。
- L3 内容不进索引（20.1）；L2 只索引脱敏字段。这两条是索引写入器的白名单，不是调用方自觉。
- 动态流 `homeos_dynamic` 与搜索共用 `change_log`，避免两套变更捕获。

### 2.8 六面接入的最小演示物（服务附加门禁①）

每域在 Wave 0 只交付一张业务表 + 五个接口，用途是把底座五步走通，不是业务功能：

| 步 | 接口 | 证明的底座能力 |
|---|---|---|
| 注册 | `POST /api/{code}/demo-items` | 分域路由、鉴权、`family_id` 注入 |
| 写 | 同上（带 `client_request_id`） | 幂等、乐观锁 |
| 只读 | `GET /api/{code}/demo-items` | 游标分页、DTO 可见性、投影同步 |
| 事件 | 发布 `{code}.demo_item.created` | 总线、订阅、幂等重放 |
| 提醒 | 注册 `due_registration` | 到期中心、通知链路 |

六面 × 五步全绿即门禁①。演示表不进 16.2 权威表，Wave 1 各面开工时替换为真实对象——`migrations/{code}/` 里单独一个目录存放 demo 表，Wave 1 首张卡即删除。

---

## 三、客户端六面 shell（14.5 第 8 项、十七章）

- 一级导航全 App 唯一：底部固定五项（首页/动态/＋/消息/我的，HomeOS 所有），顶部六面横向切换器，面内二级 Tab 由各面自定义（17.3，W0 只出框架与占位）。
- 分包严格按 `pages/{code}`，主包只放 shell 与 HomeOS 首页；`pages.json` 由 CI 校验分包命名（门禁 4 的前端项）。
- 全局「＋」是 action sheet：按当前会话家庭与权限过滤可建对象（17.4），无权限项**不显示**而非置灰。
- 深链 `homecube://{code}/...`，进入 L3 对象触发会话内二次确认（17.6、20.2）。
- 离线：核心写走 `pending_ops` 队列；跨家庭切换、邀请、权限修改、AI 录入离线不可用（21.6）——这四类入口在 shell 层统一禁用，不各自判断。
- 图标：统一图标集，`check-no-emoji` 闸门进 CI（14.7）。

管理台 `admin/`（Vite + React）Wave 0 范围只四项：家庭与成员查询、权限矩阵断言结果、死信查看/重放、审计与最小指标看板。不做业务功能，不做用户侧配置。

---

## 四、部署、备份与 CI（14.5 第 1、9 项，22.4、22.5）

### 4.1 一键部署与回滚

`deploy/` 内 docker-compose：`postgres16` + `server` + `admin`（静态）+ `web`（构建产物静态服务）。

```
make up          # 干净机器 → 可用环境（含七域迁移、种子数据、健康检查）
make backup      # pg_dump 全量 + WAL 归档，加密后落 deploy/data/backups
make restore     # 恢复到指定备份点（演练入口）
make rollback    # 切回上一版本镜像 + 校验备份完整性（不自动动数据，见下）
```

RPO ≤15 分钟靠 **WAL 连续归档**（`archive_command` 每分钟搬运一段），全量每日 1 次 + 每月留存 12 份（21.3）。Wave 0 必须完成**首次真实恢复演练并记录耗时**（附加门禁之一在 14.5 第 9 项）。

回滚刻意保持薄：`make rollback` 只切镜像与静态资源，数据库向后兼容由迁移纪律保证（不做 down 迁移自动执行）。理由与既有工程约定一致——发版脚本不承担自动备份回滚，出问题由人决定恢复点。

### 4.2 CI 四道门禁（22.5，Wave 0 全部接驳）

| # | 门禁 | 实现 | 失败即 |
|---|---|---|---|
| 1 | 迁移 | 空库重放七条序列 + 版本与代码声明一致性校验 | 阻断合并 |
| 2 | 数据归属 | 扫新增表名与 GORM `TableName()`，比对 16.2 白名单；同构实体/他域前缀即失败 | 阻断合并 |
| 3 | 验收指标 | 18.2 各条自动化用例（并发写 200、重放 1000×3、权限 100 次越权、搜索样本库 P95）产出报告 | 阻断灰度/发布 |
| 4 | 分域与鉴权 | 路由前缀 AST 检查、handler 内 `family_id` 裸 SQL 禁用、新增表 `version`+软删字段、`pages.json` 分包命名、`check-no-emoji` | 阻断合并 |

门禁 3 的样本库由 `make seed-perf` 按 21.1 上限 ×1.5 构造，进 CI 缓存，避免每次重跑造数。

### 4.3 可观测与合规素材（14.5 第 9 项）

- 指标最小集：接口 P95/P99、错误率、事件 pending/死信堆积、同步冲突数、投递回执率、依赖调用量（W0 只有 stub，为 0）+ 告警阈值写进 `deploy/alerts.yml`（21.5）。
- 审计事件按 21.5 十类落库，含 `on_behalf_of`。
- **合规素材底稿四项**（本次拍板新增的 W0 交付）：L2/L3 字段清单（由 20.1 表生成）、埋点字典（19.5）、保留期表（21.4）、第三方 SDK 与数据出境清单（当前为空表 + `adapter/` 接口清单）。Wave 1 门禁只判文本定版，不判素材收集。

---

## 五、Wave 0 排期与拆卡

4-5 个 Sprint，2 周一个。人类验收切片上限 3 面 × 2 功能域（11.7 第 2 条）——Wave 0 的验收对象是底座，按「九项清单」而非「面」验收，每 Sprint 末一次集中走查。

| Sprint | 目标 | 关键卡 | 出口判据 |
|---|---|---|---|
| S1（2 周） | 骨架可跑 | 仓库分域 + registry + 七域路由挂载 + 迁移序列与工具链 + docker-compose + CI 门禁 1/4 接驳 + Jarvis Workbench 建 HomeCube 项目与任务模板 | `make up` 在干净机器 ≤30 分钟；门禁 1/4 全绿 |
| S2（2 周） | 身份与权限 | 账号/验证码、家庭与成员、邀请三态、多家庭列表与切换（重签发 token）、Permission 表 + 内置策略、authz 中间件、`pver` 失效、15.3 逐格断言用例 + 门禁 2/3 接驳 | 附加门禁②通过；切换后上下文 ≤1 秒；越权 100 次全拒并落审计 |
| S3（2 周） | 到期与通知 | 三对象 + `due_registration` 与回调表、扫描触发、站内信、push adapter + stub、`dedupe_key` 与回执埋点、搜索索引与投影写入器、每日对账 | 到期可注册可触发；18.2#2 服务端成功率 ≥99.9%（站内信口径）；18.2#4 P95 ≤500ms |
| S4（2 周） | 总线与同步 | 事件发布/订阅/重试/死信/忽略表 + 事件目录、`client_request_id`、`version` 乐观锁与 409 双版本、`change_log` 与 delta 接口、客户端离线队列与重放 | 18.2#3、#5 通过；死信可重放；重复投递不产生重复对象 |
| S5（1-2 周） | 六面接入与收口 | uni-app shell（五项 Tab + 六面切换器 + 全局＋+ 分包）、六面 demo 五步、admin 四功能、备份与首次恢复演练、合规素材四项、14.5 逐项验收报告 | **附加门禁①③通过**；Wave 0 验收报告提交人类 |

S5 是缓冲：若 S1-S4 任一张卡被打回，只顺延 S5，不压缩六面 demo。收口后**冻结 HomeOS 对外契约**（3.7 + 16.4，22.2 第 4 条），Wave 1 六面才允许并行开工（11.7 第 1、3 条）。

任务卡按 22.3 建在 Jarvis Workbench 的 HomeCube 项目下，每卡必填 `归属数据源`，生效前走强制人工审核。

---

## 六、风险与本方案刻意保守之处

1. **推送与法务延后的连带效应**：Wave 0 的到达率验收口径降级为站内信；iOS 出包与真实推送在 W1 前完成凭证准备。若 W1 门禁②③（选型表、Apple 账号）拖延，直接后果是财务面 OCR/语音与地图不能进排期（19.3），面六家会同时卡在「依赖未定版」上。建议 S3 末就把 Apple 开发者账号与推送通道作为运营项启动，与开发并行。
2. **七条迁移序列的跨域外键**：Wave 1 会出现 `growth → finance` 引用外键，排序需人工保证同批发布。约定：跨域外键只允许引用方的迁移「后置」于被引用域，且由门禁 2 校验目标表存在。
3. **单写调度器**：到期扫描与总线投递在单实例内可靠；多实例靠 advisory lock。Wave 0-2 的实例上限内成立（21.1），不做分布式调度。
4. **demo 表清理**：六面演示表若不删会长期变成影子数据源。约定为 Wave 1 每面首张任务卡 = 删除该域 demo 表与接口，门禁 2 在此期间对 `*_demo_item` 白名单收紧。
5. **bigram 中文检索的召回上限**：达到 21.1 容量或 18.2#4 跨面命中 <45/50 时，才评审 `zhparser`（需换镜像，22.1 第 2 条）。

---

## 七、需人类拍板的四个技术分叉

以下四处 PRD 未规定，本方案给的是默认选择，选定后写回本节并作为 Wave 0 契约的一部分冻结：

| # | 分叉 | 本方案默认 | 代价 |
|---|---|---|---|
| A | 迁移序列形态 | 七条独立序列 + per-domain 版本表 | 跨域外键需人工排序；单序列则失去 per-domain 回滚 |
| B | 会话与权限载体 | JWT(access+refresh) + `pver` 版本失效 | 需自管撤销表；纯服务端 session 则每请求多一次查询 |
| C | 中文检索方案 | 应用层 bigram + `simple` tsvector | 召回弱于词典分词；换 zhparser 需改镜像并过 22.1 评审 |
| D | 客户端离线队列载体 | `storage` 队列 + 自研重放器 | 数据量受 storage 上限约束；改 SQLite 需加原生插件并过栈评审 |
