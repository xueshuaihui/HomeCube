# 反向开发清单（Case → 交付物）

> **口径（本文件的存在理由）**：case 是**需求基线**，由 `docs/prd-homecube.md` 派生，不因实现缺席而删减、不因实现现状而改判据。
> 开发方向是**从 case 反推**："要让这条 case 通过，后端/前端/契约必须补什么"。若某个功能没做，正确动作是**补开发**，不是把 case 标成"不适用"或把它从分母里拿掉。
> 因此本清单与 case 文件的关系是：case 说「应该怎样」，本清单说「现在还差什么才能达到」。
>
> **三条线互不掩盖**：
> | 线 | 含义 | 谁负责 |
> |---|---|---|
> | **L1 需求缺口**（SPEC-GAP） | PRD 自身没写清，导致 case 无法给出可判定判据 | 需求方补 PRD |
> | **L2 实现缺口**（IMPL-GAP） | 需求清楚、判据清楚，代码没做 | 后端/前端排期交付 |
> | **L3 验证缺口**（TEST-GAP） | 代码做了，但没有能证明它满足验收口径的测试 | QA/研发补测试 |
> | **线 D 契约偏差**（DEV） | 代码主体在、但 HTTP 面（路径/方法/响应）与 PRD 不符，**或路由未挂载** | 后端对齐（见 `DEVIATIONS.md`） |
>
> **线 D 与 L2 的边界（一句话）**：「handler 在、路由/方法不在」→ DEV；「handler 也不在」→ IMPL。判之前必须先 grep 确认 handler 是否真的存在（`DEVIATIONS.md` §五 记了一次因此撤回的 DEV-03）。
>
> 统计：L1 **8** 项 · L2 **39** 项 · L3 **129** 条 case 待自动化 · **契约偏差 3 条 case（DEV-01/DEV-02）** · 阻塞 case 合计 **54 条未实现 + 129 条待自动化**（分母 **291**）。其中 **IMPL-42/43 来自历史报告承接**（见 §五），与 case 无绑定关系，不计入「阻塞 case」列。
> 口径校正（2026-10-27 重基线）：上一版本行写的「L1 4 · L2 18 · L3 69 · 分母 188」是 188 条 case 时代的快照，已失效；现数字由 `index.md` 实测与本文件行数共同对齐，`lint.sh` 会拦住漂移。
> **契约偏差不并入 L2**：它是第四类（线 D），登记在 `DEVIATIONS.md`，不计入实现缺口数、也不计入未实现数——否则「改路径」和「从零开发」会被混成一个数字。

---

## 一、L1 需求缺口（阻塞判据，需 PRD 补齐）

| 编号 | 阻塞 case | 缺口 | PRD 现状 | 需要补什么 |
|---|---|---|---|---|
| SPEC-GAP-01 | HOM-SESS-001 | 验证码发送的**频控阈值** | 3.7:417 写"验证码发送按 21.4 限流"，但 21.4:2291 是「日志与数据保留」，**全文无任何限流定义** | 频控窗口、次数上限、超限错误码与重试提示 |
| SPEC-GAP-02 | HOM-SESS-002/003 | **token TTL 与 refresh 有效期** | 全文检索 `TTL`/`过期` 零命中，只有"refresh 轮换"四个字 | access/refresh 各自有效期、轮换宽限、失效后行为 |
| SPEC-GAP-03 | HOM-ADM-001 | 21.5:2302 审计九类含「规则启停」，但规则引擎（3.7:403 `POST /rules`）标注 **P6** | P1 无规则对象，这一类审计在 P1 **无发生源** | 明确 P1 判八类、九类随 P6 判；或 P1 先落启停骨架 |
| SPEC-GAP-04 | FIN-EXP-004/006 | 19.3:2194 要求「依赖选型表」，19.3 多处以它为准入条件 | 仓库内**不存在该表**（`docs/DEPENDENCIES.md` 无此文件），OCR/ASR 供应商、单价、额度全部悬空 | 产出并定版依赖选型表；OCR/ASR 随财务面子域在 P1-M1 开工前定版（19.3:2421） |
| SPEC-GAP-05 | HOM-DATA-002 | **数据导入与跨设备迁移的期次与形态** | 3.4.4:284 只列「导入、导出、跨设备」，**未标 M1/M2**，技术方案 §十一 全表无对应卡 | 明确期次、导入格式（是否复用导出包）、冲突策略与归属成员映射 |
| SPEC-GAP-06 | HOM-MEM-012 | **通讯录邀请的形态与期次** | 3.4.1:242 列为邀请四态之一，但 S3 卡只写「邀请码/链接/二维码三态」（`p1-tech-plan.md:455`），**通讯录未进任何 Sprint** | 明确是否 P1 交付；若是，补通讯录授权口径与不上传全量通讯录的边界 |
| SPEC-GAP-07 | HOM-CMP-002 | 20.3 中「儿童数据不参与 AI 训练」「分享默认脱敏摘要」两条的**承接期次** | 20.3:2237-2238 有明确要求，20.4:2245 说「P4 起适用、P5 期末全面可测」，但这两条能力属成长面/家人面，**不在 P1 case 体系的可挂靠范围内** | P4/P5 出生期各自立 case；P1 只判「监护人显式同意链路」（已由 HOM-CMP-002 承接），不把跨期能力提前塞进 P1 |

