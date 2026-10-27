# 归档 · 2026-10 P1 阶段产物

> 归档日期：2026-10-27
> 归档原因：`docs/` 顶层堆积了 10 份 2026-10-03 ~ 10-09 的过程报告与原型产物，其结论均已被 `docs/cases/`（291 条 case + 实现现状矩阵）取代。为避免主目录混入失效信息，非文档类的历史产物移入本目录。

## 内容

| 项 | 说明 | 为何不删 |
|---|---|---|
| `mockup-home.html` / `mockup-home.png` | 2026-09-29 的首页高保真原型 | 无任何代码或文档引用，但体积较大且不可由代码重建；保留以备设计溯源 |
| `screenshots/`（13 张） | 2026-10-04 真实浏览器 E2E 的页面截图 | 原为 `E2E-BROWSER-TEST-REPORT*.md` 的佐证材料。报告已删，截图的对应关系见下方「报告去向」 |
| `P1-PROGRESS-REVIEW-2026-10-09.md` | 10-09 的项目进度审核报告 | **该文件从未提交 git**，删除即不可恢复，故移入归档而非删除 |

## 报告去向

以下 9 份报告已于 2026-10-27 从 `docs/` 顶层删除。它们都已提交 git，可用
`git log --diff-filter=D -- docs/<文件名>` 找到删除提交，再 `git checkout <commit>^ -- docs/<文件名>` 恢复。

| 报告 | 结论 | 处置依据 |
|---|---|---|
| `CR_FIX_REPORT.md` | 2026-10-03 CR 阻塞级缺陷修复 | 结论已被后续 E2E 两轮与 `cases/` 覆盖 |
| `E2E-BROWSER-TEST-REPORT.md` | 11 个缺陷，10 修 + 1 定界 | 2026-10-27 逐条复核全部闭环 |
| `E2E-BROWSER-TEST-REPORT-ROUND2.md` | 7 个新缺陷 | 4 修 + 1 判为非缺陷 + **2 项未闭环，已转 `cases/BACKLOG.md` IMPL-42/43** |
| `P1-ACCEPTANCE-REPORT.md` | 2026-10-03 验收快照 | 验收口径已由 `cases/` 的 291 条 case 接管 |
| `P1-REGRESSION-TEST-REPORT.md` | 2026-10-04 浏览器验收 | 同上 |
| `P1-TEST-PROGRESS-81.md` | 过程快照（81% 通过率） | 过程数字，无长期价值 |
| `P1-FINAL-PROGRESS-REPORT.md` | 过程快照（70% 完成） | 同上 |
| `P1-CODE-REVIEW-REPORT.md` | 2026-10-09 代码审核，发现 2 项 | 2 项已由 `P1-FIX-SUMMARY.md` 记录修复 |
| `P1-FIX-SUMMARY.md` | 上述 2 项的修复记录 | 修复内容已在代码与 case 中体现 |

## 关键提醒

`E2E-BROWSER-TEST-REPORT-ROUND2.md` 里未闭环的 2 项**没有随报告消失**，已登记为
`docs/cases/BACKLOG.md` 的 **IMPL-42**（六个写入端点字段三方不一致）与 **IMPL-43**（八页弹层按钮小屏不可见）。
排查这两类问题时请从 BACKLOG 入手，不要只看本归档目录。

## 本目录的维护

- 只增不改：已归档文件不再修订，若需更新结论请在新位置（`docs/cases/`）记录。
- 不要再往 `docs/` 顶层放过程报告：阶段性结论写进 `docs/cases/AUDIT-REPORT.md`，缺陷写进 `BACKLOG.md`，契约偏差写进 `DEVIATIONS.md`。
