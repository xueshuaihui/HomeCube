# HomeOS · 数据同步、备份与事件总线

> 范围：多端同步（实时/离线/冲突）、change_log 与 delta 接口、冲突解决策略（按对象类别）、备份恢复（RPO≤15min/RTO≤30min）、家庭数据导出、事件总线（outbox/JetStream/幂等/死信/版本）、`app/bundles` 分包契约。
> 权威源：`docs/prd-homecube.md` 3.4.4（数据系统）、3.4.6（事件系统）、3.6（Event/DeadLetter/DataLevel）、3.7（`sync/changes`、`data/export`、`app/bundles`、`dead-letters`）、10.4（死信）、16.3（跨面读取三方式）、18.2#3/#5/#13、21.3（备份）、21.6（离线）。
> 本文件 case 前缀 `HOM-SYNC`。

---

## HOM-SYNC-001 · 同步 delta 接口（每个服务一条）
- 关联需求：PRD 3.7 `GET /api/{code}/sync/changes`（`since_lsn`+`family_id`+`limit`）、14.5 第 5 项
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S6
- 前置条件：客户端持有 `(family_id, service)` 游标
- 步骤：以 `since_lsn` 拉取增量
- 预期结果：返回该 lsn 之后变更；游标可前进；离线重放后增量补齐
- 实现现状（快照）：已实现(分层)（`server/packages/sync/repo_test.go:468` `GetChanges`、`:550` delta JSON 无数据体）
- 执行状态：未执行

## HOM-SYNC-002 · 同步延迟 ≤1 秒（双设备并发写）
- 关联需求：PRD 3.8（同步延迟 ≤1 秒）、18.2#3（双设备并发写 200 次，在线 P95 ≤1s）
- 类型：性能 | 优先级：P0 | 建议归属 Sprint：S6
- 前置条件：两台真机同家庭
- 步骤：A 端写入，B 端读同 version 数据，测耗时
- 预期结果：在线态 P95 ≤1s；离线态转 21.6 重放口径
- 实现现状（快照）：引用脚本（`make soak` → `deploy/scripts/soak_test.sh`，Makefile:275）
- 执行状态：未执行

## HOM-SYNC-003 · 冲突策略：金额型乐观锁拒绝后到
- 关联需求：PRD 3.4.4（财务流水/账单/借贷=服务端版本号乐观锁+拒绝后到）、14.5 第 5 项（业务对象无 version 不得进 P1-M1）
- 类型：异常 | 优先级：P0 | 建议归属 Sprint：S6
- 前置条件：同一笔流水两设备并发改
- 步骤：后到请求携带旧 version 提交
- 预期结果：返回 409 + 双版本 payload；客户端重拉后重试；不静默覆盖
- 实现现状（快照）：待自动化
- 执行状态：未执行

## HOM-SYNC-004 · 冲突策略：数量型 delta 合并（不互相覆盖）
- 关联需求：PRD 3.4.4（清单/库存=字段级 LWW + 数量走 delta 增量合并）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：库存对象（P2 出生，策略在 P1 验证）
- 步骤：两人同时出库
- 预期结果：数量用 delta 合并，不互相覆盖（**策略在 P1 定，实际对象 P2 出生**）
- 实现现状（快照）：待自动化（P2 对象未出生，策略在 P1 定）
- 执行状态：未执行

## HOM-SYNC-005 · 冲突策略：状态机只进不退（幂等）
- 关联需求：PRD 3.4.4（打卡/完成状态=幂等+只进不退）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：完成状态对象
- 步骤：旧 pending 状态回写已 done 对象
- 预期结果：`done` 不被旧 `pending` 覆盖
- 实现现状（快照）：待自动化
- 执行状态：未执行

