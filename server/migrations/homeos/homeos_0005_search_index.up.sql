-- homeos 序列 0005：搜索索引表 homeos_search_index（PRD 14.5 #7）
-- 表名由 PRD 权威表确认 = homeos_search_index，后缀 _search_index 命中定版 ㉔ 白名单。
-- 落在 **本域 schema homeos**：搜索索引由 svc-homeos 单独持有，各面数据经事件投影喂入。
-- §六「{code}_proj_{src} 只允许本服务订阅器写入」的延伸：搜索索引同样只由本服务维护。

CREATE TABLE IF NOT EXISTS homeos.homeos_search_index (
    id UUID PRIMARY KEY,
    family_id UUID NOT NULL,
    domain VARCHAR(20) NOT NULL,        -- finance/purchase/diet/etc
    entity VARCHAR(50) NOT NULL,         -- transaction/account/category/etc
    entity_id UUID NOT NULL,
    content TEXT NOT NULL,               -- 可搜索的文本内容（拼接关键字段）
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP
);

-- 索引：按家庭过滤（首页格子的统计范围恒为会话家庭，15.2）
CREATE INDEX idx_homeos_search_family ON homeos.homeos_search_index(family_id);

-- 索引：按领域过滤（支持按面分组展示结果）
CREATE INDEX idx_homeos_search_domain ON homeos.homeos_search_index(domain);

-- 唯一约束：同一领域内同一实体 ID 只能有一条索引记录（支持 upsert）
CREATE UNIQUE INDEX idx_homeos_search_entity_unique ON homeos.homeos_search_index(domain, entity_id);

-- 使用 pg_trgm 扩展支持模糊搜索（如果可用）
-- PRD 14.5 #7 要求"关键词全局搜索"，pg_trgm 提供高效的模糊匹配能力
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- GIN 索引：支持高性能的全文模糊搜索
CREATE INDEX idx_homeos_search_content ON homeos.homeos_search_index USING gin(content gin_trgm_ops);