| SPEC-GAP-08 | FIN-TX-011/012 | **标签四接口未在接口表登记** | PRD 4.7:627 有 `Tag` 实体、4.4/4.5.1 有「标签」与「按标签筛选」，但 **4.8 接口表（648-667 行）没有任何 tag 接口**；`server/contracts/openapi/finance.yaml` 亦只有 schema 内 `tags` 字段、无 `/finance/tags` 路径 | 在 4.8 登记四条（`GET/POST /api/finance/tags`、`PUT/DELETE /api/finance/tags/{id}`，即 `handler/finance.go:1770/1797/1979/2027` 注释所写），并写明「账本成员范围与标签筛选共用同一语义」的判定口径 |

> 说明：这 8 项**不是**"做不做"的问题，是"无从判定"。case 里对应条目的判据写 `待 PRD 补齐`，不写成"暂不支持"。

---

## 二、L2 实现缺口（按 Sprint 排序，可直接当排期表）

### S3 · 账号与会话（**登录链路已在盘**，剩余为加固与对齐）

> **重基线更正（2026-10-27）**：原 IMPL-01/02/03 写的「`server/` 下 `auth/login`、`sms` 零命中；`main.go` 无 auth 路由组」**已失效**——`cmd/svc-homeos/main.go:152-167` 注册了完整 `authGroup`（`/sms-code` `/login` `/refresh` `/logout` `/.well-known/jwks.json`），`handler/auth.go` 832 行、`handler/auth_test.go` 993 行 10 个测试。三条已撤销（见本节末「已撤销 / 已迁移」），其中 IMPL-02 的实质内容（`/families/switch` 对齐）由 `DEVIATIONS.md` 的 **DEV-01** 承接。

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-40 | HOM-SESS-001 | **验证码散列存储**：`homeos_sms_codes.code` 改为存散列（加盐 + 不可逆），消费时比对散列；明文只允许出现在投递通道 | `server/migrations/homeos/homeos_0006_auth_family.up.sql:61` 为 `code VARCHAR(10) NOT NULL`，无 hash 列；`repo/identity.go:131` `IssueSMSCode` 直接写明文 | SPEC-GAP-01 |
| IMPL-41 | HOM-SESS-004 | `/families/switch` 契约对齐（**与 DEV-01 同一件事**；本行只承载工时与依赖，状态以 `DEVIATIONS.md` 为准） | `cmd/svc-homeos/main.go:228` 注册的是单数路径；`contracts/openapi/homeos.yaml` 未登记该路径 | — |

### S4 · 鉴权 SDK 与分级加密

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-04 | HOM-AUTH-008 | L2 **文本列**字段级加密（落盘与备份均为密文） | `data-levels.yaml` 契约存在，`server/` 无加密实现（`grep encrypt` 零命中） | — |
| IMPL-05 | HOM-AUTH-009 | L2 **金额/余额**磁盘与备份加密（库内明文列 + 落盘密文双口径） | 同上 | IMPL-04 的加解密封装 |
| IMPL-06 | HOM-AUTH-010 | L3 **端到端**加密（服务端仅存密文，无解密能力） | 同上 | 客户端密钥管理 |

> 这三条直接影响 18.2#7，且验收方式是"抽检磁盘落盘值与备份文件"，**桩实现无法通过**。

