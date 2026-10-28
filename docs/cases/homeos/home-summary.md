# HomeOS · 首页四区聚合（`home/summary`）

> 范围：首屏单请求聚合 A/B/C/D 四区、`due_today.count` 与 `items[]≤3`、`faces[]` 四层合成、停用/撤销面剔除、`unread` 同源、`period` 三档。
> 权威源：`docs/prd-homecube.md` 3.7:430（首页四区 + 未读单请求聚合，接受 `period`）、18.2:2147-2155（四区定义与 `due_today` 口径）、18.8:2233-2234（已挂载面集合 / 该成员可见的面集合）、18.1:2133（未读只有一个口径）、4.5.3（时间窗三档、定版 ⑬）、15.5#8:1874；`docs/p1-tech-plan.md:457`（S5 关键卡：`faces[]` 四层合成、`unread`、A/B/D 三区供数）。
> 本文件 case 前缀 `HOM-SUM`。**`faces[]` 按「四层」断言**：registry 面目录 × `homeos_family_module` × 服务出生状态 × 当前成员角色的 `scope=module` 判定，四层同一处算完。

---

## HOM-SUM-001 · 首屏四区由单请求聚合（客户端不并行拼装）
- 关联需求：PRD 3.7:430（**客户端不得并行请求多个服务拼装**）、18.2:2147-2153（A 区不另发 `family` 与 `members` 请求）、18.2「首屏一个请求」
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S5
- 前置条件：家庭有成员、今日有到期项、有动态
- 步骤：进入首页，抓取网络请求
- 预期结果：
  - A/B/C/D 四区数据全部来自一次 `GET /api/homeos/home/summary`
  - **不出现**并行的 `family`、`members`、`notifications`、`dynamics` 请求
  - 返回结构含 `family`（含 `members[].relation` 与全量成员计数）、`due_today`、`faces[]`、`dynamics.items[]`、`unread`
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/home_summary_test.go:649` 服务端计数与四区结构）
- 执行状态：未执行

## HOM-SUM-002 · 引导未完成态拒绝访问首页聚合
- 关联需求：PRD 15.5#8:1874（强制选面引导，完成后首页矩阵按所选集合渲染）、18.8
- 类型：异常 | 优先级：P1 | 建议归属 Sprint：S5
- 前置条件：账号已登录但未完成建家/选面引导
- 步骤：直接调用 `GET /api/homeos/home/summary`
- 预期结果：返回业务错误（拒绝），不返回半个首页结构；引导完成后可正常返回
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/home_summary_test.go:604` `RefusesOnboarding`）
- 执行状态：未执行

## HOM-SUM-003 · `due_today.count` 全量、`items[]` 只给前 3 条
- 关联需求：PRD 3.7:430（`count` 为全量、`items[]` 只给前 3 条）、18.2:2155（定版：两者可以不等，客户端不自行截断、不补第 4 条）
- 类型：边界 | 优先级：P0 | 建议归属 Sprint：S5
- 前置条件：今日到期与待办共 5 项
- 步骤：请求首页聚合
- 预期结果：
  - `count = 5`
  - `items[]` 长度为 3，且按 `due_at` 升序
  - 今日仅 1~2 项时 `items[]` 按实际条数返回，不填空位
  - 客户端不补第 4 条、不自行截断
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/home_summary_test.go:760` `DueTodayOmitsCount` 等）
- 执行状态：未执行

## HOM-SUM-004 · `faces[]` 四层合成，只返回该成员可见的一份
- 关联需求：PRD 18.8:2233-2234（已挂载面集合 = 交集 ∩ 服务已出生；该成员可见面集合 = 已挂载 ∩ `scope=module` 判定；**客户端不得自行按角色过滤**）、3.7:430、18.7
- 类型：安全 | 优先级：P0 | 建议归属 Sprint：S5
- 前置条件：家庭启用 homeos + finance；分别以 owner / 儿童角色访问
- 步骤：对比两种角色拿到的 `faces[]`
- 预期结果：
  - `faces[]` = registry 面目录 × `homeos_family_module` × 服务出生状态 × 角色 `scope=module` 判定
  - owner 视角格数 = 本家庭已启用面数
  - 儿童角色拿到的是裁掉无权面后的集合，且服务端已裁完（响应里不存在需客户端再过滤的字段）
  - `family/modules` 的 `visible` 字段与 `faces[]` 用同一判定
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/home_summary_test.go:383` `FacesEqualMountedFaces`）
- 执行状态：未执行

## HOM-SUM-005 · 停用面 / 撤销到期项从矩阵与今日区剔除
- 关联需求：PRD 18.8（停用面入口消失、不置灰不占位）、17.4（到期注册撤销）、19.2#9
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S5
- 前置条件：启用 finance → 产生到期项 → 停用 finance
- 步骤：停用后重新请求首页聚合
- 预期结果：
  - `faces[]` 中 finance 格子消失（不置灰、不占位）
  - `due_today` 中来自 finance 的项被剔除，`count` 同步减少
  - 该面历史数据保留，统计口径不变
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/home_summary_test.go:869` `DropsRevoked`、`:929` `FilteredToMountedFaces`）
- 执行状态：未执行

## HOM-SUM-006 · `unread` 与通知接口同源（一个口径、一份状态）
- 关联需求：PRD 18.1:2133（`read_at IS NULL` 按 `member_id` 聚合；客户端只持有一份未读状态）、3.7:423（`notifications` 与 `home/summary` 的 `unread` 同口径）、18.7 第 8 条
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S5
- 前置条件：有 3 条未读
- 步骤：
  1. 读 `home/summary` 的 `unread`
  2. 读 `GET /api/homeos/notifications` 的 `unread`
  3. 标记全部已读后再读两处
- 预期结果：
  - 标记前两处均为 3，标记后两处均为 0
  - 顶栏红点与 D 行动态位读同一份状态，不各存一份、不缓存第三份
  - 类型筛选不改变 `unread` 数值
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/notifications_test.go:166` `UnreadByTypeKeys`、`:180` 类型筛选不移动未读；`home_summary_test.go:649` 同源计数）
- 执行状态：未执行

