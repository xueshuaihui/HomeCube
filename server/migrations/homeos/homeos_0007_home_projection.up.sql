-- Migration: 0007_home_projection
-- Purpose: Create the four home/dynamics/message read models svc-homeos is still missing:
--          per-family face mount config, the dynamics feed + its per-member read receipts,
--          and the message-center entries.
-- Per PRD 3.6 (FamilyModule / Dynamic / Notification 三行的关键字段)、17.1#2 (未读只有一个口径)、
-- 17.2 (首页时间窗条 + A/B/C/D 四区)、17.8 (面目录与家庭面配置：无行即未启用、version 乐观锁、
-- 仅剩最后一个已启用面时停用被拒)、技术方案 §2.3 与 §六 (投影只由本服务订阅器写入、无跨服务外键)。
-- 全部语句 IF NOT EXISTS，可整支重放（判据「重放幂等」，见 0001）。
-- 本支只建 homeos schema 自己的对象：homeos_proj_finance 已是只读投影（0004），
-- 动态流消费它、不反写它，也不跨 schema 读 finance_ 任何表（§六、PRD 16.3）。

BEGIN;

-- ---------------------------------------------------------------------------
-- homeos.homeos_family_module
-- PRD 3.6「FamilyModule | family_id、code、enabled、enabled_at、enabled_by、version」
-- PRD 17.8「一行一个 (家庭, 面)，无行即未启用」「写 FamilyModule（version 乐观锁）」。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_family_module (
    id         uuid         PRIMARY KEY,
    -- §2.2「索引第一列固定 family_id」+ 15.2「判定始终以 token 内的 family_id 为界」。
    -- 他域对象一律「只存 id，无物理约束」（0004 头注、§2.2「不存在跨服务外键」）；
    -- homeos_families 虽在本 schema，本卡同样不加 REFERENCES：强制选面引导（17.8）与
    -- 解散家庭冷存（21.4）都要在配置行之上做批量动作，物理外键会把那两条路径的顺序钉死。
    family_id  uuid         NOT NULL,
    -- 面 code = registry 登记的七类命名域之一（16.1）。**不加 CHECK**：17.8「新增一面 = 追加
    -- 一条 registry 条目，不改动导航与配置的既有结构」，把 code 枚举焊进 DDL 就违反了这条，
    -- 且门禁 4 把「硬编码面清单」判失败。
    code       varchar(20)  NOT NULL,
    -- 停用 = 入口消失 + 数据保留（17.8 停用语义），所以这是一个状态位而不是删除动作。
    -- NOT NULL 是「仅剩最后一个已启用面时停用被拒」那条计数检查所读的列（17.8 第 4 条）。
    enabled    boolean      NOT NULL DEFAULT false,
    -- PRD 3.6 的两个审计列：谁在什么时候开的通。停用时不清空它们（保留最近一次开通痕迹），
    -- 17.8 只要求「变更即落审计」，明细在 homeos_audit_log（另一支卡）。
    enabled_at timestamptz,
    enabled_by uuid,
    -- §2.2「业务表必备列：version bigint not null default 1」+ 17.8「version 乐观锁」。
    -- PUT family/modules 带的 version 与本列不符即 409。
    version    bigint       NOT NULL DEFAULT 1,
    created_at timestamptz  NOT NULL DEFAULT now(),
    -- 软删：17.8「停用不删数据」，本列只为「家庭解散后清出配置视图」留位（21.4 冷存窗口）。
    updated_at timestamptz  NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    deleted_by uuid
);

-- 17.8「一行一个 (家庭, 面)」的字面直译。部分索引：软删后的行不得再占住这个槽位，
-- 否则「解散 → 重建同名面」会被历史行挡住。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_family_module_family_code_uidx
    ON homeos.homeos_family_module (family_id, code)
    WHERE deleted_at IS NULL;

-- 「已启用 N 面」矩阵标题（17.2 C 区）与 family/modules 列表都按家庭整取。
CREATE INDEX IF NOT EXISTS idx_homeos_family_module_family
    ON homeos.homeos_family_module (family_id)
    WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- homeos.homeos_dynamic