### S5 / S14 · 设备锁与到期中心

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-07 | HOM-AUTH-011/012/013、HOM-APP-007、HOM-NOTI-006、18.2#14 | 设备锁 PIN：散列不出端、连续 5 次失败锁定 L3、指数退避、忘记 PIN 走手机号重验重置、未解锁读 L3 = 0 | 全仓 `device.?lock`/`设备锁` 零命中 | IMPL-06（L3 载体） |
| IMPL-08 | HOM-AUTH-013 | `deploy/scripts/device_lock_check.sh` 的判据修正——现脚本在字段缺失时**会假通过** | 脚本存在但断言依赖不存在字段 | IMPL-07 |
| IMPL-09 | HOM-TIME-010 | `POST /calendar/events` 管理台/运维口（手工补录） | `calendar/events` 零命中 | — |

### S6 · 同步与检索

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-10 | HOM-SRCH-001/003/004/005 | `homeos_search_index` 的**写入方**（投影写入器）——接口在、索引无数据，搜索恒返回空 | `UpsertSearchIndex`/`DeleteSearchIndex` **零调用方** | 各面写路径事件接入 |

### S7-S8 · 财务主干

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| ~~IMPL-11~~ | — | **已迁移至 `DEV-02`**（标签四路由未挂载属契约偏差，不是实现缺口）；编号保留不复用 | 见 `DEVIATIONS.md` §4.2 | — |
| IMPL-12 | FIN-LED-003 | `PUT /finance/ledgers/{id}`：编辑名称/说明/可见范围 + `version` 递增 + 乐观锁 409 | 全仓 `UpdateLedger` **零命中**（repo 只有 `CreateLedger:618`/`ListLedgersByFamily:648`，handler 只有 `CreateLedger:638`/`ListLedgers:669`）；`cmd/svc-finance/main.go:286-287` 只注册 `POST`/`GET`。**按 D3 边界规则属「handler 也不在」→ 留在 L2，不迁 DEV**（计划原拟的 DEV-03 已撤回，见 `DEVIATIONS.md` §五） | — |
| IMPL-13 | FIN-LED-004 | 流水筛选/统计的 `ledger_id` 参数 | `repo/finance.go` 无 `LedgerID` 过滤 | IMPL-12 |

### S11-S12 · 预算与账单

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-14 | FIN-TX-014、FIN-EXP-007 | 票据附件：`GET/POST /transactions/{id}/attachments` + `adapter/storage`（单机磁盘） + 限额校验（≤3 张/笔、≤5MB/张、jpg/png/webp） | `attachment` 在 `server/` 零命中 | — |

### S14 · M1 收口

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-15 | HOM-APP-001/002 | `GET/PUT /homeos/settings`（活配置，禁止死 UI） | `main.go` 无 `settings` 路由 | — |
| IMPL-16 | HOM-SYNC-013 | `POST /homeos/data/export`（与财务导出**不得复用 handler**） | `data/export` 零命中 | — |
| IMPL-17 | HOM-ADM-001~005 | 管理台五口：`audit-events`、`reconciliation-report`、`data/backup-status`、`data/restore`、`events/publish|subscribe` | 五个路径在 `server/` 全部零命中 | —（管理员身份已随登录链路具备，见 `handler/auth.go` claims 的 `Role`） |
| IMPL-18 | FIN-DEMO-001 | 跨服务五步 demo 链路（`GET/POST /finance/demo-items`） | `demo-items`/`DemoItem` 零命中 | 两服务已起 |

### S17-S18 · M2

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-19 | FIN-SPLIT-008 | `POST /finance/expenses`（报销主干写入口） | `expenses` 零命中 | 发票（FIN-SPLIT-005）同批 |

### S19 · M2 收口

| 编号 | 阻塞 case | 缺什么 | 现状证据 | 依赖 |
|---|---|---|---|---|
| IMPL-20 | FIN-DEMO-002 | 删除 demo 表 + 迁移回滚 + 路由 + 前端入口，全仓零残留；删除后 18.3 九条复跑 | 待 IMPL-18 落地后才有可删对象 | IMPL-18 |

