-- Migration: 0010_time_collab
-- Purpose: Create the four 「HomeOS · 时间与协作对象」 tables PRD 3.7 registers as P1-M1 新建 but
--          that no branch has ever built: homeos_todos / homeos_reminders / homeos_board_messages /
--          homeos_votes. Storage底座 only -- no handler, no GORM model, no contract change in this card.
-- Why this branch exists: 15.3 定版 ㉓ (:1745/:1755) grants every role a cell on these objects
--          (成人 M「创建自己的、可勾选完成、不可删他人」) and 17.4 与 18.2#12③ make 待办/提醒/留言 the
--          only 可建对象 for 儿童与访客 -- a verdict the server cannot compute without an ownership
--          column, a soft-delete column and a version column on each of the four tables.
-- Per PRD 3.6 (:381-383 Todo/Reminder/Vote 行的关键字段与「新增」列)、3.4.2 (:259-262 提醒/待办/周期任务/
--     到期中心)、3.4.3 (:272 留言：家庭留言板、模块内评论)、3.7 (:422 todos 列表三态与「带 version」、
--     :423 reminders 的「时间、周期、提前量、渠道、暂停」、:425 board/messages 的「评论落 homeos」、
--     :426 votes/{id} 与 ballot)、15.3 ㉓ (:1745)、16.4 (:1863 注册字段映射、:1865 幂等键、:1867 删除与
--     完成回写语义)、17.6 (:1980 深链 homecube://{code}/{entity}/{id})、
--     contracts/events/homeos.yaml:142-178 (homeos.todo.completed / homeos.reminder.fired 的 payload)、
--     tech plan §2.2 (:148 无跨服务外键、:149 业务表必备列、:151 索引第一列固定 family_id)。
-- 全部语句 IF NOT EXISTS，可整支重放（判据「重放幂等」，见 0001 头注）。
-- 本支只建这四张表：homeos_due_registration 属 0008、homeos_dynamic*/homeos_notification 属 0007，
-- 一张都不重建，也不动它们的任何列或索引。

BEGIN;

-- ---------------------------------------------------------------------------
-- homeos.homeos_todos
-- PRD 3.6「Todo | id、family_id、member_id、title、due_at、status、source |
-- 新增 source_system、source_id、done_returns_to（完成回写目标）」(:381)。
-- member_id 是创建者（㉓「创建自己的」「不可删他人」判定读它），assigned_to 是分配对象：
-- :422 的三条读路径「分配给我 / 我创建的 / 已归档」是两列而不是一个 owner，合并即少一条路径。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_todos (
    id             uuid         PRIMARY KEY,
    -- §2.2「索引第一列固定 family_id」+ 15.2「判定始终以 token 内的 family_id 为界」。
    -- 不加 REFERENCES：0007:22-24 已定这条——homeos_families 虽同 schema，解散家庭冷存（21.4）
    -- 与「家庭删除后对象留在库里」都要在配置行之后动，物理外键会把顺序钉死。本卡同一取舍。
    family_id      uuid         NOT NULL,
    -- 创建成员（3.6:381 的 member_id）。NOT NULL：㉓ 的 M 格全靠这一列判定。
    member_id      uuid         NOT NULL,
    -- 分配对象（16.4:1849「diet.Menu 做饭任务 Todo（分配 assigned_to）」、5.5.1:730「认领实现为
    -- HomeOS Todo 分配给成员」）。可空 = 尚未分配，「分配给我」列表天然不取 NULL 行。
    assigned_to    uuid,
    -- 子任务用自引用（3.4.2:260「待办：创建、分配、完成、归档、子任务」——本卡口径明确不另建子任务表）。
    parent_id      uuid,
    -- 标题长度取 0008:38 的 varchar(200) 同一口径（注册字段映射 title 与首页 B 区都读它）。
    title          varchar(200) NOT NULL,
    -- 截止时刻：可空（「今日到期与待办」只取有 due_at 的行，手工无截止的待办仍是合法对象，3.5:356）。
    due_at         timestamptz,
    -- 周期规则：16.4 把 CareTask/Chore.schedule（:1854）、Habit.frequency（:1858）注册成 Todo，
    -- 所以待办可带重复规则。
    -- 形态取 jsonb 而不是文本：3.4.2:261 一条规则至少带 频率(天/周/月/年/自定义) + 跳过 + 顺延 三个部件，
    -- 且 CalendarEvent 也有同名列 repeat（3.6:375），两表必须同形。
    repeat         jsonb,
    -- status 的三个取值逐个有出处：创建->pending、完成->done、归档->archived（3.4.2:260）。
    -- 这是文档里唯一闭合的一处（同一行只列这四个动词，「分配」落 assigned_to 不是状态），
    -- 所以本列是四张表里唯一加 status CHECK 的——与 0007 code 列「新增一面 = registry 改一行」
    -- 那条不加 CHECK 的纪律不冲突：面清单是开放的，本状态机不是。
    -- 没有 doing/in_progress：PRD 未定义「进行中」态，handler 卡若需要，走 ALTER ADD CONSTRAINT（同 0008:34）。
    status         varchar(20)  NOT NULL DEFAULT 'pending'
                                     CHECK (status IN ('pending', 'done', 'archived')),
    -- 勾选完成的落点：两列的名字与时区取自冻结契约 homeos.todo.completed 的 payload
    -- （contracts/events/homeos.yaml:158-159 的 completed_at / completed_by），回写目标见 done_returns_to。
    completed_at   timestamptz,
    completed_by   uuid,
    -- 3.6:381 的 source = 录入形态，取值取 3.5:358「全局 +：语音、拍照、文字、模板」，默认文字键入。
    -- **不加 CHECK**：P6 的 AI 归类（:411）与 16.4 的事件注册（低库存自动生成「补货 X」）都会往同一列
    -- 追加取值，焊进 DDL 就是 0007:26-28 那条纪律要避免的「新增一个取值 = 改迁移」。
    source         varchar(20)  NOT NULL DEFAULT 'text',
    -- 16.4 注册三元组的来源两列（3.6:381 新增）。可空：家庭成员自己建的待办没有来源域。
    -- 不加 REFERENCES：§2.2:148「引用他域对象只存 id，无物理约束」。
    source_system  varchar(20),
    source_id      uuid,
    -- 完成回写目标（3.6:381、16.4:1867「回写业务面对象状态（done_returns_to 指定的回调）」）。
    -- jsonb：一个回写目标至少要带 归属对象 + 回写字段/事件名 两件，且各面形态不同（Bill.status、
    -- 库存入库、打卡）；文档没有给它封闭结构，所以只约束它是 object 而不是数组/标量。
    done_returns_to jsonb,
    -- ㉓ 第 4 列「由监护人代写（on_behalf_of）」（:1745）：actor 是代管人、被记录对象是本列。
    -- 同 0007:81-83 homeos_dynamic.on_behalf_of 的取舍与命名。
    on_behalf_of   uuid,
    -- §2.2:149 必备列。:422 明确 todos 编辑「带 version」，PUT 与库值不符即 409。
    version        bigint       NOT NULL DEFAULT 1,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    -- 软删（§2.2:149）。语义边界来自㉓：成人/儿童/访客「不可删他人」，所以删除动作永远有一个执行成员，
    -- deleted_by 非空时即那个成员；管理员（A 格含删）删他人条目也写自己。
    deleted_at     timestamptz,
    deleted_by     uuid
);

COMMENT ON COLUMN homeos.homeos_todos.repeat IS
    '周期规则，与 homeos_calendar_event.repeat 同形（PRD 3.6:375、3.4.2:261）。';

-- 读路径①「分配给我」（:422）：家庭 + 被分配人，按截止时刻升序（今日时间轴卡 3.5:356 同形）。
CREATE INDEX IF NOT EXISTS homeos_todos_family_assigned_due_idx
    ON homeos.homeos_todos (family_id, assigned_to, due_at ASC)
    WHERE deleted_at IS NULL;

-- 读路径②「我创建的」（:422）：归属判定列即㉓ 的判定列，建列表与鉴权是同一条索引。
CREATE INDEX IF NOT EXISTS homeos_todos_family_member_idx
    ON homeos.homeos_todos (family_id, member_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- 读路径③「已归档」（:422）：部分索引只装归档行，活跃列表不被历史撑大；排序取 updated_at
-- （归档时刻就是最后更新时间，PRD 未给独立的 archived_at 列，不自造）。
CREATE INDEX IF NOT EXISTS homeos_todos_family_archived_idx
    ON homeos.homeos_todos (family_id, updated_at DESC)
    WHERE deleted_at IS NULL AND status = 'archived';

-- 到期中心/首页 B 区「今日到期与待办」（:262、3.5:356、:455 已挂载面过滤）：只扫未完成行的时间窗。
CREATE INDEX IF NOT EXISTS homeos_todos_family_due_open_idx
    ON homeos.homeos_todos (family_id, due_at ASC)
    WHERE deleted_at IS NULL AND status = 'pending';

-- 子任务展开（:260）：一个父待办的子项集。
CREATE INDEX IF NOT EXISTS homeos_todos_parent_idx
    ON homeos.homeos_todos (parent_id)
    WHERE deleted_at IS NULL AND parent_id IS NOT NULL;

-- 注册幂等键（16.4:1865「注册幂等键为 (source_system, source_id, kind)」——kind 在本表恒为 todo，
-- 所以键降到两列）。部分于 deleted_at IS NULL 的理由照 0008:50-54：来源对象删除后可重新注册
-- （库存再次触低阈值就是新的一条），历史软删行不得永久占住槽位。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_todos_source_uidx
    ON homeos.homeos_todos (source_system, source_id)
    WHERE source_id IS NOT NULL AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- homeos.homeos_reminders
-- PRD 3.6「Reminder | id、owner_id、type、trigger_at、repeat、status |
-- 新增 calendar_event_id」(:382)。
-- 3.6 的这行没有 family_id，但本表必须有：§2.2:149「业务表必备列 …… family_id uuid not null」，
-- 且没有它就挡不住跨家庭的 id 猜测——与 0007:132-133 给 homeos_notification 补 family_id 同一理由。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_reminders (
    id                uuid         PRIMARY KEY,
    family_id         uuid         NOT NULL,
    -- 3.6:382 的归属列就叫 owner_id（不是 member_id）：本卡按原文保留列名，㉓ 的「创建自己的」读它。
    owner_id          uuid         NOT NULL,
    -- 站内信的 content 由发布方拼；家庭成员自建提醒没有 source_system/source_id，文案就没有出处，
    -- 故补这一列（命名与 16.4:1863 注册字段映射的 title 一致；契约 payload 见 :174-178）。
    title             varchar(200) NOT NULL,
    -- type 的四值逐字取自 3.4.2:259「提醒：单次、周期、位置、到期」。
    -- CHECK 而非枚举类型：同 0008:34 的取舍。注意与 homeos.reminder.fired payload 里那个 type 不同物
    -- （那个是 3.6:379 Notification 的三值通知分型），两列同名异义已在本卡上报里记下。
    -- 位置提醒（location）受 3.4.2:267「默认降级为地理围栏 + 失败即回落到时间提醒」约束，
    -- 围栏列随该能力落地再 ALTER，本支不预留经纬度空列（不留没有写方的列，同 0008:21-23）。
    type              varchar(20)  NOT NULL
                                         CHECK (type IN ('once', 'recurring', 'location', 'due')),
    -- 触发时刻（3.6:382）。周期提醒存下一次要触发的时刻，展开规则读 repeat。
    trigger_at        timestamptz  NOT NULL,
    -- 与 homeos_todos.repeat 同形（3.6:375、3.4.2:261：每天/周/月/年、自定义、跳过、顺延）。
    repeat            jsonb,
    -- 提前量（:423 提醒编辑项「提前量」，16.4 触发点列的「到期前 3 天 / 前 30 天」即它的取值）。
    -- 用分钟而不是 interval：16.4 的触发点全部是「前 N 天/小时」，integer 可直接与 trigger_at 相减。
    advance_minutes   integer,
    -- 渠道（:423 编辑项「渠道」）：取值口径同 3.6:379 Notification 的 channel（push/站内/@），
    -- 与 0007:143-144 同一列名、同一默认值、同一「不 CHECK」处理（P1 只有站内信一路）。
    channel           varchar(20)  NOT NULL DEFAULT 'inapp',
    -- status 只有两值，且各有一条文档动作：暂停 = paused、正常 = active（:423「提醒列表与编辑
    -- （时间、周期、提前量、渠道、暂停）；暂停与删除发 {code}.due.revoked」）。
    -- 「删除」不进 status：同一行把 暂停 与 删除 分成两件事，删除的落点是下面的 deleted_at。
    -- 触发历史不进 status：homeos.reminder.fired 的 business_id 是 {reminder_id}:{fire_date}
    -- （契约 :163），每一次触发是一条独立事件而不是把提醒改成 done，否则同一提醒只能响一次。
    status            varchar(20)  NOT NULL DEFAULT 'active'
                                        CHECK (status IN ('active', 'paused')),
    -- 3.6:382 的新增列。可空：家庭成员自建提醒不挂日历事件。
    -- 不加 REFERENCES，也不只是「他域只存 id」这一条：**本序列里目前没有日历事件表**
    -- （0007/0008 只建了 homeos_due_registration，:421 的 calendar/events 接口同样没有落点），
    -- 建物理外键会指向一个不存在的关系。已上报，见本卡报告的「缺表」条。
    calendar_event_id uuid,
    -- 16.4 把 finance.Bill/Loan/purchase.Batch/trip/kin/growth 的到期对象注册成 Reminder：
    -- 来源两列是注册幂等键与「前往源对象」深链的落点（:421、:1863、:1867）。
    source_system     varchar(20),
    source_id         uuid,
    on_behalf_of      uuid,
    version           bigint       NOT NULL DEFAULT 1,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    -- 删除路径是 {code}.due.revoked（:423），执行方可能是他服务的撤销而不是某个成员，
    -- 所以 deleted_by 可空——与 0008:43-46 完全同一口径。
    deleted_at        timestamptz,
    deleted_by        uuid
);

-- 提醒列表（GET /api/homeos/reminders，:423）：某成员的提醒按下次触发升序。
CREATE INDEX IF NOT EXISTS homeos_reminders_family_owner_trigger_idx
    ON homeos.homeos_reminders (family_id, owner_id, trigger_at ASC)
    WHERE deleted_at IS NULL;

-- 触发器扫描：本服务是唯一触发方（:265「HomeOS 是唯一聚合与触发方」），
-- 「已暂停」的行不触发（:423 暂停发 due.revoked），故把它排除在部分索引之外而不是留在表里再过滤。
CREATE INDEX IF NOT EXISTS homeos_reminders_family_active_trigger_idx
    ON homeos.homeos_reminders (family_id, trigger_at ASC)
    WHERE deleted_at IS NULL AND status = 'active';

-- 由日历事件反查挂在它上面的提醒（3.6:382 的 calendar_event_id 唯一用途；日历事件删除时级联失效，:1867）。
CREATE INDEX IF NOT EXISTS homeos_reminders_calendar_event_idx
    ON homeos.homeos_reminders (calendar_event_id)
    WHERE deleted_at IS NULL AND calendar_event_id IS NOT NULL;

-- 注册幂等键：与 homeos_todos 同一条 16.4:1865 规则（kind 降到 reminder）。
-- 注意它与 homeos_due_registration 的唯一键（0008:52）不重复：那张表是「到期中心」的聚合视图，
-- 本行是提醒对象自身，16.4 明确一个业务对象可以同时注册成 Reminder 与 CalendarEvent（:1842 的
-- finance.Bill.due_at 行）。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_reminders_source_uidx
    ON homeos.homeos_reminders (source_system, source_id)
    WHERE source_id IS NOT NULL AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- homeos.homeos_board_messages
-- 留言板对象在 3.6 数据模型表里没有行，字段只能从登记面与㉓ 取：
--   :425「家庭留言板列表与写入（3.4.3）；评论落 homeos，产生的动态经事件，不由业务面直写」
--   3.4.3:272「留言：家庭留言板、模块内评论」
--   ㉓:1745 访客格「M（留言 + 勾选完成，不可删）」、成人格「可回应他人条目」
-- 因此本表的形状是：家庭时间序（列表）+ 作者列（删他人判定）+ parent_id（回应）
--   + (entity, entity_id)（模块内评论的锚点）。
-- 动态不在本表写：:425 明确「产生的动态经事件，不由业务面直写」，落点是 0007 的 homeos_dynamic。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_board_messages (
    id         uuid        PRIMARY KEY,
    family_id  uuid        NOT NULL,
    -- 作者（㉓ 的「创建自己的」「不可删他人」判定列；留言板在 3.6 没有行，列名沿用 Todo 的 member_id，
    -- 不新造第三种叫法）。
    member_id  uuid        NOT NULL,
    -- 「可回应他人条目」（:1745）与「模块内评论」（:272）都不是新表能表达的形状之外的东西，
    -- 但两者是两件不同的事：parent_id 指向被回应的那条留言本身。
    parent_id  uuid,
    -- 锚点：评论挂在某个业务面对象上时用这两列，形态取 17.6:1980 的深链
    -- homecube://{code}/{entity}/{id} 的 {entity}/{id} 两段（列名与 0007:77-79 homeos_dynamic 一致）。
    -- {code} 不另存一列：本列组语义是「评论指向哪个对象」，而 homeos_dynamic.code 是「动态归属哪个面」，
    -- 两者不同物（:425 的留言动态一律归 homeos），handler 要面归属时按 entity 反查 registry。
    entity     varchar(50),
    entity_id  uuid,
    -- 正文：text 而不是 varchar(200)——标题类短标签才限长（0008:38 的 title），留言是自由文本。
    -- 二十章/L3：正文不得携带 L3 分级内容，与 0007:142 homeos_notification.content 同一条纪律；
    -- 分级抽检按定版 ㉔ 只认 contracts/data-levels.yaml，本卡不加 CHECK（长度与内容都不是 DDL 能判的）。
    content    text        NOT NULL,
    version    bigint      NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- updated_at 有写方：㉓ 的成人格是「创建与编辑自己的」，编辑 = 本列 + version+1。
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- 软删：㉓「不可删他人」的前提是「可删自己的」，管理员 A 格含删（㉓ 注）。
    deleted_at timestamptz,
    deleted_by uuid,
    -- 锚点两列必须同时有或同时无：只带 entity_id 不带 entity 的行无法生成深链，
    -- 只带 entity 的行指向不了任何对象。这是结构约束而不是取值枚举，与「不加 CHECK」纪律无关。
    CONSTRAINT homeos_board_messages_target_pair_chk
        CHECK ((entity IS NULL) = (entity_id IS NULL))
);

-- 留言板列表（GET /api/homeos/board/messages，:425）：家庭 + 时间倒序，分页取一页。
CREATE INDEX IF NOT EXISTS homeos_board_messages_family_created_idx
    ON homeos.homeos_board_messages (family_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- 鉴权判定路径：删/改自己的条目要按 (家庭, 作者) 取行（㉓:1745）。
CREATE INDEX IF NOT EXISTS homeos_board_messages_family_member_idx
    ON homeos.homeos_board_messages (family_id, member_id)
    WHERE deleted_at IS NULL;

-- 模块内评论（:272）：某个业务对象下的评论列表，仍按时间序。
CREATE INDEX IF NOT EXISTS homeos_board_messages_target_idx
    ON homeos.homeos_board_messages (entity_id, created_at DESC)
    WHERE deleted_at IS NULL AND entity_id IS NOT NULL;

-- 「可回应他人条目」：一条留言的回复集（:1745）。
CREATE INDEX IF NOT EXISTS homeos_board_messages_parent_idx
    ON homeos.homeos_board_messages (parent_id)
    WHERE deleted_at IS NULL AND parent_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- homeos.homeos_votes
-- PRD 3.6「Vote | id、family_id、question、options、mode、deadline |
-- 供饮食点菜、家人轮值引用，业务面不自建投票表」(:383)。
-- 业务面不自建 = 这张表是七个域唯一的投票载体，所以它必须同时容得下 P3 点菜与 P4 轮值两种形状，
-- 这直接决定了 options 的类型选择（见下）。member_id 在 3.6 的这行里没有，但㉓ 的 M 格判定
-- 要求「创建自己的」可判，故补作者列——同 Reminder 补 family_id 的处理（本文件上方注释）。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_votes (
    id          uuid         PRIMARY KEY,
    family_id   uuid         NOT NULL,
    member_id   uuid         NOT NULL,
    question    varchar(200) NOT NULL,
    -- options = jsonb 数组，而不是 text[]。三条文档依据：
    --   ① 3.6:383 的用法是「供饮食点菜、家人轮值引用」——一个选项要指向一个业务对象
    --     （一道菜、一个人），text[] 只能装标签，装不下那个引用；
    --   ② :426 的投票动作 POST votes/{id}/ballot 要落到具体选项上，选项必须有稳定 id，
    --     而 text[] 的元素改一次文案就换了一个值，历史票就指向了错误的选项；
    --   ③ 与同卡的 repeat / done_returns_to 同一类型口径（结构化部件一律 jsonb）。
    -- 只 CHECK 它是数组：选项的内部结构由 P3/P4 的消费方定义，现在焊 schema 就是替没出生的面定契约。
    options     jsonb        NOT NULL CHECK (jsonb_typeof(options) = 'array'),
    -- mode 在 3.6 只给了列名，全文没有给它取值集合（点菜是单选还是多选、轮值是否加权都未定版），
    -- 所以**不加 CHECK**：按 0007:26-28 的纪律，把一个文档没闭合的枚举写进 DDL，
    -- 将来每加一种模式就是改迁移而不是改一行登记。NOT NULL 保留——不存在「没有模式的投票」。
    mode        varchar(20)  NOT NULL,
    -- 截止（3.6:383）。可空：轮值类投票按 :426「页面与创建入口随饮食（P3）/家人（P4）出生」，
    -- 现在没有任何写方给过非空 deadline 的承诺。开/关状态由本列与当前时刻比较得出，
    -- 不另存 status：PRD 没给 Vote 状态列，deadline 就是唯一的时间语义。
    deadline    timestamptz,
    on_behalf_of uuid,
    version     bigint       NOT NULL DEFAULT 1,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    -- 软删：㉓ 给管理员的 A 含删与导；投票行的删除只应出现在治理动作里，
    -- 因此 deleted_by 语义上非空（执行成员），但列仍可空——与 0008:43-46 同一处理。
    deleted_at  timestamptz,
    deleted_by  uuid
);

-- 判据要求的读路径：某家庭的投票按 deadline 排序（今晚吃什么/本周轮值都取它）。
-- 部分于 deleted_at IS NULL；「未过期」那一半不进索引谓词，因为 deadline > now() 不是 immutable，
-- Postgres 不允许它做部分索引条件（这是取舍，不是遗漏）。
CREATE INDEX IF NOT EXISTS homeos_votes_family_deadline_idx
    ON homeos.homeos_votes (family_id, deadline ASC)
    WHERE deleted_at IS NULL;

-- ㉓ 判定 + 「我发起的投票」列表：与 homeos_todos/homeos_board_messages 同一形状。
CREATE INDEX IF NOT EXISTS homeos_votes_family_member_idx
    ON homeos.homeos_votes (family_id, member_id)
    WHERE deleted_at IS NULL;

COMMIT;
