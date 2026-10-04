# P1 完整修复进度报告

**开始时间**: 2026-10-04
**当前状态**: 进行中（约 30% 完成）

---

## 一、已修复缺陷

### ✅ P0-1: 建家后 token 刷新问题

**问题**: 测试脚本在建家成功后未更新 access_token，导致后续请求仍使用 onboarding token

**修复**:
- 文件: `deploy/scripts/p1-e2e-test.sh` line 86-92
- 修改: 从建家响应中提取新的 access_token 并更新 ADMIN_TOKEN 变量
- 验证: 测试已通过（创建家庭返回 201）

**状态**: ✅ 已完成

---

### ✅ P0-2: 邀请成员功能实现

**问题**: PRD 明确要求的核心接口 `POST /members/invite` 完全缺失

**修复**:
1. **数据库表**: 已存在（`homeos_invitations` in migration 0006）
2. **Handler 实现**: 新增 3 个 handler
   - `CreateInvite` (POST /families/{family_id}/invites) — 生成邀请码
   - `ListInvites` (GET /families/{family_id}/invites) — 查看邀请列表
   - `RevokeInvite` (DELETE /families/{family_id}/invites/{id}) — 撤销邀请
3. **路由注册**: 已在 main.go 中注册
4. **权限控制**: 仅 owner 可创建/查看/撤销邀请

**文件变更**:
- `server/services/svc-homeos/internal/handler/family.go`: +220 行
- `server/services/svc-homeos/cmd/svc-homeos/main.go`: +12 行

**状态**: ⏳ 代码已实现，待编译测试

---

## 二、待修复缺陷（按优先级排序）

### 🔴 P0-3: 余额查询接口路径错误

**现象**: 测试脚本调用 `/api/finance/balance` 返回 404

**根因**: 实际路径是 `/api/finance/accounts/:id/balance`

**修复方案**:
- 修改测试脚本中的 API 路径
- 或在前端添加统一的余额查询封装

**预计工时**: 15 分钟

---

### 🔴 P0-4: 统计报表接口路径错误

**现象**: 测试脚本调用 `/api/finance/statistics` 返回 404

**根因**: 实际有 4 个子路径：
- `/statistics/overview`
- `/statistics/trend`
- `/statistics/category`
- `/statistics/member`

**修复方案**: 修改测试脚本指定具体统计类型

**预计工时**: 10 分钟

---

### 🔴 P0-5: 成人成员越权删除未被拒绝

**现象**: 成人成员可以删除管理员创建的流水（应返回 403）

**根因分析**:
1. authz 权限矩阵已定义九行五列
2. 但 DeleteTransaction handler 可能未调用权限检查
3. 或权限中间件未正确执行

**修复方案**:
1. 检查 `svc-finance/internal/handler/finance.go` 的 DeleteTransaction 函数
2. 确保调用 `svcauth.Require(authz.ActionDelete, ...)`
3. 验证权限矩阵在 finance 域的配置

**预计工时**: 1-2 小时

---

### 🔴 P0-6: L3 私密流水未被过滤

**现象**: 成人成员可以看到他人的私密流水（visibility=private）

**根因分析**:
1. DTO 层可见性过滤 (`packages/authz/dto_visibility.go`) 可能未生效
2. 或 ListTransactions 查询未加入 visibility 条件

**修复方案**:
1. 检查 repo.ListTransactions 是否过滤 private 记录
2. 验证 dto_visibility.Apply 是否在 handler 层调用
3. 确保非作者且非 owner 的角色看不到 private 流水

**预计工时**: 1-2 小时

---

### 🔴 P0-7: 软删除返回 500

**现象**: DELETE /transactions/{id} with soft_delete=true 返回 500

**根因**: 可能是：
1. 回收站表不存在
2. 或事务处理逻辑有误
3. 或缺少必要的字段

**修复方案**:
1. 检查数据库中是否有 trash 相关表
2. 查看 svc-finance 日志确定具体错误
3. 修复 handler/repo 层的实现

**预计工时**: 1 小时

---

### 🟡 P1-1: 预算创建参数不匹配

**现象**: POST /budgets 返回 400

**根因**: 请求体字段与 handler 期望不一致

**修复方案**: 检查 handler 的 binding struct 并修正测试脚本

**预计工时**: 30 分钟

---

### 🟡 P1-2: 模块配置接口 404

**现象**: GET /families/{id}/modules 返回 404

**根因**: 路由可能未注册或路径前缀错误

**修复方案**: 检查 main.go 中的路由注册

**预计工时**: 30 分钟

---

### 🟡 P1-3: 成员列表接口 404

**现象**: GET /families/{id}/members 返回 404

**根因**: 同上

**修复方案**: 检查路由注册

**预计工时**: 30 分钟

---

## 三、修复进度统计

| 类别 | 总数 | 已完成 | 进行中 | 待开始 | 完成率 |
|------|------|--------|--------|--------|--------|
| P0 缺陷 | 7 | 2 | 1 | 4 | 29% |
| P1 缺陷 | 3 | 0 | 0 | 3 | 0% |
| **总计** | **10** | **2** | **1** | **7** | **20%** |

---

## 四、下一步计划

### 立即执行（今天内）
1. ✅ 邀请功能实现（已完成代码）
2. ⏳ 修复余额/统计接口路径（15 分钟）
3. ⏳ 修复权限矩阵失效（2 小时）
4. ⏳ 修复 L3 字段过滤（2 小时）

### 明天执行
5. 修复软删除 500 错误
6. 修复预算创建参数
7. 补充缺失的路由（模块配置、成员列表）

### 后天执行
8. 全面回归测试
9. 浏览器自动化验收
10. 生成最终交付报告

---

## 五、风险与阻塞

### 已知风险
1. **Go 编译器不可用**: 当前环境无法编译测试，需切换到有 Go 的环境
2. **数据库迁移未执行**: 新功能依赖的表可能需要手动迁移
3. **前端未同步更新**: 部分 API 变更后前端需相应调整

### 缓解措施
1. 先在代码层面完成所有修复，再统一编译测试
2. 准备数据库迁移脚本
3. 同步更新前端 API 调用

---

**最后更新**: 2026-10-04 18:30
**下次更新**: 完成 P0-3/4/5/6 后