> **L2 汇总**：**37 项**，覆盖 **54 条未实现 case**（口径同步 `index.md` 实测）。
> **原「IMPL-01/02 登录链路是唯一全网关型缺口」的结论已作废**——登录与 refresh 在盘（`handler/auth.go` + `authGroup` 五条路由，10 个测试）。token 类能力的瓶颈现在是两件：① **分级加密与设备锁未落地**（IMPL-04/05/06/07，直接卡 18.2#7/#14）；② **切换家庭的路径与契约不一致**（DEV-01，卡 HOM-SESS-004 的端到端）。

---

### 本轮新增（Round 5 · 需求点级审核发现的实现缺口）

| 编号 | 关联 case | 缺什么 | 证据（grounded） | 依赖 |
|---|---|---|---|---|
| IMPL-21 | HOM-DATA-002 | 数据导入 / 跨设备迁移：导入接口 + 幂等 + 归属映射 | `server/` 无导入路由；期次亦未定（SPEC-GAP-05） | SPEC-GAP-05 |
| IMPL-22 | HOM-DATA-003/004/006、HOM-ADM-004 | 备份**版本回滚**、恢复顺序（禁单 schema）、解散家庭 30 天冷存与逾期物理删除（含备份/归档残留清理） | `data/restore` 零命中；无冷存与物理删除实现 | IMPL-17 |
| IMPL-23 | HOM-DATA-005、HOM-SESS-005 | 保留期清理任务：审计 2 年 / 事件 90 天+1 年 / 死信 90 天 / 通知 180 天 / 解散 30 天 / 登录 6 个月 | 全仓无保留期清理任务 | — |
| IMPL-24 | HOM-DATA-007、FIN-EXP-009、FIN-TX-014 | 图片缩略图与原图分档（列表不加载原图）+ 图片格式导出 | 附件链路整体未实现；无图片派生 | IMPL-14 |
| IMPL-25 | HOM-CMP-002 | 首次登录/注册的协议确认留痕（协议版本 + 时间 + 主体，可导出可审计） | `agreed_at`/`consent_at`/`agreement_version` 全库零命中（页面与接口已有：`main.go:171`、`legal/detail.vue:35`） | — |
| IMPL-26 | HOM-CMP-003 | 埋点字典（P1-M1 交付物）+ 埋点上报，必带 `family_id`/`code`、不含 L3 | 仓库无埋点字典文件，`server/` 无埋点上报 | — |
| IMPL-27 | HOM-CMP-004/005 | 管理台二次认证；凭据扫描与依赖漏洞扫描接入 CI（且反例必败） | `admin/`、`server/` 检索 `2fa`/`totp` 零命中；`.github/workflows` 仅 `release.yml` | — |
| IMPL-28 | HOM-CMP-006、HOM-ADM-006、FIN-EXP-012 | 指标端点（九类指标、`family_id`+`code` 双维度、每服务出图）+ 最小告警集（死信 >100 等）+ AI 用量与成本三项 | `/metrics` 与 `code` 常量标签**已有**（`server/packages/obs/metrics.go:96`、`serve.go:245`），但只注册接口延迟/错误率/进程项；九类中 outbox 积压、死信数、事件收敛、跨服务耗时与降级、同步冲突、投递回执、依赖成本、AI 三项**均无 collector**；告警集未落 `deploy/alerts.yml` | — |
| IMPL-29 | HOM-SYNC-020 | `EventIgnore` 忽略表与「人工删除后不重建」语义（落归属服务自己的 schema） | `server/` 无 `EventIgnore` 与忽略表 | — |
| IMPL-30 | HOM-MEM-012/013/015、HOM-NOTI-009、HOM-TIME-015 | 通讯录邀请；访客凭证 30 天/可撤销/不可导出；儿童逐面开放；通知 `channel` 与 @；家庭公告与模块内评论 | 五项均无实体/路由（详见各 case 的「实现现状」） | SPEC-GAP-06 |
| IMPL-31 | FIN-TX-016、FIN-BUD-014 | 模板记账；预算模板（M2） | 无模板实体与接口（`finance_recurring_rule` 只覆盖周期规则） | — |
| IMPL-32 | HOM-CT-006/007 | 归属门禁脚本（含「未出生表提前出现必败」反例）；`CategoryMapping` 与未映射确认框 | `scripts/` 只有 `build-base.sh`/`verify-release.sh`，无 ownership 检查；`CategoryMapping` 零命中 | — |
| IMPL-33 | HOM-APP-012/013、HOM-SUM-010 | 首页四态文案与「暂不可用」重试三件事、0 格 C 区不渲染的**可断言实现**（当前仅前端部分呈现，无抓包断言与四态样本） | 前端有部分呈现，无对应测试与四态构造脚本 | — |
| IMPL-34 | HOM-ARCH-001/002/019/027 | 架构静态检查脚本五件：领域分层与 service 层、`gorm:"-"` handler 旁路、路由系统前缀、依赖方向、跨服务调用三件事声明 | `scripts/` 只有 `build-base.sh`/`verify-release.sh`，五类检查均无 | — |
| IMPL-35 | HOM-ARCH-017 | 底座契约冻结快照（OpenAPI + 七域 registry）+ 契约门禁第 5 道 | 无冻结快照文件；`server/contracts/openapi/` 无版本化冻结产物 | — |
| IMPL-36 | FIN-EXP-010/011/012 | AI 调用 P95 ≤3s 的阈值实现与超时回落、家庭级每日配额表与计数、AI 用量成本指标 | 无 P95 统计端点；无家庭配额表；AI 三项无 collector；OCR 仍为桩（`server/packages/adapter/ocr/`） | — |
| IMPL-37 | HOM-ARCH-020/021/024/026 | 边界扫描四件：无 Redis / 无搜索集群、无 K8s·网格·2PC·全采样、范围外十二项零实现、镜像内服务数 = 1 | 四类扫描脚本均无；当前**事实合规但无断言**，靠人工记忆维持 | — |
| IMPL-38 | HOM-ARCH-004/005/006 | HomeOS 侧三项反向扫描：schema 内业务字段数 = 0、业务规则硬编码分支 = 0、业务表与页面归属 = 0 | 无 schema 列级扫描、无业务名词分支扫描、门禁 2 只校验表名前缀不校验归属 | — |
| IMPL-39 | HOM-ARCH-003/009/013/016/018、HOM-SYNC-024 | 代码已具备但缺自动化断言：claims 六字段结构、多家庭唯一约束、服务随面出生、迁移命名、三运维端点、幂等表归属 | 六项实现均已在盘（证据见各 case），缺可重复执行的断言脚本 | — |

