// Package obs is the operability plumbing HomeCube shares across its services:
// structured logging and metrics with the constant label code
// (docs/p1-tech-plan.md §1.2 仓库布局「packages/obs/  结构化日志、指标（常量标签 code）、审计上报」).
//
// # What this package delivers today, and what it deliberately does not
//
//   - Structured logging (§1.2, PRD 22.2 第 10 条「日志、埋点与指标必须带 family_id 与 code
//     两个维度」): the code dimension comes from the registry row and is attached once at
//     construction; see log.go.
//   - Metrics with the constant label code (§1.2, §10.1 末条, PRD 21.5): only the items of
//     §10.3's 最小集 that a running skeleton can actually produce -- 接口延迟 (histogram, so
//     P95/P99 are derivable) and 错误率 (a counter per status code). No gauge is registered for
//     outbox 积压 / 死信数 / 收敛时间 / 跨服务调用 / 同步冲突 / 投递回执 / 依赖调用量, because those
//     numbers come from S2/S5/S6/S13 and an empty gauge would be a fake data source.
//   - The /healthz probe aggregation (§10.1 末条「每服务 /healthz 报告『本 schema 可连 + JetStream
//     已连』」) and the Gin engine assembly PRD 22.4/22.2 第 1 条 describes per service, because
//     services may not import each other (§1.2「services/A → services/B 的任何 import 禁止」) and
//     §1.2's package list is closed -- a new shared package is not available to this card. That
//     placement is reported as a document question, not assumed.
//   - NOT audit reporting: §1.2 lists it in this package's remit, but the audit envelope, the nine
//     categories (PRD 21.5) and the {code}.audit.recorded publish path are S4/S5 work (§10.3 第 2 条
//     「审计事件按 21.5 的九类落 homeos_audit_log ... 与业务写同一 outbox 事务落盘」 -- the outbox
//     itself is S2). No placeholder function lives here.
//
// # The metrics dimensions this package does NOT emit yet
//
// §10.3 and PRD 21.5 require 全部指标带 family_id 与 code. code is emitted as a constant label now
// (that is what §10.1 末条 names as this card's 判据). family_id has no source in this checkout:
// the only documented source of a request's family is the JWT claim carried by the authz SDK
// middleware (§4.1, PRD 14.5 第 3 项「token 携带 family_id、角色快照与 pver」), and that middleware
// is S4. Labeling it with an empty value would register a dimension that no code path can ever
// fill, so it is reported as an open item instead.
//
// # Dependency direction
//
// This package imports packages/registry only (§1.2:「packages/* 不得反向依赖任何服务」). It holds
// no business semantics: no table, no route beyond the two documented operational endpoints, no
// event.
package obs
