# 财务 · 借贷与目标（M2）

> 范围：借入/借出、还款计划（RepaymentPlan 逐期注册到期）、结算、储蓄目标（Goal 进度/自动存入记账层生成）。
> 权威源：`docs/prd-homecube.md` 4.4（借贷/目标能力域，P1-M2）、4.5.6（借贷）、4.5.7（目标）、4.7（Loan/RepaymentPlan/Goal）、4.8（loans/goals 接口，M2）、16.4（Loan→RepaymentPlan 注册契约）。
> 本文件 case 前缀 `FIN-LOAN`。**属 M2（S15-S16），P1-M1 不计入分母。**

---

## FIN-LOAN-001 · 借入/借出创建（counterparty 允许非成员）
- 关联需求：PRD 4.5.6（借入/借出/还款计划/逾期）、4.7（Loan.counterparty 允许非成员）、4.8 `POST /api/finance/loans`
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S15-S16
- 前置条件：M2 期
- 步骤：创建借出（对手方为家庭外联系人）
- 预期结果：Loan 创建成功；counterparty 可为非成员
- 实现现状（快照）：已实现(分层)（`repo/finance_test.go:880` `CreateAndGetLoan`）
- 执行状态：未执行

## FIN-LOAN-002 · 还款计划逐期注册到期中心
- 关联需求：PRD 4.5.6（RepaymentPlan 逐期到期走 16.4 注册）、4.7（RepaymentPlan）、16.4（Loan→RepaymentPlan.due_at → Reminder 每期前 3 天）
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S15-S16
- 前置条件：存在 Loan 与还款计划
- 步骤：检查到期中心是否出现每期还款提醒
- 预期结果：每期前 3 天注册提醒；还款登记回写
- 实现现状（快照）：已实现(分层)（`repo/finance_test.go:1024`、`:1083` 还款计划）
- 执行状态：未执行

## FIN-LOAN-003 · 还款登记与逾期判定
- 关联需求：PRD 4.5.6（提醒/逾期/报表）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S15-S16
- 前置条件：存在逾期还款计划
- 步骤：登记部分/全部还款 → 查看状态
- 预期结果：状态正确流转；逾期标记准确
- 实现现状（快照）：已实现(分层)（`repo/finance_test.go:1211` 登记还款、`:1141` 逾期清单）
- 执行状态：未执行

## FIN-LOAN-004 · 储蓄目标创建与进度
- 关联需求：PRD 4.5.7（储蓄目标/进度/家庭共同目标）、4.7（Goal：target/saved/deadline/members）、4.8 `POST /api/finance/goals`
- 类型：功能 | 优先级：P0 | 建议归属 Sprint：S15-S16
- 前置条件：M2 期
- 步骤：创建目标（含 deadline 与成员）
- 预期结果：Goal 创建成功；进度 = saved/target
- 实现现状（快照）：已实现(分层)（`repo/finance_test.go:1281`、`:1355` 目标进度）
- 执行状态：未执行

## FIN-LOAN-005 · 目标"自动存入"= 记账层自动转账记录生成
- 关联需求：PRD 4.5.7（"自动存入"指记账层自动转账记录生成，不接资金通道、不做真实划转）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S15-S16
- 前置条件：设周期规则自动存入
- 步骤：触发自动存入
- 预期结果：生成转账/收款记录（记账层）；不触达任何真实资金通道（合规边界 20.4）
- 实现现状（快照）：待自动化
- 执行状态：未执行

## FIN-LOAN-006 · Goal 注册到期中心（deadline 前 30 天）
- 关联需求：PRD 16.4（finance.Goal.deadline → Reminder 前 30 天）
- 类型：功能 | 优先级：P1 | 建议归属 Sprint：S15-S16
- 前置条件：存在带 deadline 的目标
- 步骤：检查到期中心前 30 天提醒
- 预期结果：正确注册提醒；不回写（仅提醒）
- 实现现状（快照）：待自动化
- 执行状态：未执行