## HOM-SYNC-006 · 事件总线：发布经 outbox 不丢（at-least-once）
- 关联需求：PRD 3.4.6（事务内写 outbox、提交后投递、崩溃由 outbox 重投）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：服务进程在事务提交后崩溃
- 步骤：触发写事务 → 模拟崩溃 → 恢复
- 预期结果：事件经 outbox 重投，不丢；at-least-once
- 实现现状（快照）：已实现(分层)（`server/packages/bus/bus_test.go:324` 投递推进、`:355` 已投不重发、`:374` 失败保 pending）
- 执行状态：未执行

## HOM-SYNC-007 · 事件幂等：重放不产生重复业务对象
- 关联需求：PRD 3.4.6（按 `idempotency_key` 去重）、18.2#5（1000 事件重跑 3 次，新增对象=0）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：1000 事件
- 步骤：重跑 3 次
- 预期结果：新增业务对象=0；按 `causation_id` 可还原链路
- 实现现状（快照）：已实现(分层)（`packages/bus/bus_test.go` 去重；另 `repo/finance_test.go:341` 幂等）
- 执行状态：未执行

## HOM-SYNC-008 · 消费失败指数退避 + 死信
- 关联需求：PRD 3.4.6（失败指数退避至多 3 次、超限进死信）、10.4、3.6 DeadLetter
- 类型：异常 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：消费者持续失败
- 步骤：投递失败消息
- 预期结果：退避 [1s,10s,60s]×3 后入 `{code}_dead_letter`；可查可重放
- 实现现状（快照）：已实现(分层)（`server/packages/bus/bus_test.go:490` 失败写死信、`:609` 仅在最大投递时写死信、`:552` 重投后重跑）
- 执行状态：未执行

## HOM-SYNC-009 · 未登记事件不得发布（契约门禁拦截）
- 关联需求：PRD 3.4.6（未在目录登记的事件不得发布）、22.5 第 5 道门禁
- 类型：安全 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：尝试发布未登记事件
- 步骤：发未登记事件
- 预期结果：契约门禁拦截（CI 22.5 第 5 道）
- 实现现状（快照）：CI门禁（registry 事件目录校验；未登记事件拒绝发布）
- 执行状态：未执行

## HOM-SYNC-010 · 死信查看与重放（人审入口）
- 关联需求：PRD 3.7 `GET/POST /api/homeos/dead-letters/{id}/replay`（覆盖 homeos 自身死信）、10.4
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S2
- 前置条件：存在 dead letter
- 步骤：查看 → 重放
- 预期结果：重放成功并清死信；逐服务同名接口聚合展示
- 实现现状（快照）：待自动化（无 HTTP 死信口）
- 执行状态：未执行

## HOM-SYNC-011 · 每日备份 + WAL 归档（RPO≤15min/RTO≤30min）
- 关联需求：PRD 3.4.4（每日全备+binlog/WAL 连续归档）、21.3（RPO≤15min、RTO≤30min、整集群备份）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S14
- 前置条件：生产/同构环境
- 步骤：验证备份频率与归档加密
- 预期结果：每日全备（加密保留 30 天+月备 12 月）；WAL 归档加密、RPO≤15min
- 实现现状（快照）：引用脚本（`make backup` / `make restore`，Makefile:157/175）
- 执行状态：未执行

## HOM-SYNC-012 · 恢复演练（全栈 Compose 重建，首次即 M1 门禁）
- 关联需求：PRD 21.3（每季度真实恢复演练、首次即 P1-M1 门禁、含全栈 Compose 重建）、18.2 附加门禁
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S14
- 前置条件：已备份
- 步骤：`make restore-drill` 全栈重建
- 预期结果：先恢复整集群 → 各服务重放迁移序列校验版本表 → 重启当期全部服务与 JetStream 消费组；不允许只恢复单 schema
- 实现现状（快照）：引用脚本（`make restore-drill`，Makefile:202）
- 执行状态：未执行

