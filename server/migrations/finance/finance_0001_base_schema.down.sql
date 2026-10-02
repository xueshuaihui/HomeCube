-- 反向于 0001：只回收空 schema。
-- 刻意不写 CASCADE：schema 内若还有任何对象（S3+ 的业务表、后续分支的对象），本句必须失败而不是把它们带走
-- （§10.1「回滚刻意保持薄……不执行 down 迁移」，down 只在开发库上按序倒退）。
-- golang-migrate 的倒退顺序是 0004→0003→0002→0001，走到这里时运行表族与投影表已被各自 down 删除。
DROP SCHEMA IF EXISTS finance;
