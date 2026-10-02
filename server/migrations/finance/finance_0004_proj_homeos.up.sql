-- finance 序列 0004：投影表 finance_proj_homeos（§2.3 第 6 行、§六）
-- 表名由 registry 的 ProjectionTable("homeos") 派生 = finance_proj_homeos，
-- 命名口径：PRD 16.3「投影表命名 {code}_proj_{来源域}」；后缀 _proj_* 命中定版 ㉔ 白名单。
-- 落在 **本域 schema finance**：投影是他域数据的本地副本，写方只有本服务订阅器（§六）。
-- §六「{code}_proj_{src} 只允许本服务订阅器写入，可随时全量重建（重建脚本随 M1 交付，
--       否则投影表会变成第二数据源）」
-- 本表因此只是 homeos 数据的本地副本：存本服务渲染要的算料、不反写 homeos；
-- 引用他域对象一律「只存 id，无物理约束」（§2.2「不存在跨服务外键」、16.3「逻辑 id 引用」），
-- 所以本支一个 REFERENCES 都不出现，完整性由每日对账兜（§六、PRD 18.6）。
CREATE TABLE IF NOT EXISTS finance.finance_proj_homeos (
    -- §2.2「索引第一列固定 family_id」；§15.2「判定始终以 token 内的 family_id（当前会话家庭）为界」。
    family_id  uuid        NOT NULL,
    -- §2.3/§六 本表用途 = 「成员/权限快照」；§8.3 第 3 步「demo 项的『参与成员』字段渲染与该快照同源」，
    -- 同源对象就是 §3.7 的 GET /api/homeos/members/snapshot 里的成员行。
    member_id  uuid        NOT NULL,
    -- §4.1「claims: … role: owner|member|ward|guest」；PRD 15.1 的五角色里「已退出成员」是状态不是角色，
    -- 因此本卡不加 CHECK（15.5 的历史作者标记由 homeos 侧决定），取值口径待 15.6 快照契约定版 -> 见报告。
    role       text        NOT NULL,
    -- §4.1「pver 是家庭级权限版本，任何权限、成员或面配置变更即 +1」；
    -- PRD 3.7 快照口「支持 If-None-Match: pver 命中 304」-> 本地副本要能判断自己是不是旧于源。
    pver       bigint      NOT NULL,
    -- §六 投影可随时全量重建 + PRD 18.6 每日对账「投影 vs 来源逐表比行数与抽样字段」-> 需要本地写入时刻。
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 订阅器的 upsert 锚点（§六「只允许本服务订阅器写入」= 一个成员一行）。
-- 文档未给投影表的唯一键列集合 -> 见报告『待回写文档』。
CREATE UNIQUE INDEX IF NOT EXISTS finance_proj_homeos_family_member_uidx ON finance.finance_proj_homeos (family_id, member_id);
