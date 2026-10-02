-- homeos 序列 0002：底座运行表族之「发布 / 消费」三支（§2.3 的第 1、2、3 行）
-- 三支同族：后缀 _outbox / _event_dedupe / _dead_letter 命中定版 ㉔ 的后缀白名单
-- （§10.2 第 2 道、PRD 22.5 第 2 道「底座运行表族按命名后缀白名单放行、不进 16.2」），
-- 命中后缀只校验「前缀 = 所在服务」：本文件全部对象的前缀 homeos_ = schema homeos 的 code。
-- 全部语句 IF NOT EXISTS，可整支重放。

-- ---------------------------------------------------------------------------
-- homeos.homeos_outbox
-- §2.3「homeos_outbox | 发布前落盘，投递器异步搬入 JetStream | 事务边界只在单库单 schema 内成立」
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_outbox (
    -- §3.3 原文只给 (subject, envelope, status)。投递器「每 500ms 批量 100 行 → 成功置 sent」
    -- 要按行寻址（PRD 3.7 亦按 {id} 寻址死信重放），文档未给主键列 -> 见报告『待回写文档』。
    id         bigserial                   PRIMARY KEY,
    -- §10.3「指标最小集（全部带 family_id 与 code）……outbox 积压」+ PRD 22.2 第 10 条；
    -- 可空按 §2.2「系统预置数据用可空 family_id 表达」（预置对象的变更事件不属于任何家庭）。
    family_id  uuid,
    -- §3.3「INSERT homeos_outbox(subject, envelope, status=pending)」；
    -- §3.1「subject 命名即事件名：{code}.{object}.{action}，小写点分」。
    subject    text             NOT NULL,
    -- §3.3 同上；§3.1「版本不进 subject 而进信封 version 字段」-> 版本落在信封里，不另设列。
    -- jsonb 是本卡选的存储类型，文档未指定 -> 见报告。
    envelope   jsonb            NOT NULL,
    -- §3.3「status=pending」+「成功置 sent」：文档只定义这两态，CHECK 即这条状态机的直译。
    status     text             NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent')),
    -- §3.3「失败 attempts+1」「attempts>10 告警（不丢，只是没送）」-> 超限仍留在 pending，不另设失败态。
    attempts   integer          NOT NULL DEFAULT 0,
    -- §10.3 outbox 积压、§3.3「事件传播多 ≤1 秒的固有延迟」与 18.1 收敛口径都要行龄：now()-created_at。
    -- 投递成功时刻（sent_at）文档未给列 -> 未落，见报告『文档缺陷或冲突』。
    created_at timestamptz      NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- homeos.homeos_event_dedupe
-- §2.3「homeos_event_dedupe | 消费幂等键 (event_type, business_id)（business_id 形态见 §3.3 与 PRD 10.4 取值约定）
--        | 去重是消费方自己的状态，不能共享别人的表」
-- §3.4「进 handler 第一件事：INSERT ON CONFLICT DO NOTHING INTO homeos_event_dedupe；已存在即 ack 返回」
-- §3.4「重复投递不产生重复业务对象是表约束保证，不是代码纪律」
-- §3.4「留存期取 365 天 + 30 天余量，每日清理更早的行」-> created_at 既是留存判定列
-- P1 去分区：移除 RANGE 按月分区，改为普通表，(event_type, business_id) 做全局唯一索引
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_event_dedupe (
    -- §3.4 去重键第一段；PRD 10.4「幂等键：event_type + business_id」。
    event_type  text        NOT NULL,
    -- §3.4 去重键第二段。形态由发布方按 PRD 10.4 的取值约定填写
    -- （可重复发生的事件必须带周期或版本后缀：{budget_id}:{period} / {source_id}:{due_at} / …），
    -- 因此本列是自由文本，不加 CHECK、不拆列。
    business_id text        NOT NULL,
    -- §3.4 留存判定列。
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- §3.4 的全局唯一键 (event_type, business_id)，跨月插入不重复
CREATE UNIQUE INDEX IF NOT EXISTS homeos_event_dedupe_key ON homeos.homeos_event_dedupe (event_type, business_id);

-- ---------------------------------------------------------------------------
-- homeos.homeos_dead_letter
-- §2.3「homeos_dead_letter | 死信落库与人工重放 | 死信归属消费方（22.2 第 10 条）」
-- §3.4「超限失败 -> publish 到 dl.{consumerCode}.{event_type} + 写 homeos_dead_letter」
-- §3.4「管理台按服务查看并人工重放（重放即原样重新入队，走同一幂等键）」
-- PRD 3.7「GET /api/{code}/dead-letters」「POST /api/homeos/dead-letters/{id}/replay」
-- PRD 21.4「死信：解决后保留 90 天」
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS homeos.homeos_dead_letter (
    -- PRD 3.7 的重放口按 {id} 寻址。
    id            bigserial   PRIMARY KEY,
    -- PRD 22.2 第 10 条「日志、埋点与指标必须带 family_id 与 code」；可空理由同 outbox（§2.2 预置数据）。
    family_id     uuid,
    -- §3.4 死信 subject 的两段之一：dl.{consumerCode}.{event_type}，也是「管理台按服务查看」的过滤位。
    consumer_code text        NOT NULL,
    -- §3.4 死信 subject 的另一段；§3.1「subject 命名即事件名」-> 与 subject 同义，不重复存 subject。
    event_type    text        NOT NULL,
    -- §3.4「重放即原样重新入队」-> 必须存原文。
    envelope      jsonb       NOT NULL,
    -- §3.4「管理台按服务查看」：查看的最小内容即失败原因；文档未给列名 -> 见报告。
    last_error    text,
    -- PRD 21.4「解决后保留 90 天」的两个端点：落库时刻与解决时刻。
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- PRD 21.4 的保留期从「解决」起算 -> 需要解决时刻。文档只写了「解决」这个词，
    -- 没有给状态机（重放成功 / 人工忽略 / 已过期），因此本卡不发明 status 枚举 -> 见报告。
    resolved_at   timestamptz
);

-- §2.2「索引第一列固定 family_id」：管理台按家庭列出死信（PRD 3.7）与 21.4 的保留期扫描共用这一条。
CREATE INDEX IF NOT EXISTS homeos_dead_letter_family_created_idx ON homeos.homeos_dead_letter (family_id, created_at);