### 已撤销 / 已迁移（编号保留，不再复用）

| 原编号 | 原描述 | 处置 | 依据 |
|---|---|---|---|
| ~~IMPL-01~~ | `/auth/sms-code`、`/auth/login`、`/auth/refresh` 三 handler + 路由组 + 验证码存储 + JWT 签发 | **撤销**（证据失效） | `cmd/svc-homeos/main.go:152-167` 五条 auth 路由在盘；`handler/auth.go` 832 行 / `handler/auth_test.go` 993 行 10 测试 |
| ~~IMPL-02~~ | `POST /families/switch` 重签发 token | **撤销**，实质内容转 **DEV-01 / IMPL-41** | handler `family.go:381 SwitchFamily` 在盘，仅路径单复数不符 |
| ~~IMPL-03~~ | 登录链路打通后 `pver` 才能端到端 | **撤销**（前提已不成立） | 登录链路存在；`pver` 端到端缺的是「缓存失效 ≤1 秒」的实测用例，归 L3（HOM-AUTH-005 待自动化） |
| ~~IMPL-11~~ | `/tags` 四路由未挂载 | **迁移至 DEV-02**（契约偏差，非实现缺口） | 四个 handler 在盘（`handler/finance.go:1770/1797/1979/2027`），`svc-finance/main.go` 无 tag 路由 |
| ~~DEV-03~~ | 原拟将 IMPL-12 迁为契约偏差 | **撤回**（`UpdateLedger` 全仓零命中，handler 也不在） | 见 `DEVIATIONS.md` §五 |


## 三、L3 验证缺口（有实现、无足以证明验收口径的测试）

**129 条 case 处于「待自动化」**（口径同步 `index.md` 实测；原写的 69 是 188 条时代的旧数），按域分布：

| 域 | 待自动化 | 典型情形 |
|---|---|---|
| homeos | 见 `index.md` 矩阵 | 边界类（上限/非法值/越权负向）大多无 HTTP 用例；`pver` 生效只测了仓储层 |
| finance | 见 `index.md` 矩阵 | 归档恢复、`period` 非法值、导出三参数组合、附件限额等缺用例 |