## HOM-SYNC-013 · 家庭数据导出（HomeOS 与 Finance 两接口不得复用 handler）
- 关联需求：PRD 3.7 `POST /api/homeos/data/export`（按成员范围）、4.8 `GET /api/finance/reports/export`（两接口不得复用同一 handler）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：未点名(P1-M1)
- 前置条件：家庭有数据
- 步骤：分别调用两导出接口
- 预期结果：两接口独立实现；HomeOS 导出家庭级、Finance 导出财务级
- 实现现状（快照）：未实现（全仓无 `POST /api/homeos/data/export` 路由与 handler）
- 执行状态：未执行

## HOM-SYNC-014 · 分包清单契约（按需加载与远程下发载体）
- 关联需求：PRD 3.7 `GET /api/homeos/app/bundles`（version/url/sha256/size/min_compat）、17.8（P1 只用清单契约+随基座发布，真实远程下发 P2 起）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：客户端携带 codes 与 installed
- 步骤：请求分包清单
- 预期结果：返回每面分包 metadata；sha256/size/min_compat 齐全；P1 不实现远程下发链路
- 实现现状（快照）：已实现(HTTP)（`handler/bundle_manifest_test.go:71` bundles 线形、`:114` app/version 线形）
- 执行状态：未执行

## HOM-SYNC-015 · 离线记账可用、重放冲突提示不静默丢弃
- 关联需求：PRD 21.6（离线可用：记账；重放冲突金额型被拒绝需重拉重试，不静默丢弃）
- 类型：异常 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：断网记账多条
- 步骤：恢复网络后重放
- 预期结果：金额型冲突提示用户重拉重试；不做静默丢弃
- 实现现状（快照）：待自动化
- 执行状态：未执行

## HOM-SYNC-016 · `business_id` 周期后缀：四条可重复事件不得被当作重复丢弃
- 关联需求：PRD 10.4:1358（同对象可合法重复发生的事件，`business_id` 必须带周期或版本后缀；P1 四条：`finance.budget.exceeded` = `{budget_id}:{period}`、`{code}.due.registered` = `{source_id}:{due_at}`、`homeos.reminder.fired` = `{reminder_id}:{fire_date}`、`homeos.family.module.updated` = `{family_id}:{code}:{version}`）、10.2:1330（`idempotency_key` = `event_type + business_id`）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：四条事件各构造「同一对象、不同周期」的两次合法发生
- 步骤：
  1. 同一预算连续两个月超支，各发一次 `finance.budget.exceeded`
  2. 同一账单改期后重发 `{code}.due.registered`
  3. 同一周期提醒连续两天触发 `homeos.reminder.fired`
  4. 同一面连续两次启停 `homeos.family.module.updated`
- 预期结果：
  - 四次「第二次」均**不被消费端幂等表当重复丢弃**（各产生一次业务效果）
  - 幂等键形如 `type:{business_id}`，`business_id` 含周期/版本后缀
  - 事件目录（`contracts/events/{code}.yaml`）登记了该形态，门禁 5 可校验
- 实现现状（快照）：待自动化（`server/packages/bus/bus_test.go` 覆盖幂等与重投，但无「周期后缀」四条专项断言）
- 执行状态：未执行

## HOM-SYNC-017 · 事件信封字段完整（幂等键 / 因果链 / 版本 / 重试 / 投递形态）
- 关联需求：PRD 10.2:1329-1335（`event_id` 不作幂等键；`idempotency_key` = `event_type + business_id`；`causation_id` 用于链路追溯，无上游为 `null`；`version` 语义化只允许向后兼容；`attempt` 从 0 起；`delivery` 为 `stream`；`family_id`/`member_id` 消费端不得跨家庭处理）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：一次跨服务事件链路（财务写 → 事件 → HomeOS 消费）
- 步骤：
  1. 抓取事件信封，逐字段核对
  2. 用 `causation_id` 还原完整链路
  3. 构造一条 `family_id` 与消费方上下文不一致的事件