-- PRD 3.6「Dynamic | id、family_id、source、action、payload、created_at —— 仅由事件总线写入」。
-- 列名取契约与 17.2 的口径：source -> code（面 code）、payload 拆成服务端拼好的整句 summary
-- （17.2「动态流条目的文案由服务端拼好整句下发」）、created_at -> at（事件发生时刻，不是落库时刻）。
-- 「仅由事件总线写入」这条不变量因此被保留：已读态不落本表，见下面 homeos_dynamic_read。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_dynamic (
    id              uuid         PRIMARY KEY,
    family_id       uuid         NOT NULL,
    -- 归属面 code：D 区条目前缀的面图标与面色令牌、动态流的面筛选项都读它（17.2、⑫ 后五处同源）。
    code            varchar(20)  NOT NULL,
    -- .actor_member_id 可空：系统自己产生的条目（预算超支、到期触发）没有动作成员。
    actor_member_id uuid,
    -- 17.2「条目 = 归属面图标 + 人名 + 一句动作描述（含对象摘要）+ 相对时间」。
    -- 人名与整句都在写入时定稿（事件里带的是当时的快照，事后改昵称不回溯历史条目）。
    actor_name      varchar(100) NOT NULL,
    action          varchar(50)  NOT NULL,
    summary         text         NOT NULL,
    -- 对象摘要的落点：条目指向哪个对象，供 17.6 的深链 homecube://{code}/{entity}/{id}。
    entity          varchar(50)  NOT NULL,
    -- 家庭级条目（创建家庭、接受邀请）无可指对象 -> 可空。
    entity_id       uuid,
    -- 技术方案 §3.4「finance.* 事件写 homeos_dynamic 时标注 on_behalf_of（代管场景，14.5 第 2 项）」：
    -- 代管人替孩子/老人记的账，actor 是代管人、受益对象是本列。
    on_behalf_of    uuid,
    -- 事件发生时刻（家庭时区在展示层格式化，17.2「相对时间的格式属格式化层」）。
    at              timestamptz  NOT NULL,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

-- 实际查询形态：动态流按 (family_id, at desc) 取一页，首页 D 区取前 20 条（17.2）。
CREATE INDEX IF NOT EXISTS homeos_dynamic_family_at_idx
    ON homeos.homeos_dynamic (family_id, at DESC);

-- 带面筛选的同一页（GET /api/homeos/dynamics?face_code=，⑫ 后动态流筛选是面集合的五个消费位之一）。
CREATE INDEX IF NOT EXISTS homeos_dynamic_family_code_at_idx
    ON homeos.homeos_dynamic (family_id, code, at DESC);

-- ---------------------------------------------------------------------------
-- homeos.homeos_dynamic_read
-- 本卡建的第四张表：动态的已读态按 (动态, 成员) 一行一条，而不是 homeos_dynamic 上一个布尔。
-- 两条文档依据逼出这个形状：
--   ① PRD 3.6「Dynamic …… 仅由事件总线写入」——在同一个被订阅器独占写入的表上 UPDATE read_at
--      会把读动作变成第二写方，重建投影（§六「可随时全量重建」）时还会把已读态一起冲掉。
--   ② PRD 17.1 第 2 条「未读只有一个口径：homeos_notification.read_at IS NULL 按 member_id 聚合」
--      + 15.2「判定以 family_id 为界、成员角色再裁一层」——家庭里每个成员的已读进度是各自的，
--      一个全局布尔会让爸爸读过之后孩子就再也看不到未读，且与消息红点那个唯一口径互相污染。
-- 不加 REFERENCES：本表行是缓存性质的读回执，动态行按保留策略清理后回执留在库里无害。
CREATE TABLE IF NOT EXISTS homeos.homeos_dynamic_read (
    id         uuid        PRIMARY KEY,
    family_id  uuid        NOT NULL,
    dynamic_id uuid        NOT NULL,
    member_id  uuid        NOT NULL,
    read_at    timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- 幂等锚点：同一成员对同一条动态只有一条回执（重复标记走 ON CONFLICT DO NOTHING）。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_dynamic_read_dyn_member_uidx
    ON homeos.homeos_dynamic_read (dynamic_id, member_id);

-- 「只看未读」与未读计数：按 (family_id, member_id) 反查该成员的回执集。
CREATE INDEX IF NOT EXISTS homeos_dynamic_read_family_member_idx
    ON homeos.homeos_dynamic_read (family_id, member_id);

-- ---------------------------------------------------------------------------
-- homeos.homeos_notification
-- PRD 3.6「Notification | id、member_id、type、content、read_at；新增 channel（push/站内/@）、
-- dedupe_key；未读 = read_at IS NULL，红点计数按 member_id 聚合，L3 内容不得进入 content」+
-- 技术方案 §「通知分型与未读」：type 是受控枚举 budget_alert/system/reminder，
-- 未读口径唯一为 read_at IS NULL，同一个计数同时供顶栏红点与 D 行「消息」红点 + 计数。
-- family_id 在 3.6 的关键字段里没写，但本表必须有：15.2「一切判定以会话家庭为界」，
-- 没有它就无法在不 join members 的情况下挡住跨家庭的 id 猜测。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_notification (
    id         uuid           PRIMARY KEY,
    family_id  uuid           NOT NULL,
    -- 收件成员，不是发送者：站内信在写入时按成员扇出，未读聚合口径即本列（17.1 第 2 条）。
    member_id  uuid           NOT NULL,
    -- 三值 CHECK 是契约 enum 与技术方案「新增取值须同时改事件目录与客户端文案库」的直译。
    type       varchar(20)    NOT NULL CHECK (type IN ('budget_alert', 'system', 'reminder')),
    -- 二十章/L3：正文不得携带 L3 分级内容，只放指向对象的描述句。
    content    text           NOT NULL,
    -- 3.6 的 channel。P1 只有站内信这一路（技术方案：18.2#2 的成功率以站内信链路测得）。
    channel    varchar(20)    NOT NULL DEFAULT 'inapp',
    -- 3.6 的 dedupe_key：提醒重放与总线重试不得产生第二条同一站内信。
    dedupe_key text,
    -- 未读 = 本列为 NULL；「熄灭即已读」（17.1 第 4 条）只写这一列。
    read_at    timestamptz,
    created_at timestamptz    NOT NULL DEFAULT now()
);

-- 每型未读计数（消息页三分型各显未读计数，17.1 第 3 条）与 total unread：
-- 部分索引只覆盖未读行，红点这条最热路径的 index-only scan 不被已读历史撑大。
CREATE INDEX IF NOT EXISTS homeos_notification_member_type_unread_idx
    ON homeos.homeos_notification (family_id, member_id, type)
    WHERE read_at IS NULL;

-- 列表页：某成员的站内信按时间倒序分页（GET /api/homeos/notifications?type=&cursor=）。
CREATE INDEX IF NOT EXISTS homeos_notification_member_created_idx
    ON homeos.homeos_notification (family_id, member_id, created_at DESC);

-- dedupe_key 唯一：NULL 不参与唯一性，故不写 WHERE 子句也能放过无键条目。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_notification_member_dedupe_uidx
    ON homeos.homeos_notification (member_id, dedupe_key);

COMMIT;