三类**特别提示**（这些不是"没写测试"，而是"测试存在但证明不了验收口径"）：

| 编号 | 问题 | 说明 |
|---|---|---|
| TEST-GAP-01 | 45 格矩阵只断言**格数** | `server/test/authz/matrix_test.go:171` 断言 `testCount != 45`，即"用例数对上了"，但每格的**期望值**是否逐条取自 15.3 表格需人工核 |
| TEST-GAP-02 | `device_lock_check.sh` 假通过 | 字段缺失时不断言失败 → 18.2#14 的"未解锁读 L3 = 0"当前**不可信** |
| TEST-GAP-03 | 冒烟脚本 ≠ 验收 | `deploy/scripts/rt1-e2e-test.sh` 的 147 项是冒烟覆盖，不能替代 18.2/18.3 的度量定义（P95、行数一致、零误差等式） |

---

## 四、依赖链（决定排期顺序，不能并行硬上）

```
DEV-01 切换家庭路径对齐 ─→ HOM-SESS-004（不修则按 PRD 路径打 404，重签发无法端到端证明）
IMPL-41 是 DEV-01 的工单行，两者同生共死

DEV-02 标签四路由注册 ─→ FIN-TX-011/012（不修则标签只能停在仓储层）
                        └─→ 前置：SPEC-GAP-08（4.8 未登记 tag 接口）确认后才好写 openapi

IMPL-40 验证码散列 ─→ HOM-SESS-001 第 2 条判据（当前应判 `失败`，不是"待环境"）

IMPL-04/05/06 分级加密 ─→ IMPL-07 设备锁 ─→ IMPL-08 脚本判据 ─→ 18.2#7 / 18.2#14

IMPL-10 搜索索引写入 ─→ HOM-SRCH-003/004/005（无索引时"按面分组"无从判）

IMPL-12 账本 PUT ─→ IMPL-13 ledger_id 过滤（同一份归集定义）

IMPL-17 管理台五口 ─→ IMPL-22 备份回滚 / 恢复顺序（同用管理员身份与审计口径）

IMPL-18 demo 链路 ─→ IMPL-20 删除 demo（S19）
```

**关键路径结论（2026-10-27 重基线，已改写）**：原结论「S3 的登录链路是全项目单点前置，卡住 6 条 case 与 1 个管理台缺口」**作废**——登录链路已在盘，不再卡任何东西。现在的关键路径有两条，且都是**小时级而非人日级**，可以并行：

1. **DEV-01（1.5 人日）**：`/families/switch` 对齐。它卡的是 18.2#1「多家庭上下文重建」的端到端证据，属于**验收口径是否会失真**的问题，不是功能有无的问题。
2. **IMPL-04/05/06 → IMPL-07（约 20 人日）**：分级加密与设备锁。这是 18.2#7 与 18.2#14 的唯一通路，也是 P1 里唯一「桩实现必然被抓」的验收项（抽检磁盘落盘值与备份文件）。

> 排期含义：S3 的登录卡从「最高优先级」降为「加固项」；腾出的人日应转投 IMPL-04/05/06 与四处假绿修复（C-S-01~04）。

---

## 五、历史缺陷承接（2026-10 浏览器验收遗留）

> **本节的来历**：`docs/` 顶层原有 10 份 2026-10-03 ~ 10-09 的过程报告，内容已被本目录的 case 体系取代，2026-10-27 清理。但清理前逐条核实了报告里的 18 个缺陷，**发现 2 项至今未闭环**，若随之删掉就永久丢失线索，故承接于此。
>
> 核实方式：每条缺陷回到代码定位对应实现，判定 `已修复 / 未修复 / 非缺陷`。核实结果：ROUND1 的 11 项全部闭环（10 修 + 1 项改为返回空集合定界）；ROUND2 的 7 项中 **4 项已修**（trash 取参改走 session、`original_type` 兜底、金额符号、错误对象补 `message`）、**1 项判定为非缺陷**（登录统一 401 是 PRD 3.4.1 与契约的明确要求，用于防手机号枚举）、**2 项未修**（即下表）。

