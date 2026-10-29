# HomeOS · 账号与会话（登录、token、会话记录）

> 范围：验证码发送、登录换 token、refresh 轮换、切换家庭重签发 token、会话记录保留、无账号成员禁登录。
> **本文件是「case 由 PRD 派生」的典型样本**：PRD 3.7:417 只登记了三个接口名与「验证码发送按 22.4 限流」，而 22.4 是「日志与数据保留」，**全文没有任何限流阈值、token TTL、refresh 有效期的定义**——因此本文件的「判据」一栏标 `待 PRD 补齐`，而不是按某项实现现状写成"目前不做"。这类**需求缺口（SPEC-GAP）**与实现缺口是两回事，见 `docs/cases/BACKLOG.md` 与 `docs/cases/SPEC-GAP-PROPOSAL.md`。
> 权威源：`docs/prd-homecube.md` 3.4.1:250（JWT claims 携带 `family_id`、角色快照、`pver`）、3.7:417（三个 auth 接口）、16.6:2002（pver 与缓存失效）、22.4:2546（登录与会话记录保留 6 个月）、22.5:2558（审计事件含"登录"）；`docs/p1-tech-plan.md:455` S3（"账号/验证码（测试码 + 站内信桩）… JWKS 分发"）、`:456` S4（鉴权 SDK）。
> 本文件 case 前缀 `HOM-SESS`。**归属 Sprint：S3**（账号与验证码卡），S4 只做其后置校验，不重复承载登录写路径。
> **与既有 case 的边界**：`HOM-FAM-004` 判的是**客户端**切家庭后的上下文重建（缓存、币种、时区）；本文件 `HOM-SESS-004` 判的是**服务端**重签发 token 与旧 token 的家庭隔离，两者不可互相替代。
>
> **2026-10-27 重基线说明**：本文件此前 6 条全部标「未实现」，证据写「`server/` 下 `auth/login`、`sms` 零命中；`main.go` 无 auth 路由组」。经 `docs/cases/audit_baseline.py` 体检与人工复核，该证据**已失效**：`cmd/svc-homeos/main.go:152-167` 注册了完整 `authGroup`（`/sms-code` `/login` `/refresh` `/logout` `/.well-known/jwks.json`），`handler/auth.go` 832 行，`handler/auth_test.go` 993 行 10 个测试。故按事实重写以下 6 条——**判据一字未改，只改「实现现状」**。

---

## HOM-SESS-001 · 验证码发送（`POST /api/homeos/auth/sms-code`）
- 关联需求：PRD 3.7:417（验证码发送）；3.3:240（"验证码登录"属全新建能力，身份与权限行）；24.5:2690（依赖选型表定版项，20.3:2437 为要求本身，**P1-M1 开工前不定版则以适配器桩替代**）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S3
- 前置条件：手机号未注册（新用户）与已注册（老用户）两组样本各一
- 步骤：
  1. 输入手机号，请求发送验证码
  2. 观察返回体与站内信/短信投递
  3. 30 秒内对同一手机号再次请求
  4. **直接查 `homeos_sms_codes` 表，检查 `code` 列存的是明文还是散列**
