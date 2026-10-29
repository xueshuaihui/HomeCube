# PRD 接口 → Case 追溯矩阵（自动生成）

> 本文件由 `docs/cases/gen_traceability.py` 从 `docs/prd-homecube.md` 生成，**请勿手改**——改 MAP 后重跑。
> 用途：证明「一期 PRD 的每个接口都有 case 承接」。这是**需求覆盖率**的唯一判据；
> `自动化` / `实现现状` 是另一条线，不影响本表——**case 不因实现缺席而消失**。
> 非一期接口（P2-P6 面、P6 能力）单列在末节，**不进入分母**，避免被误判为缺口。

**统计**：一期接口行 75 条（合计 111 个接口，全部有 case 承接）· 非一期 51 行（52 个接口）· 覆盖 case 310 条

## 一、一期接口追溯表

| PRD 行 | 接口 | 承接 case |
|---|---|---|
| :418 | `GET /api/homeos/family` | `HOM-SESS-002`<br>`HOM-FAM-004` |
| :419 | `GET /api/homeos/families`<br>`POST /api/homeos/families/switch` | `HOM-FAM-004` |
| :420 | `GET /api/homeos/members` | `HOM-MEM-011` |
| :421 | `POST /api/homeos/members/invite` | `HOM-MEM-004` |
| :422 | `GET /api/homeos/permissions` | `HOM-AUTH-014`<br>`HOM-AUTH-005` |
| :423 | `GET /api/homeos/notifications`<br>`POST /api/homeos/notifications/read` | `HOM-NOTI-003`<br>`HOM-NOTI-007` |
| :424 | `POST /api/homeos/devices/push-token` | `HOM-NOTI-005` |
| :425 | `POST /api/homeos/events/publish`<br>`POST /api/homeos/events/subscribe` | `HOM-ADM-005` |
| :427 | `GET /api/homeos/family/modules` | `HOM-FAM-009`<br>`HOM-FAM-007` |
| :428 | `PUT /api/homeos/family/modules` | `HOM-FAM-006`<br>`HOM-FAM-008` |
| :429 | `GET /api/homeos/app/bundles` | `HOM-SYNC-014`<br>`CAL-FACE-004` |
| :430 | `GET /api/homeos/home/summary` | `HOM-SUM-001`<br>`HOM-SUM-003`<br>`HOM-SUM-004` |
| :431 | `GET /api/homeos/permissions/snapshot`<br>`GET /api/homeos/members/snapshot` | `HOM-AUTH-014` |
| :433 | `GET /api/homeos/search` | `HOM-SRCH-001`<br>`HOM-SRCH-002` |
| :435 | `GET /api/homeos/settings` | `HOM-APP-001`<br>`HOM-APP-002` |
| :436 | `PUT /api/homeos/settings` | `HOM-APP-001`<br>`HOM-APP-002` |
| :437 | `GET /api/homeos/app/version` | `HOM-APP-005`<br>`HOM-APP-006` |
| :438 | `POST /api/homeos/votes` | `HOM-TIME-007` |
| :439 | `POST /api/homeos/auth/sms-code`<br>`POST /api/homeos/auth/login`<br>`POST /api/homeos/auth/refresh` | `HOM-SESS-001` |
| :440 | `POST /api/homeos/families` | `HOM-FAM-001` |
| :441 | `POST /api/homeos/family/invite/accept` | `HOM-FAM-003` |
| :442 | `POST /api/homeos/members`<br>`PUT /api/homeos/members/{id}` | `HOM-MEM-001`<br>`HOM-MEM-002` |
| :443 | `GET /api/homeos/dynamics` | `HOM-TIME-009` |
| :444 | `GET /api/homeos/board/messages`<br>`POST /api/homeos/board/messages` | `HOM-TIME-008` |
| :445 | `GET /api/homeos/votes/{id}`<br>`POST /api/homeos/votes/{id}/ballot` | `HOM-TIME-007` |
| :446 | `POST /api/homeos/data/export`<br>`GET /api/finance/reports/export` | `HOM-SYNC-013` |
| :447 | `DELETE /api/homeos/family/invites/{id}` | `HOM-MEM-005` |
| :448 | `DELETE /api/homeos/members/{id}` | `HOM-MEM-009` |
| :449 | `PUT /api/homeos/members/{id}/role` | `HOM-MEM-007`<br>`HOM-MEM-008` |
| :450 | `POST /api/homeos/family/transfer-owner` | `HOM-MEM-010` |
| :451 | `POST /api/homeos/family/leave`<br>`POST /api/homeos/family/disband` | `HOM-FAM-011`<br>`HOM-FAM-012` |
| :452 | `GET /api/homeos/data/backup-status`<br>`POST /api/homeos/data/restore` | `HOM-ADM-003`<br>`HOM-SYNC-011` |
| :453 | `GET /api/homeos/audit-events` | `HOM-ADM-001`<br>`HOM-SESS-005` |
| :454 | `GET /api/homeos/reconciliation-report` | `HOM-ADM-002` |
| :456 | `GET /api/homeos/dead-letters`<br>`POST /api/homeos/dead-letters/{id}/replay` | `HOM-SYNC-010` |
| :657 | `POST /api/finance/transactions` | `FIN-TX-002`<br>`FIN-TX-003` |
| :658 | `GET /api/finance/transactions` | `FIN-TX-004`<br>`FIN-TX-005` |
| :659 | `GET /api/finance/transactions/{id}` | `FIN-TX-006` |
| :660 | `PUT /api/finance/transactions/{id}` | `FIN-TX-007` |
| :661 | `DELETE /api/finance/transactions/{id}` | `FIN-TX-008` |
| :662 | `GET /api/finance/accounts` | `FIN-ACC-003`<br>`FIN-ACC-004` |
| :663 | `POST /api/finance/budgets` | `FIN-BUD-001` |
| :664 | `GET /api/finance/reports/summary` | `FIN-BAL-002`<br>`FIN-BAL-009` |
| :665 | `GET /api/finance/reports/export` | `FIN-EXP-001`<br>`FIN-EXP-002` |
| :666 | `POST /api/finance/ledgers` | `FIN-LED-001` |
| :667 | `POST /api/finance/settlements` | `FIN-SPLIT-002`<br>`FIN-SPLIT-003` |
| :668 | `POST /api/finance/loans` | `FIN-LOAN-001` |
| :669 | `POST /api/finance/goals` | `FIN-LOAN-004` |
| :670 | `GET /api/finance/categories` | `FIN-CAT-001`<br>`FIN-CAT-003` |
| :671 | `POST /api/finance/categories`<br>`PUT /api/finance/categories/{id}` | `FIN-CAT-001`<br>`FIN-CAT-007` |
| :672 | `PUT /api/finance/categories/{id}/archive`<br>`POST /api/finance/categories/{id}/restore`<br>`PUT /api/finance/categories/sort` | `FIN-CAT-003`<br>`FIN-CAT-005` |
| :673 | `POST /api/finance/accounts`<br>`PUT /api/finance/accounts/{id}` | `FIN-ACC-001`<br>`FIN-ACC-002` |
| :674 | `POST /api/finance/accounts/{id}/archive`<br>`POST /api/finance/accounts/{id}/restore` | `FIN-ACC-004`<br>`FIN-ACC-005` |
| :675 | `GET /api/finance/budgets`<br>`PUT /api/finance/budgets/{id}` | `FIN-BUD-002`<br>`FIN-BUD-004` |
| :676 | `PUT /api/finance/ledgers/{id}` | `FIN-LED-003` |
| :677 | `GET /api/finance/bills`<br>`POST /api/finance/bills`<br>`PUT /api/finance/bills/{id}`<br>`POST /api/finance/bills/{id}/paid` | `FIN-BUD-007`<br>`FIN-BUD-009` |
| :678 | `GET /api/finance/recurring`<br>`POST /api/finance/recurring`<br>`PUT /api/finance/recurring/{id}` | `FIN-BUD-005`<br>`FIN-BUD-006` |
| :679 | `POST /api/finance/transactions/{id}/restore` | `FIN-TX-008`<br>`FIN-TX-009` |
| :680 | `GET /api/finance/transactions?deleted=true` | `FIN-TX-009` |
| :681 | `GET /api/finance/ledgers` | `FIN-LED-002`<br>`FIN-LED-004` |
| :682 | `GET /api/finance/bills/{id}` | `FIN-BUD-009` |
| :683 | `GET /api/finance/settings`<br>`PUT /api/finance/settings` | `FIN-SET-001`<br>`FIN-SET-004` |
| :684 | `POST /api/finance/invoices`<br>`POST /api/finance/expenses` | `FIN-SPLIT-005`<br>`FIN-SPLIT-007` |
| :685 | `GET /api/finance/demo-items`<br>`POST /api/finance/demo-items` | `FIN-DEMO-001`<br>`FIN-DEMO-002` |
| :686 | `GET /api/finance/transactions/{id}/attachments`<br>`POST /api/finance/transactions/{id}/attachments` | `FIN-TX-014`<br>`FIN-EXP-007` |
| :879 | `POST /api/calendar/reminders` | `CAL-TIME-005`<br>`CAL-TIME-017` |
| :880 | `POST /api/calendar/todos` | `CAL-TIME-002`<br>`CAL-TIME-011`<br>`CAL-TIME-012`<br>`CAL-TIME-017` |
| :881 | `POST /api/calendar/events`<br>`PUT /api/calendar/events/{id}`<br>`DELETE /api/calendar/events/{id}` | `CAL-TIME-010`<br>`CAL-TIME-017` |
| :882 | `GET /api/calendar/events`<br>`GET /api/calendar/events/{id}` | `CAL-TIME-001`<br>`CAL-DUE-001`<br>`CAL-FACE-002`<br>`CAL-FACE-008` |
| :883 | `GET /api/calendar/todos`<br>`PUT /api/calendar/todos/{id}`<br>`DELETE /api/calendar/todos/{id}` | `CAL-TIME-002`<br>`CAL-TIME-004`<br>`CAL-DUE-004` |
| :884 | `GET /api/calendar/reminders`<br>`PUT /api/calendar/reminders/{id}`<br>`DELETE /api/calendar/reminders/{id}` | `CAL-TIME-005`<br>`CAL-TIME-006`<br>`CAL-DUE-008` |
| :1876 | `PUT /api/finance/recurring` | `FIN-BUD-006` |
| :2153 | `GET /api/homeos/dynamics` | `HOM-TIME-009` |
| :2253 | `GET /api/homeos/app/bundles?codes=` | `HOM-SYNC-014` |
| :2302 | `PUT /api/homeos/members/{id}/role`<br>`PUT /api/finance/transactions/{id}` | `HOM-MEM-007`<br>`HOM-MEM-008` |