- 预期结果：
  - 九个字段齐全且取值合规；`event_id` 每次唯一、**未被用作幂等键**
  - 按 `causation_id` 可还原上游链路（无上游为 `null`）
  - 跨 `family_id` 的事件被消费端拒绝，不落业务表
- 实现现状（快照）：待自动化（总线有信封结构，缺字段级断言与跨家庭负向用例）
- 执行状态：未执行

## HOM-SYNC-018 · 事件版本兼容：破坏性变更须升 `version` 并保留旧 subject 一个期
- 关联需求：PRD 10.4:1362（语义化版本、向后兼容；破坏性变更须升 `version` 并保留旧 subject 一个期，由 22.5 契约门禁检测未升级即失败）
- 类型：异常 | 优先级：P1 | 建议归属 Sprint：S2
- 前置条件：事件目录可改；一份不兼容变更（删除必填字段）
- 步骤：
  1. 在目录里做一次不兼容变更但不升 `version`，跑门禁 5
  2. 升级 `version` 并保留旧 subject，重跑
  3. 让旧版本消费者继续订阅一个期
- 预期结果：
  - 未升级即被契约门禁**拒绝**（CI 失败）
  - 升级后新旧 subject 并存一个期，旧消费者不中断
  - 消费者按 `version` 分流，不因字段缺失崩溃
- 实现现状（快照）：待自动化（契约 diff 工具存在；缺「未升级必败」反例）
- 执行状态：未执行

## HOM-SYNC-019 · 分区有序：同 `(source_system, source_id)` 有序，不承诺全局有序
- 关联需求：PRD 10.4:1365（只承诺同一 `(source_system, source_id)` 分区内有序；消费端**不得依赖跨对象事件顺序**）
- 类型：边界 | 优先级：P1 | 建议归属 Sprint：S2
- 前置条件：同一对象连发两次状态变更；另有两个不同对象乱序到达
- 步骤：
  1. 同一对象连发 created → updated，检查消费顺序
  2. 两个不同对象的事件乱序投递，检查消费端是否出错
- 预期结果：
  - 同一对象的事件严格按序消费（最终状态为 updated）
  - 跨对象乱序不导致状态错误（消费端不依赖全局顺序）
- 实现现状（快照）：待自动化
- 执行状态：未执行

## HOM-SYNC-020 · 人工删除后不重建（EventIgnore 忽略表）
- 关联需求：PRD 10.4:1364（`EventIgnore(business_id, event_type, ignored_at, operator)` 实现「人工删除后不重建」，落归属服务自己的 schema）、10.3:1344（生成型单据适用该语义）
- 类型：异常 | 优先级：P1 | 建议归属 Sprint：S2
- 前置条件：一条事件生成的单据被人工删除；忽略表已登记该 `business_id`
- 步骤：
  1. 人工删除生成型单据
  2. 重放同一事件
  3. 检查是否重建、忽略表是否有记录
- 预期结果：
  - 重放**不重建**该单据；忽略表有 `(business_id, event_type, ignored_at, operator)` 记录
  - 忽略表落在**归属服务自己的 schema**，不跨服务共用
  - 未登记忽略的 `business_id` 仍可正常重建（不误伤）
- 实现现状（快照）：未实现（`server/` 无 `EventIgnore`/忽略表实现）
- 执行状态：未执行

## HOM-SYNC-021 · 文本型对象的字段级 LWW（档案 / 偏好 / 正文）
- 关联需求：PRD 3.4.4:292（档案、偏好、菜谱正文 = 字段级 LWW，文本型可接受后到覆盖）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：两设备同时编辑同一对象的**不同字段**
- 步骤：
  1. A 改字段一、B 改字段二，几乎同时提交
  2. 同步后检查两字段
  3. 两设备同时改**同一字段**，检查后到覆盖结果
- 预期结果：
  - 不同字段的并发编辑**都保留**（字段级合并，不是整行覆盖）
  - 同一字段冲突按 LWW 取后到值，且不报 409（文本型可接受）
