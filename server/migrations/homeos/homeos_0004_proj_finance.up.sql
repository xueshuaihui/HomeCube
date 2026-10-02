-- homeos 序列 0004：投影表 homeos_proj_finance（§2.3 第 6 行、§六）
-- 表名由 registry 的 ProjectionTable("finance") 派生 = homeos_proj_finance，
-- 命名口径：PRD 16.3「投影表命名 {code}_proj_{来源域}」；后缀 _proj_* 命中定版 ㉔ 白名单。
-- 落在 **本域 schema homeos**：投影是他域数据的本地副本，写方只有本服务订阅器（§六）。
-- §六「{code}_proj_{src} 只允许本服务订阅器写入，可随时全量重建（重建脚本随 M1 交付，
--       否则投影表会变成第二数据源）」
-- 本表因此只是 finance 数据的本地副本：存本服务渲染要的算料、不反写 finance；
-- 引用他域对象一律「只存 id，无物理约束」（§2.2「不存在跨服务外键」、16.3「逻辑 id 引用」），
-- 所以本支一个 REFERENCES 都不出现，完整性由每日对账兜（§六、PRD 18.6）。
CREATE TABLE IF NOT EXISTS homeos.homeos_proj_finance (
    -- §2.2「索引第一列固定 family_id」；首页格子的统计范围恒为会话家庭（15.2）。
    family_id              uuid    NOT NULL,
    -- §六「按『自然月桶』聚合的收支与预算剩余快照」+「投影的桶粒度固定为月，
    --   季/年两档由服务端在月桶上求和后下发（⑬ 三档不能各存一套投影）」
    -- -> 只存月桶一套；bucket_month 取该自然月的首日（家庭时区口径，定版⑬ 的 YYYY-MM 由服务端格式化）。
    --   列名与类型文档未给（§六 只写「自然月桶」这个说法）-> 见报告『待回写文档』。
    bucket_month           date    NOT NULL,
    -- §六 「收支」的两翼。§2.2「金额一律 amount_cents bigint；禁止浮点与 numeric 混用」+ PRD 14.7
    -- 「金额一律以『分』为最小单位的整数存储（amount_cents），不使用浮点」-> bigint，列名沿用 *_cents 口径。
    income_cents           bigint  NOT NULL DEFAULT 0,
    expense_cents          bigint  NOT NULL DEFAULT 0,
    -- §六 「预算剩余快照」；定版⑬「只有月度预算时，季/年档返回『未设该周期预算』的显式标识，
    --   乘 3 或乘 12 造出的假剩余在 18.3#9 里判失败」-> 未设周期预算必须是 NULL，不能是 0。
    budget_remaining_cents bigint,
    -- §九 faces[]「每项带 availability、headline、badge、as_of」中的 as_of：
    -- 「该投影同时是 home/summary 中财务那一格 headline 与角标的唯一数据源」-> 下发算料的产出时刻。
    -- 角标（badge）本身的计数列形态文档未给 -> 本卡不落，见报告『文档缺陷或冲突』。
    updated_at             timestamptz NOT NULL DEFAULT now()
);

-- 月桶的唯一性 = 订阅器 upsert 的锚点，也是服务端在月桶上求和（定版⑬）的分组键。
-- 文档未给投影表的唯一键列集合 -> 见报告『待回写文档』。
CREATE UNIQUE INDEX IF NOT EXISTS homeos_proj_finance_family_month_uidx ON homeos.homeos_proj_finance (family_id, bucket_month);
