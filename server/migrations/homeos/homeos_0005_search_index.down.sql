-- homeos 序列 0005：回滚搜索索引表

DROP INDEX IF EXISTS homeos.idx_homeos_search_content;
DROP EXTENSION IF EXISTS pg_trgm;
DROP INDEX IF EXISTS homeos.idx_homeos_search_entity_unique;
DROP INDEX IF EXISTS homeos.idx_homeos_search_domain;
DROP INDEX IF EXISTS homeos.idx_homeos_search_family;
DROP TABLE IF EXISTS homeos.homeos_search_index;