## 二、非一期接口（不进入分母）

| PRD 行 | 接口 | 归属 | 说明 |
|---|---|---|---|
| :426 | `POST /api/homeos/rules` | P6 | P6 能力（规则引擎 / AI 归类随 P6 交付） |
| :432 | `GET /api/{code}/sync/changes` | {code} | {code} 面属 P2-P6 出生期 |
| :434 | `POST /api/homeos/ai/classify` | P6 | P6 能力（规则引擎 / AI 归类随 P6 交付） |
| :455 | `GET /api/{code}/dead-letters` | {code} | {code} 面属 P2-P6 出生期 |
| :1013 | `POST /api/purchase/lists` | purchase | purchase 面属 P2-P6 出生期 |
| :1014 | `POST /api/purchase/lists/{id}/items` | purchase | purchase 面属 P2-P6 出生期 |
| :1015 | `PUT /api/purchase/items/{id}` | purchase | purchase 面属 P2-P6 出生期 |
| :1016 | `POST /api/purchase/inventory` | purchase | purchase 面属 P2-P6 出生期 |
| :1017 | `POST /api/purchase/inventory/{id}/out` | purchase | purchase 面属 P2-P6 出生期 |
| :1018 | `GET /api/purchase/inventory/alerts` | purchase | purchase 面属 P2-P6 出生期 |
| :1019 | `GET /api/purchase/inventory?location=fridge&expiring_within=2d` | purchase | purchase 面属 P2-P6 出生期 |
| :1020 | `POST /api/purchase/purchases` | purchase | purchase 面属 P2-P6 出生期 |
| :1021 | `GET /api/purchase/purchases/stats` | purchase | purchase 面属 P2-P6 出生期 |
| :1022 | `POST /api/purchase/coupons` | purchase | purchase 面属 P2-P6 出生期 |
| :1023 | `POST /api/purchase/stocktakes`<br>`PUT /api/purchase/stocktakes/{id}` | purchase | purchase 面属 P2-P6 出生期 |
| :1138 | `POST /api/diet/recipes` | diet | diet 面属 P2-P6 出生期 |
| :1139 | `GET /api/diet/recipes` | diet | diet 面属 P2-P6 出生期 |
| :1140 | `POST /api/diet/menus` | diet | diet 面属 P2-P6 出生期 |
| :1141 | `POST /api/diet/menus/{id}/purchase` | diet | diet 面属 P2-P6 出生期 |
| :1142 | `GET /api/diet/fridge` | diet | diet 面属 P2-P6 出生期 |
| :1143 | `POST /api/diet/fridge/{item_id}/consume` | diet | diet 面属 P2-P6 出生期 |
| :1144 | `GET /api/diet/ingredients/alerts` | diet | diet 面属 P2-P6 出生期 |
| :1145 | `POST /api/diet/meal-records` | diet | diet 面属 P2-P6 出生期 |
| :1146 | `GET /api/diet/taste-prefs` | diet | diet 面属 P2-P6 出生期 |
| :1246 | `POST /api/trip/trips` | trip | trip 面属 P2-P6 出生期 |
| :1247 | `GET /api/trip/trips` | trip | trip 面属 P2-P6 出生期 |
| :1248 | `POST /api/trip/vehicles` | trip | trip 面属 P2-P6 出生期 |
| :1249 | `POST /api/trip/maintenance` | trip | trip 面属 P2-P6 出生期 |
| :1250 | `GET /api/trip/maintenance/alerts` | trip | trip 面属 P2-P6 出生期 |
| :1251 | `POST /api/trip/tickets` | trip | trip 面属 P2-P6 出生期 |
| :1252 | `POST /api/trip/travel-plans` | trip | trip 面属 P2-P6 出生期 |
| :1253 | `POST /api/trip/travel-plans/{id}/packing` | trip | trip 面属 P2-P6 出生期 |
| :1361 | `POST /api/kin/profiles` | kin | kin 面属 P2-P6 出生期 |
| :1362 | `GET /api/kin/profiles` | kin | kin 面属 P2-P6 出生期 |
| :1363 | `POST /api/kin/preferences` | kin | kin 面属 P2-P6 出生期 |
| :1364 | `GET /api/kin/preferences?member_id=&type=allergen` | kin | kin 面属 P2-P6 出生期 |
| :1365 | `POST /api/kin/care-tasks` | kin | kin 面属 P2-P6 出生期 |
| :1366 | `GET /api/kin/care-tasks/today` | kin | kin 面属 P2-P6 出生期 |
| :1367 | `POST /api/kin/anniversaries` | kin | kin 面属 P2-P6 出生期 |
| :1368 | `POST /api/kin/chores` | kin | kin 面属 P2-P6 出生期 |
| :1369 | `GET /api/kin/emergency/{member_id}` | kin | kin 面属 P2-P6 出生期 |
| :1370 | `POST /api/kin/pets` | kin | kin 面属 P2-P6 出生期 |
| :1473 | `POST /api/growth/growth-records` | growth | growth 面属 P2-P6 出生期 |
| :1474 | `GET /api/growth/growth-records/{member_id}` | growth | growth 面属 P2-P6 出生期 |
| :1475 | `POST /api/growth/courses` | growth | growth 面属 P2-P6 出生期 |
| :1476 | `POST /api/growth/homework` | growth | growth 面属 P2-P6 出生期 |
| :1477 | `PUT /api/growth/homework/{id}` | growth | growth 面属 P2-P6 出生期 |
| :1478 | `POST /api/growth/habits` | growth | growth 面属 P2-P6 出生期 |
| :1479 | `POST /api/growth/habits/{id}/checkin` | growth | growth 面属 P2-P6 出生期 |
| :1480 | `POST /api/growth/plans` | growth | growth 面属 P2-P6 出生期 |
| :1481 | `GET /api/growth/reports/{member_id}` | growth | growth 面属 P2-P6 出生期 |

## 三、校验结论

- ✅ 一期接口全部有 case 承接；无悬空 case 引用；无 MAP 幽灵键。
