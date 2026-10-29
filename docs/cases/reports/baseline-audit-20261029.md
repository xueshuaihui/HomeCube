# 证据体检报告 · 2026-10-29

> **时点说明**：本报告是定版 ㉚ 落盘前的快照（27 文件 / 291 条，仍用 `HOM-TIME-` 编号）。㉚ 后当前语料为 **30 文件 / 310 条**（HOM 180 + FIN 101 + CAL 29；309 是 ㉚ 轮数，审核轮补 `CAL-TIME-017` +1），现行口径一律以 `bash docs/cases/lint.sh` 输出为准。

> 扫描 27 个模块文件，291 条 case。**只读**，不修改任何 case 文件。

## 汇总

| 判定 | 含义 | 条数 |
|---|---|---:|
| OK | 证据指向的位置与内容都成立 | 180 |
| **STALE** | **声明已失效，必修**（文件/脚本不存在、零命中断言为假） | **111** |
| DRIFT | 需人看（行号存活但内容不相关、行数偏差 >20%） | 0 |
| WEAK | 断言方式不可靠（零命中为真，但检索词形态可疑） | 0 |

## 一、STALE · 声明已失效（必修）

> 这些条目的证据所指向的东西已不存在，或「零命中」的断言已被事实推翻。

| case | 状态 | 发现 |
|---|---|---|
| `FIN-ACC-001` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-ACC-004` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-ACC-005` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-ACC-008` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-BAL-002` | 引用脚本 | Makefile 不存在 |
| `FIN-BAL-004` | 已实现(分层) | service/statistics_test.go 不存在；service/statistics_test.go 不存在 |
| `FIN-BUD-001` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-BUD-002` | 已实现(分层) | repo/finance_tag_test.go 不存在；repo/finance_tag_test.go 不存在 |
| `FIN-BUD-005` | 已实现(分层) | service/recurring_test.go 不存在 |
| `FIN-BUD-006` | 已实现(分层) | repo/finance_tag_test.go 不存在；repo/finance_tag_test.go 不存在；repo/finance_tag_test.go 不存在 |
| `FIN-BUD-007` | 已实现(HTTP) | handler/finance_bill_test.go 不存在；service/bill_test.go 不存在 |
| `FIN-BUD-008` | 已实现(HTTP) | service/bill_test.go 不存在；repo/finance_bill_revoke_test.go 不存在 |
| `FIN-CAT-001` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-CAT-003` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-EXP-001` | 已实现(分层) | service/export_test.go 不存在；service/export_test.go 不存在；service/export_test.go 不存在 |
| `FIN-EXP-003` | 已实现(分层) | service/export_test.go 不存在；service/export_test.go 不存在 |
| `FIN-EXP-006` | 已实现(HTTP) | handler/voice_test.go 不存在；handler/voice_test.go 不存在；handler/voice_test.go 不存在 |
| `FIN-EXP-007` | 未实现 | server/packages/obs/config.go 不存在 |
| `FIN-EXP-010` | 未实现 | server/packages/adapter/ocr/stub_test.go 不存在；handler/voice_test.go 不存在 |
| `FIN-EXP-012` | 未实现 | server/packages/obs/metrics.go 不存在 |
| `FIN-LED-001` | 已实现(分层) | 标 已实现(分层)，但路由表无此接口；server/services/svc-finance/internal/repo/finance.go 不存在；server/services/svc-finance/internal/model/finance_test.go 不存在；handler/finance.go 不存在 |
| `FIN-LED-002` | 已实现(分层) | 标 已实现(分层)，但路由表无此接口；server/services/svc-finance/internal/repo/finance.go 不存在；cmd/svc-finance/main.go 不存在 |
| `FIN-LED-003` | 未实现 | cmd/svc-finance/main.go 不存在 |
| `FIN-LED-005` | 待自动化 | server/test/authz/matrix_test.go 不存在 |
| `FIN-LOAN-001` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-LOAN-002` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-LOAN-003` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-LOAN-004` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-SET-001` | 已实现(HTTP) | server/services/svc-finance/internal/handler/finance_settings_test.go 不存在；server/services/svc-finance/internal/handler/finance_settings_test.go 不存在 |
| `FIN-SET-002` | 已实现(HTTP) | server/services/svc-finance/internal/handler/finance_settings_test.go 不存在；server/services/svc-finance/internal/handler/finance_settings_test.go 不存在 |
| `FIN-SET-003` | 已实现(HTTP) | server/services/svc-finance/internal/handler/finance_settings_test.go 不存在；server/services/svc-finance/internal/handler/finance_settings_test.go 不存在；server/services/svc-finance/internal/handler/finance_settings_test.go 不存在 |
| `FIN-SPLIT-001` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-SPLIT-002` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-SPLIT-003` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-SPLIT-005` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在；repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-SPLIT-006` | 已实现(分层) | repo/finance_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-SPLIT-008` | 未实现 | handler/finance.go 不存在；cmd/svc-finance/main.go 不存在 |
| `FIN-TX-002` | 已实现(HTTP) | handler/finance_transaction_test.go 不存在；repo/finance_test.go 不存在 |
| `FIN-TX-003` | 已实现(HTTP) | handler/finance_transaction_test.go 不存在；handler/finance_transaction_test.go 不存在 |
| `FIN-TX-004` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-TX-005` | 引用脚本 | Makefile 不存在 |
| `FIN-TX-008` | 已实现(分层) | repo/finance_test.go 不存在 |
| `FIN-TX-010` | 已实现(分层) | server/test/authz/matrix_test.go 不存在 |
| `FIN-TX-011` | 契约偏差 | handler/finance.go 不存在；handler/finance.go 不存在；handler/finance.go 不存在；handler/finance.go 不存在；repo/finance_tag_test.go 不存在；repo/finance_tag_test.go 不存在 |
| `FIN-TX-012` | 契约偏差 | handler/finance.go 不存在；repo/finance_tag_test.go 不存在 |
| `HOM-ADM-001` | 未实现 | server/migrations/homeos/homeos_0009_member_governance.up.sql 不存在 |
| `HOM-APP-003` | 引用脚本 | 脚本 deploy/scripts/check-contrast.sh 不存在 |
| `HOM-APP-005` | 已实现(HTTP) | handler/bundle_manifest_test.go 不存在；handler/bundle_manifest_test.go 不存在 |
| `HOM-APP-008` | 已实现(HTTP) | handler/home_summary_test.go 不存在 |
| `HOM-APP-009` | 已实现(HTTP) | handler/home_summary_test.go 不存在；handler/home_summary_test.go 不存在 |
| `HOM-ARCH-001` | 待自动化 | server/services/svc-homeos/internal/repo/homeos_projection.go 不存在 |
| `HOM-ARCH-003` | 已实现(分层) | server/packages/auth/auth.go 不存在；server/packages/auth/auth.go 不存在；server/packages/auth/auth.go 不存在；server/packages/auth/auth.go 不存在 |
| `HOM-ARCH-007` | 已实现(分层) | server/packages/bus/interface.go 不存在；server/packages/bus/interface.go 不存在；server/packages/bus/interface.go 不存在；server/packages/bus/outbox.go 不存在；server/packages/bus/outbox.go 不存在；server/packages/bus/consumer.go 不存在；server/packages/bus/consumer.go 不存在；server/packages/bus/consumer.go 不存在；server/packages/bus/consumer.go 不存在 |
| `HOM-ARCH-009` | 已实现(分层) | server/migrations/homeos/homeos_0006_auth_family.up.sql 不存在；server/migrations/homeos/homeos_0006_auth_family.up.sql 不存在；server/migrations/homeos/homeos_0006_auth_family.up.sql 不存在 |
| `HOM-ARCH-013` | 已实现(分层) | server/packages/registry/registry.go 不存在 |
| `HOM-ARCH-016` | 已实现(分层) | server/packages/obs/serve.go 不存在 |
| `HOM-ARCH-018` | 已实现(分层) | server/packages/obs/serve.go 不存在；server/packages/obs/serve.go 不存在；server/packages/obs/serve.go 不存在；server/packages/obs/health.go 不存在；server/packages/obs/metrics.go 不存在 |
| `HOM-ARCH-025` | 待自动化 | server/packages/obs/serve.go 不存在 |
| `HOM-AUTH-001` | 已实现(分层) | server/test/authz/matrix_test.go 不存在；handler/auth_test.go 不存在 |
| `HOM-AUTH-002` | 已实现(分层) | server/test/authz/matrix_test.go 不存在；server/test/authz/matrix_test.go 不存在 |
| `HOM-AUTH-003` | 已实现(分层) | server/test/authz/matrix_test.go 不存在；server/test/authz/matrix_test.go 不存在 |
| `HOM-AUTH-004` | 已实现(分层) | server/test/authz/matrix_test.go 不存在 |
| `HOM-AUTH-006` | 已实现(分层) | server/test/authz/matrix_test.go 不存在 |
| `HOM-AUTH-009` | 未实现 | Makefile 不存在 |
| `HOM-AUTH-011` | 未实现 | 脚本 deploy/scripts/device_lock_check.sh 不存在 |
| `HOM-CMP-001` | 已实现(HTTP) | server/services/svc-homeos/cmd/svc-homeos/main.go 不存在；web/src/pages/homeos/legal/detail.vue 不存在；web/src/pages/homeos/mine/index.vue 不存在 |
| `HOM-CMP-006` | 未实现 | server/packages/obs/metrics.go 不存在；server/packages/obs/serve.go 不存在 |
| `HOM-CT-005` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary.go 不存在 |
| `HOM-SUM-001` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary_test.go 不存在 |
| `HOM-SUM-002` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary_test.go 不存在 |
| `HOM-SUM-003` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary_test.go 不存在 |
| `HOM-SUM-004` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary_test.go 不存在 |
| `HOM-SUM-005` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary_test.go 不存在；server/services/svc-homeos/internal/handler/home_summary_test.go 不存在 |
| `HOM-SUM-006` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/notifications_test.go 不存在；server/services/svc-homeos/internal/handler/notifications_test.go 不存在；home_summary_test.go 不存在 |
| `HOM-SUM-007` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/home_summary_test.go 不存在 |
| `HOM-FAM-001` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/auth_test.go 不存在；repo/identity_events_test.go 不存在 |
| `HOM-FAM-003` | 已实现(HTTP) | server/services/svc-homeos/internal/handler/auth_test.go 不存在 |
| `HOM-FAM-006` | 已实现(HTTP) | handler/faces_test.go 不存在 |
| `HOM-FAM-007` | 已实现(HTTP) | handler/faces_test.go 不存在 |
| `HOM-FAM-008` | 已实现(HTTP) | handler/faces_test.go 不存在 |
| `HOM-FAM-009` | 已实现(HTTP) | handler/faces_test.go 不存在 |
| `HOM-MEM-004` | 已实现(HTTP) | handler/auth_test.go 不存在 |
| `HOM-MEM-005` | 已实现(HTTP) | handler/auth_test.go 不存在 |
| `HOM-MEM-007` | 已实现(分层) | repo/identity_events_test.go 不存在 |
| `HOM-MEM-009` | 已实现(分层) | repo/identity_events_test.go 不存在 |
| `HOM-MEM-011` | 已实现(分层) | server/test/authz/matrix_test.go 不存在 |
| `HOM-NAV-009` | 已实现(分层) | web/src/api/request.ts 不存在；deploy/nginx.conf 不存在；deploy/nginx.conf 不存在 |
| `HOM-NOTI-001` | 已实现(HTTP) | handler/notifications_test.go 不存在；handler/notifications_test.go 不存在 |
| `HOM-NOTI-002` | 已实现(HTTP) | handler/notifications_test.go 不存在 |
| `HOM-NOTI-003` | 已实现(HTTP) | handler/notifications_test.go 不存在；home_summary_test.go 不存在 |
| `HOM-NOTI-004` | 已实现(HTTP) | handler/notifications_test.go 不存在；handler/notifications_test.go 不存在 |
| `HOM-SRCH-001` | 未实现 | server/services/svc-homeos/internal/handler/homeos.go 不存在；cmd/svc-homeos/main.go 不存在 |
| `HOM-SRCH-003` | 待自动化 | handler/homeos.go 不存在 |
| `HOM-SESS-001` | 已实现(HTTP) | 标 已实现(HTTP)，但路由表无此接口；server/services/svc-homeos/internal/handler/auth.go 不存在；cmd/svc-homeos/main.go 不存在；repo/identity.go 不存在；handler/auth_test.go 不存在 |
| `HOM-SESS-002` | 已实现(HTTP) | 标 已实现(HTTP)，但路由表无此接口；handler/auth.go 不存在；cmd/svc-homeos/main.go 不存在；handler/auth.go 不存在；handler/auth.go 不存在；handler/auth.go 不存在；auth.go 不存在；auth_test.go 不存在；auth_test.go 不存在 |
| `HOM-SESS-003` | 已实现(HTTP) | 标 已实现(HTTP)，但路由表无此接口；handler/auth.go 不存在；cmd/svc-homeos/main.go 不存在；auth.go 不存在；auth.go 不存在；auth_test.go 不存在 |
| `HOM-SESS-004` | 契约偏差 | cmd/svc-homeos/main.go 不存在；server/test/acceptance/routes.json 不存在；handler/family.go 不存在；auth_test.go 不存在 |
| `HOM-SESS-005` | 未实现 | handler/auth.go 不存在；handler/auth.go 不存在 |
| `HOM-SESS-006` | 已实现(HTTP) | handler/auth.go 不存在；main.go 不存在 |
| `HOM-SYNC-001` | 已实现(分层) | server/packages/sync/repo_test.go 不存在；server/packages/sync/repo_test.go 不存在 |
| `HOM-SYNC-002` | 引用脚本 | Makefile 不存在；脚本 deploy/scripts/soak_test.sh 不存在 |
| `HOM-SYNC-006` | 已实现(分层) | server/packages/bus/bus_test.go 不存在；server/packages/bus/bus_test.go 不存在；server/packages/bus/bus_test.go 不存在 |
| `HOM-SYNC-007` | 已实现(分层) | repo/finance_test.go 不存在 |
| `HOM-SYNC-008` | 已实现(分层) | server/packages/bus/bus_test.go 不存在；server/packages/bus/bus_test.go 不存在；server/packages/bus/bus_test.go 不存在 |
| `HOM-SYNC-011` | 引用脚本 | Makefile 不存在；Makefile 不存在 |
| `HOM-SYNC-012` | 引用脚本 | Makefile 不存在 |
| `HOM-SYNC-014` | 已实现(HTTP) | handler/bundle_manifest_test.go 不存在；handler/bundle_manifest_test.go 不存在 |
| `HOM-SYNC-024` | 已实现(分层) | server/packages/bus/consumer.go 不存在；server/packages/bus/consumer.go 不存在；server/packages/bus/outbox.go 不存在 |
| `HOM-TIME-001` | 已实现(分层) | consumer/due_trigger_scanner_test.go 不存在；repo/homeos_due_test.go 不存在 |
| `HOM-TIME-003` | 已实现(分层) | server/test/authz/matrix_test.go 不存在 |
| `HOM-TIME-009` | 已实现(HTTP) | handler/dynamics_test.go 不存在；handler/dynamics_test.go 不存在 |