- 实现现状（快照）：待自动化（现有测试覆盖金额型 409 与数量型 delta，无文本 LWW 用例）
- 执行状态：未执行

## HOM-SYNC-022 · 业务对象无 `version` 列不得进 P1-M1
- 关联需求：PRD 3.4.4:295（写路径一开始就落 `version` 与乐观锁；对象级版本 + 冲突返回 409 及双版本 payload；**业务对象无 `version` 列不得进 P1-M1**）
- 类型：安全 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：两服务全部业务表
- 步骤：
  1. 扫描 `information_schema.columns`，列出缺 `version` 的业务表
  2. 对每张缺列的表检查是否在 16.2 归属表内、是否属 P1 业务对象
- 预期结果：
  - P1 业务对象缺 `version` 列的数量 = 0
  - 底座运行表族（`_outbox`/`_idempotency` 等）按白名单豁免，不误判
- 实现现状（快照）：待自动化（可脚本扫描；需白名单）
- 执行状态：未执行

## HOM-SYNC-023 · 离线不可用清单：五类操作在离线态必须明确拒绝
- 关联需求：PRD 21.6:2310（离线可用：记账、清单勾选、库存出入、饮食/成长记录、查看档案与行程；**离线不可用：跨家庭切换、邀请成员、权限修改、AI 录入**）、21.6:2311（重放冲突保留提示，不静默丢弃）
- 类型：异常 | 优先级：P1 | 建议归属 Sprint：S6
- 前置条件：设备断网
- 步骤：
  1. 断网后依次尝试：切换家庭、邀请成员、修改权限、AI/OCR 录入
  2. 断网后记账并恢复网络，检查重放
  3. 制造金额型冲突后恢复网络
- 预期结果：
  - 四类操作在离线态**明确拒绝并给出原因**，不进入队列、不假装成功
  - 记账在离线态可用，恢复后自动重放且不重复
  - 金额型冲突保留冲突提示（重拉后重试），不静默丢弃
- 实现现状（快照）：待自动化（`web/tests/request.test.ts` 有请求层重放基础；缺四类负向断言）
- 执行状态：未执行

## HOM-SYNC-024 · 幂等表落在消费方自己的 schema，跨 schema 读写幂等表 = 0
- 关联需求：PRD 14.7:1700（**每个事件消费者必须幂等**：以 `event_type + business_id`（或 16.4 的注册幂等键）判重，重复投递不产生重复业务对象；**幂等表落在消费方自己的 schema**）、22.2:2337 第 2 条（数据库账号即边界）、16.2
- 类型：边界 | 优先级：P0 | 建议归属 Sprint：S2
- 前置条件：两个服务各有专属账号；可构造重复投递
- 步骤：
  1. 列出两个 schema 中的幂等表实际名称与归属
  2. 用 `svc-finance` 账号尝试读/写 `homeos_event_dedupe`
  3. 触发同一事件重复投递 3 次，检查两张幂等表的写入分布
  4. 检查判重键是否使用 `event_type + business_id`（而非 `event_id`）
- 预期结果：
  - 每个消费方有**自己的**幂等表，落在自己 schema 内；跨 schema 读写命中数 = 0
  - 判重键为 `event_type + business_id`；以 `event_id` 判重的实现命中数 = 0（`event_id` 每次投递都变，用它判重等于没有幂等）
  - 重复投递 3 次，业务对象只产生 1 个，且三方的幂等记录各自独立
  - 幂等记录释放（失败回滚场景）不留下永久占位（见 `server/packages/bus/consumer.go:351` `releaseDedupe`）
- 实现现状（快照）：已实现(分层)（`server/packages/bus/consumer.go:309` `checkDedupe`、`:351` `releaseDedupe`、`server/packages/bus/outbox.go:55` `OutboxTableName`；`server/migrations/homeos/homeos_0002_outbox_dedupe_dead_letter.up.sql` 建幂等表）
- 执行状态：未执行