| 编号 | 关联 case | 缺什么 | 证据（grounded，2026-10-27 复核） | 依赖 |
|---|---|---|---|---|
| IMPL-42 | —（见下「为什么不关联 case」） | **六个写入端点的请求字段三方不一致**：前端发出的字段名与日期格式，既不匹配后端 DTO、也不匹配 `finance.yaml`。真实浏览器实测 8 个写入端点中 **6 个 100% 400**，`bill/goal/loan/ledger` 四表一行未落库 | 后端 DTO 现状（`server/services/svc-finance/internal/handler/finance.go`）：`CreateBillRequest{FamilyID,PayeeID,AmountCents,DueAt}`——无 `title`（契约 required）、无 `recurring`；`CreateLoanRequest{LenderName,BorrowerName,PrincipalCents,...}`——前端发 `counterparty_name`；`CreateGoalRequest{Name,TargetAmountCents,Deadline}`——前端发 `target_cents`/`target_at`；`CreateLedgerRequest{FamilyID,Name,MemberIDs,PeriodStart,PeriodEnd}`——前端只发 `name`；`CreateBudgetRequest{...,StartDate,EndDate}`——前端发 `"2026-10-01"` 纯日期，binding 要求 RFC3339；`CreateSplitSettlementRequest{...,TotalAmount}`——前端发 `total_amount_cents` | — |
| IMPL-43 | —（PRD 无对应验收项） | **八个页面弹层底部操作按钮在小屏视口不可见**：`max-height:80vh` 与 `overflow-y:auto` 同时加在包含按钮区的 `.dialog-content` 上，导致按钮被推到视口外；用户首屏看不到「确定」，也无滚动提示 | `web/src/pages/finance/account/index.vue:369-375`（`max-height: 80vh` / `overflow-y: auto`）；同套 CSS 复制于 bill / budget / goal / loan / split / category / ledger 七页。原实测：视口 1280×633，`.dialog-content` 的 `scrollHeight=646` 而上限 506px，按钮实际位于 615~677 | — |

**为什么不关联 case**：这两项恰好暴露了现行判据的一个盲区——

- **IMPL-42** 是「服务端单接口测试全部通过，但前端按契约发请求就失败」。291 条 case 的判据都以**服务端接口行为**为准，checks 里的 `http`/`go-test` 自行构造合法 body，**与前端实际发出的 body 无关**。所以 291 条即使全绿也发现不了它。要覆盖，必须在 L3 增补「前端请求体 ↔ 后端 DTO ↔ 契约」三方对齐的 e2e 断言。
- **IMPL-43** 是纯视觉可用性，PRD 无验收项，属 SPEC-GAP 候选；若要纳入，需先在 PRD 补一条「弹层操作区在小屏下必须可见」。

> **处置建议**：不单独走 Sprint 排期，挂在四处假绿修复（C-S-01~04）之后一次性返工。IMPL-42 **应以契约为准改后端 DTO**（补 `title`/`recurring`、`due_at` 由 `string` 改 `time.Time`），而不是把前端改成迁就现状——否则与 `finance.yaml` 只会越走越远。

---

## 六、用法

1. **排期**：取某一 Sprint 的 L2 表格 → 每行即一张开发卡；「阻塞 case」列就是该卡的验收范围。**线 D（契约偏差）不排开发卡，只排「对齐卡」**，工时通常 1~2 人日，见 `DEVIATIONS.md`。
2. **回归**：卡完成后，把对应 case 的 `执行状态` 从 `未执行` 改 `通过/失败`，并回填 `实现现状（快照）`（必须带 `文件:行号`）。**`实现现状` 由人写**——"通过"是事实、"已实现"是判断，两者不可互相代替。
3. **不变量**：`docs/cases/lint.sh` 会拦住五类退化——① 把 `未实现` 改成 `纯手动` 以规避交付；② 新增 case 却不在追溯矩阵中登记接口；③ `契约偏差` 没有 DEV 编号或 DEV 已关但 case 没更新；④ 执行状态写时间戳却不符合 `状态 · 时间 · run-id`；⑤ index 统计与实际不符。
4. **重算**：`python3 docs/cases/gen_requirements.py && python3 docs/cases/gen_index.py && python3 docs/cases/gen_traceability.py`，再 `bash docs/cases/lint.sh` 全量校验。
5. **体检**：`python3 docs/cases/audit_baseline.py` 用路由级交叉验证复核「已实现/未实现/契约偏差」是否还成立，输出 STALE / DRIFT / WEAK 三类。