- 预期结果：
  - 首次请求受理成功，返回统一的"已发送"响应（**不区分手机号是否已注册**，避免账号枚举）
  - 验证码为一次性凭据，落库时不可逆散列存储，明文只出现在投递通道
  - 重复请求的处置**判据待 PRD 补齐**：PRD 3.7:417 写"按 22.4 限流"，而 22.4 全文为日志与数据保留，**未定义频控窗口、次数上限、错误码**（SPEC-GAP-01；建议值见 `SPEC-GAP-PROPOSAL.md`：同号 60s/次、日 10 次、IP 100/日、429 + `Retry-After`）
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/auth.go:90` `SendSMSCode`、`cmd/svc-homeos/main.go:154` 路由、`repo/identity.go:131` `IssueSMSCode`、测试 `handler/auth_test.go:279`）
- 执行状态：未执行
- 判据缺口：第 2 条「不可逆散列存储」**当前不满足**——`homeos_sms_codes.code VARCHAR(10) NOT NULL` 无 hash 列（`server/migrations/homeos/homeos_0006_auth_family.up.sql:61`），`IssueSMSCode` 直接写入明文。已登记 **IMPL-40**（验证码散列存储 + 消费比对改造），本条在该卡关闭前应判 `失败`

## HOM-SESS-002 · 登录换 token（`POST /api/homeos/auth/login`）
- 关联需求：PRD 3.7:417（"登录换 token；token 带 `family_id`、角色快照与 `pver`"）；3.4.1:250；16.6:2002（各服务中间件据 token 内 `family_id` 做隔离、据 `pver` 做缓存比对）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S3
- 前置条件：手机号已通过验证码校验；该账号至少属于 1 个家庭，且另有一个"已注册但无家庭"的账号样本
- 步骤：
  1. 提交手机号 + 验证码登录
  2. 解出 token claims
  3. 用该 token 调 `GET /api/homeos/families` 与任一无权接口
- 预期结果：
  - 返回 access token + refresh token；**claims 必须含 `family_id`、角色快照、`pver` 三项**，缺任一即视为契约不达标
  - 有家庭账号：默认进入其首个家庭，`GET /api/homeos/families` 返回该家庭
  - 无家庭账号：登录成功但**不得进入首页**，直接落"创建家庭 / 加入家庭"引导（PRD 3.7:432 明确"不允许存在 0 面家庭"）
  - 无权接口按 16.3 矩阵拒绝，而非返回 404 掩盖
- 实现现状（快照）：已实现(HTTP)（`handler/auth.go:192` `Login`、`cmd/svc-homeos/main.go:157` 路由；claims 三字段见 `handler/auth.go:238` `FamilyID` / `:240` `Role` / `:241` `PVersion`；无家庭引导见 `auth.go:232-252` 的 bootstrap session；测试 `auth_test.go:279` `TestSendSMSCodeThenLoginIssuesVerifiableTokensForRealRows`、`:539` `TestFamilylessLoginBootstrapsAFamilyEndToEnd`）
- 执行状态：未执行

## HOM-SESS-003 · refresh 轮换（`POST /api/homeos/auth/refresh`）
- 关联需求：PRD 3.7:417（"refresh 轮换"）；22.4:2546（登录与会话记录保留 6 个月）
- 类型：安全 | 优先级：P0 | 建议归属 Sprint：S3
- 前置条件：持有一份有效 refresh token
- 步骤：
  1. 用 refresh token 换新 access token
  2. 用**同一份旧** refresh token 再换一次
  3. 用已失效的 access token 访问受保护接口
- 预期结果：
  - 首次刷新成功，**同时下发新的 refresh token**（轮换而非续期）
  - 旧 refresh token 立即失效，第二次使用被拒并落审计（22.5 的"登录"类）
  - 已失效 access token 访问受保护接口返回 401，且**不降级为匿名可读**
  - token TTL 与 refresh 有效期**判据待 PRD 补齐**（SPEC-GAP-02：PRD 全文无 `TTL`/`过期`/`refresh` 有效期定义；建议值见 `SPEC-GAP-PROPOSAL.md`：access 2h / refresh 30d / 轮换宽限 60s）
- 实现现状（快照）：已实现(HTTP)（`handler/auth.go:364` `Refresh`、`cmd/svc-homeos/main.go:160` 路由；refresh 只存散列见 `auth.go:273` 与 `:687-700`；测试 `auth_test.go:479` `TestRefreshRotatesAndOldTokenDies` 覆盖"轮换后旧 token 死亡"）
- 执行状态：未执行
- 判据缺口：TTL 与宽限的具体数值待 SPEC-GAP-02 确认；确认前只能判"轮换机制成立"，不能判"有效期达标"

## HOM-SESS-004 · 切换家庭重签发 token（`POST /api/homeos/families/switch`）
- 关联需求：PRD 3.7:419（家庭列表与切换当前会话家庭）、3.4.1:250（"切换后重建上下文缓存…JWT claims 必须携带 `family_id`"）；`docs/p1-tech-plan.md:455` S3（"多家庭列表与切换（**重签发 token**）"）
- 类型：安全 | 优先级：P0 | 建议归属 Sprint：S3
- 前置条件：同一账号属于家庭 A 与家庭 B，A 与 B 各有独立数据
- 步骤：
  1. 以家庭 A 的 token 切到家庭 B
  2. 用**切换前**的 token 请求家庭 B 的成员列表
  3. 用切换后的 token 请求家庭 A 的成员列表
- 预期结果：
  - 切换返回**新签发的 token**，claims 中 `family_id` 已为 B、角色快照与 `pver` 按 B 重算
  - 旧 token（仍带 A）请求 B 的数据被拒（`family_id` 隔离），**不得因"同一账号"放行**
  - 切换后的 token 请求 A 的数据同样被拒
  - 同账号跨 A/B 的数据不合并（3.4.1:250 原文）
- 实现现状（快照）：契约偏差（`cmd/svc-homeos/main.go:228` 注册的是 `POST /family/switch`（**单数**），实际路径 `/api/homeos/family/switch`（`server/test/acceptance/routes.json:378` 实抓确认）；PRD 3.7:419 与 `docs/cases/traceability.md:15` 要求 `POST /api/homeos/families/switch`（**复数**）——而 `contracts/openapi/homeos.yaml` 连该路径都未登记（该文件 20 条 path 中无 switch，属契约文件侧的第二个缺口）；业务逻辑见 `handler/family.go:381` `SwitchFamily`，测试 `auth_test.go:431` 覆盖"切到非本家庭 403 + 落审计"）
- 执行状态：未执行
- 偏差登记：DEV-01（差异类型：路径单复数；关闭方式见 `docs/cases/DEVIATIONS.md`，派卡 C-H-01）
- 判据说明：**标 `契约偏差` 而非 `已实现`**——按 PRD 判据，路径不符即不通过；但代码主体在，不应重复派卡重建，只需对齐路径（含旧路径兼容期）

## HOM-SESS-005 · 登录与会话记录保留（22.4）+ 登录审计（22.5）
- 关联需求：PRD 22.4:2556（"登录与会话记录 | 6 个月"）、22.5:2558（审计事件九类之首为"登录"）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S3
- 前置条件：完成若干次成功登录与若干次验证码错误/refresh 重放
- 步骤：
  1. 触发成功登录、验证码失败、refresh 重放三类事件
  2. 以管理员身份查 `GET /api/homeos/audit-events` 按"登录"类过滤
  3. 检查会话记录表的保留策略
- 预期结果：
  - 三类事件均落 `homeos_audit_log`，带 `member_id`、`family_id`、结果、时间
  - 管理员可读本家庭条目；普通成员只能读**自己触发**的条目（PRD 3.7:435）
  - 会话记录保留 6 个月，超期清理；解散家庭按 30 天冷存窗口另行处置（与本条不混）
- 实现现状（快照）：未实现（**写入侧已有**：`handler/auth.go:280` 与 `:311` 以 `AuditEventLogin` 落审计；**读取端点 `GET /api/homeos/audit-events` 未落地**，见 `homeos/admin-ops.md` HOM-ADM-001 与 IMPL-17；**保留期清理任务不存在**，见 IMPL-23）
- 执行状态：未执行
- 判据说明：本条是「部分实现」——第 1 条已满足，第 2、3 条不满足，故整体标 `未实现`，不拆条、不改判据

## HOM-SESS-006 · 无账号被记录成员禁止登录（安全负向）
- 关联需求：PRD 3.4.1:263（"允许无账号成员（`Member.user_id` 可为空）…**无账号成员不可登录、不出现在邀请列表**"）
- 类型：安全 | 优先级：P0 | 建议归属 Sprint：S3
- 前置条件：家庭内存在一个 `user_id` 为空的被记录成员（如儿童），并已被监护人建档
- 步骤：
  1. 尝试以该成员的姓名/手机号发起验证码登录
  2. 在邀请成员列表中查找该成员
  3. 由监护人以其名义写入一笔代管记录（`on_behalf_of`）
- 预期结果：
  - 该成员**无法登录**（无 `user_id` 即无账号主体），且不因"建档完整"而被授予凭据
  - 邀请列表中**不出现**该成员
  - 代管写入成功但审计记录标注 `on_behalf_of`（22.5 九类之一），写入人 = 监护人而非被记录者
- 实现现状（快照）：已实现(HTTP)（登录身份只经手机号查 `homeos_users`：`handler/auth.go:192` `Login` → `repo.FindOrCreateUser(phone)`；无账号成员在 `homeos_members.user_id` 为空、`homeos_users` 无对应行，故不存在"以该成员身份登录"的路径；邀请列表见 `main.go:212` `GET /family/invites`）
- 执行状态：未执行
- 边界说明（**已知残留风险，不掩盖**）：若建档时给该成员录了手机号，`FindOrCreateUser` 会为**该号码**新建一个独立 `user`——这不等于"以该成员身份登录"（新 user 不在原家庭），但会形成"同一人两个身份"的歧义。判据层面本条成立，产品层面需在成员建档流程约束"无账号成员不得填写手机号"，属 SOP 级约束，不单独立 case