## HOM-SUM-007 · `period` 三档贯通，非法值 400 且不回落
- 关联需求：PRD 3.7:430（接受 `period`：月/季/年三档编码，定版 ⑬）、4.5.3、19.3#9
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S5
- 前置条件：同一家庭有跨月数据
- 步骤：
  1. 依次以 `YYYY-MM` / `YYYY-Qn` / `YYYY` 请求
  2. 提交非法 `period`（如 `2026-13`、`2026Q5`）
- 预期结果：
  - 三档均返回，且与流水页、报表页、预算页同一时间窗的数值一致
  - 非法值返回 **400**，**不静默回落到月档**
  - 首页恒渲染时间窗条，切换即重拉，四处同源
- 实现现状（快照）：已实现(HTTP)（`server/services/svc-homeos/internal/handler/home_summary_test.go:508` `TestHomeSummaryPeriodIllegalIsFourHundredAndNeverFallsBack`——断言非法 `period` 一律 400 且**不回落默认时间窗**）
- 执行状态：未执行

## HOM-SUM-008 · A 区问候语回落链与头像排溢出（前 3 + N）
- 关联需求：PRD 18.2:2150（问候语 `{时段}好，{称谓}`，时段按家庭时区、称谓取 `Member.relation`，⑭；成员头像排**前 3 个 + 溢出「+N」**，N = 未画出成员数含无账号成员；整排点击进成员管理；A 区不放家庭切换器）、19.2#12②:2308（`relation` 缺值/空串/有值三种样本各 20 次；两者皆空回落家庭称谓通用文案，**且不因此发起 svc-kin 或任何关系表请求**）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S14
- 前置条件：三种成员样本（`relation` 有值 / 空串 / 无值）；家庭 6 人含 2 个无账号成员
- 步骤：
  1. 三种样本各 20 次进入首页，记录问候语
  2. 检查头像排与「+N」数值
  3. 抓包检查是否请求了关系表或 `svc-kin`
- 预期结果：
  - 有值 → 用 `relation`；空串/无值 → 回落成员名；两者皆空 → 回落通用文案
  - 头像排前 3 个 + 「+N」，N 正确（含无账号成员）；无账号与有账号成员呈现一致，不画虚线/灰度区分
  - 整个 A 区渲染**不发 `svc-kin` 或关系表请求**；A 区无家庭切换器
- 实现现状（快照）：待自动化（首页已渲染 A 区，回落链与「不发关系请求」未断言）
- 执行状态：未执行

## HOM-SUM-009 · D 区动态流：前 20 条、服务端整句、可见性裁剪
- 关联需求：PRD 18.2:2153（D 区：动态流前 20 条 + 「查看全部」；条目 = 归属面图标 + 人名 + 一句动作描述 + 相对时间）、18.2:2159（文案由服务端拼好整句下发，客户端只格式化）、18.2:2160（**可见性由服务端裁剪**：被裁掉的条目不返回、不占位、不显示「无权限查看」；客户端不得自行过滤）、3.7:424（`GET /dynamics` 按天分组、按面筛选、游标分页；首页取前 20、动态页续读同一接口）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S14
- 前置条件：动态 >20 条，含 1 条私密流水产生的动态、1 条被停用面的历史动态
- 步骤：
  1. 首页取 D 区，核对条数与顺序
  2. 以非作者身份查看私密流水动态
  3. 检查首页与动态页是否同一接口、续读是否连续
- 预期结果：
  - 首页取前 20 条；「查看全部」进动态页续读**同一接口**，游标连续不重复不丢
  - 文案为服务端整句；客户端不拼动词、不二次聚合
  - 私密动态对被裁剪者**不返回、不占位、不显示「无权限」**；停用面的历史动态仍在（停用是入口开关，非数据开关）
- 实现现状（快照）：待自动化（`GET /dynamics` 已实现，裁剪与整句下发未断言）
- 执行状态：未执行

## HOM-SUM-010 · 该角色可见面为 0 时，C 区整区不渲染
- 关联需求：PRD 18.8:2243（儿童与访客按「该成员可见的面集合」裁剪后**可以是 0 格**；此时 **C 区整区不渲染**，A/B/D 照常，不出现空宫格、不出现「暂无可用面」占位卡）、19.2#9③:2305（0 格时页面上不出现任何「无权限」字样）
- 类型：边界 | 优先级：P1 | 建议归属 Sprint：S14
- 前置条件：访客账号（无任何面 `scope=module` 权限）；儿童账号同理
- 步骤：
  1. 访客进入首页，截图与取 DOM
  2. 检查 A/B/D 区是否照常
  3. 检查页面是否出现「无权限」「暂无可用面」「未启用」等字样
- 预期结果：
  - C 区**整区不渲染**（不是 0 高度占位、不是空宫格）
  - A 家庭区、B 今日区、D 动态区照常渲染
  - 页面**不出现任何「无权限」字样**；`faces[]` 为空数组而不是缺字段
- 实现现状（快照）：待自动化（0 格态无专项断言）
- 执行状态：未执行
