# PRD 接口 → Case 追溯矩阵（自动生成）

> 本文件由 `docs/cases/gen_traceability.py` 从 `docs/prd-homecube.md` 生成，**请勿手改**——改 MAP 后重跑。
> 用途：证明「一期 PRD 的每个接口都有 case 承接」。这是**需求覆盖率**的唯一判据；
> `自动化` / `实现现状` 是另一条线，不影响本表——**case 不因实现缺席而消失**。
> 非一期接口（P2-P6 面、P6 能力）单列在末节，**不进入分母**，避免被误判为缺口。

**统计**：一期接口行 75 条（合计 107 个接口，全部有 case 承接）· 非一期 51 行（52 个接口）· 覆盖 case 291 条

## 一、一期接口追溯表

| PRD 行 | 接口 | 承接 case |
|---|---|---|
| :393 | `GET /api/homeos/family` | `HOM-SESS-002`<br>`HOM-FAM-004` |
| :394 | `GET /api/homeos/families`<br>`POST /api/homeos/families/switch` | `HOM-FAM-004` |
| :395 | `GET /api/homeos/members` | `HOM-MEM-011` |
| :396 | `POST /api/homeos/members/invite` | `HOM-MEM-004` |
| :397 | `GET /api/homeos/permissions` | `HOM-AUTH-014`<br>`HOM-AUTH-005` |
| :398 | `POST /api/homeos/reminders` | `HOM-TIME-005` |
| :399 | `POST /api/homeos/todos` | `HOM-TIME-002` |
| :400 | `GET /api/homeos/notifications`<br>`POST /api/homeos/notifications/read` | `HOM-NOTI-003`<br>`HOM-NOTI-007` |
| :401 | `POST /api/homeos/devices/push-token` | `HOM-NOTI-005` |
| :402 | `POST /api/homeos/events/publish`<br>`POST /api/homeos/events/subscribe` | `HOM-ADM-005` |
| :404 | `GET /api/homeos/family/modules` | `HOM-FAM-009`<br>`HOM-FAM-007` |
| :405 | `PUT /api/homeos/family/modules` | `HOM-FAM-006`<br>`HOM-FAM-008` |
| :406 | `GET /api/homeos/app/bundles` | `HOM-SYNC-014` |
| :407 | `GET /api/homeos/home/summary` | `HOM-SUM-001`<br>`HOM-SUM-003`<br>`HOM-SUM-004` |
| :408 | `GET /api/homeos/permissions/snapshot`<br>`GET /api/homeos/members/snapshot` | `HOM-AUTH-014` |
| :410 | `GET /api/homeos/search` | `HOM-SRCH-001`<br>`HOM-SRCH-002` |
| :412 | `GET /api/homeos/settings` | `HOM-APP-001`<br>`HOM-APP-002` |
| :413 | `PUT /api/homeos/settings` | `HOM-APP-001`<br>`HOM-APP-002` |
| :414 | `GET /api/homeos/app/version` | `HOM-APP-005`<br>`HOM-APP-006` |
| :415 | `POST /api/homeos/calendar/events` | `HOM-TIME-010` |
| :416 | `POST /api/homeos/votes` | `HOM-TIME-007` |
| :417 | `POST /api/homeos/auth/sms-code`<br>`POST /api/homeos/auth/login`<br>`POST /api/homeos/auth/refresh` | `HOM-SESS-001` |
| :418 | `POST /api/homeos/families` | `HOM-FAM-001` |
| :419 | `POST /api/homeos/family/invite/accept` | `HOM-FAM-003` |
| :420 | `POST /api/homeos/members`<br>`PUT /api/homeos/members/{id}` | `HOM-MEM-001`<br>`HOM-MEM-002` |
| :421 | `GET /api/homeos/calendar/events`<br>`GET /api/homeos/calendar/events/{id}` | `HOM-TIME-001` |
| :422 | `GET /api/homeos/todos`<br>`PUT /api/homeos/todos/{id}` | `HOM-TIME-002`<br>`HOM-TIME-003` |
| :423 | `GET /api/homeos/reminders`<br>`PUT /api/homeos/reminders/{id}` | `HOM-TIME-005`<br>`HOM-TIME-006` |
| :424 | `GET /api/homeos/dynamics` | `HOM-TIME-009` |
| :425 | `GET /api/homeos/board/messages`<br>`POST /api/homeos/board/messages` | `HOM-TIME-008` |
| :426 | `GET /api/homeos/votes/{id}`<br>`POST /api/homeos/votes/{id}/ballot` | `HOM-TIME-007` |
| :427 | `POST /api/homeos/data/export`<br>`GET /api/finance/reports/export` | `HOM-SYNC-013` |
| :428 | `DELETE /api/homeos/family/invites/{id}` | `HOM-MEM-005` |
| :429 | `DELETE /api/homeos/members/{id}` | `HOM-MEM-009` |
| :430 | `PUT /api/homeos/members/{id}/role` | `HOM-MEM-007`<br>`HOM-MEM-008` |
| :431 | `POST /api/homeos/family/transfer-owner` | `HOM-MEM-010` |
| :432 | `POST /api/homeos/family/leave`<br>`POST /api/homeos/family/disband` | `HOM-FAM-011`<br>`HOM-FAM-012` |
| :433 | `GET /api/homeos/data/backup-status`<br>`POST /api/homeos/data/restore` | `HOM-ADM-003`<br>`HOM-SYNC-011` |
| :434 | `GET /api/homeos/audit-events` | `HOM-ADM-001`<br>`HOM-SESS-005` |
| :435 | `GET /api/homeos/reconciliation-report` | `HOM-ADM-002` |
| :437 | `GET /api/homeos/dead-letters`<br>`POST /api/homeos/dead-letters/{id}/replay` | `HOM-SYNC-010` |
| :638 | `POST /api/finance/transactions` | `FIN-TX-002`<br>`FIN-TX-003` |
| :639 | `GET /api/finance/transactions` | `FIN-TX-004`<br>`FIN-TX-005` |
| :640 | `GET /api/finance/transactions/{id}` | `FIN-TX-006` |
| :641 | `PUT /api/finance/transactions/{id}` | `FIN-TX-007` |
| :642 | `DELETE /api/finance/transactions/{id}` | `FIN-TX-008` |
| :643 | `GET /api/finance/accounts` | `FIN-ACC-003`<br>`FIN-ACC-004` |
| :644 | `POST /api/finance/budgets` | `FIN-BUD-001` |
| :645 | `GET /api/finance/reports/summary` | `FIN-BAL-002`<br>`FIN-BAL-009` |
| :646 | `GET /api/finance/reports/export` | `FIN-EXP-001`<br>`FIN-EXP-002` |
| :647 | `POST /api/finance/ledgers` | `FIN-LED-001` |
| :648 | `POST /api/finance/settlements` | `FIN-SPLIT-002`<br>`FIN-SPLIT-003` |
| :649 | `POST /api/finance/loans` | `FIN-LOAN-001` |
| :650 | `POST /api/finance/goals` | `FIN-LOAN-004` |
| :651 | `GET /api/finance/categories` | `FIN-CAT-001`<br>`FIN-CAT-003` |
| :652 | `POST /api/finance/categories`<br>`PUT /api/finance/categories/{id}` | `FIN-CAT-001`<br>`FIN-CAT-007` |
| :653 | `PUT /api/finance/categories/{id}/archive`<br>`POST /api/finance/categories/{id}/restore`<br>`PUT /api/finance/categories/sort` | `FIN-CAT-003`<br>`FIN-CAT-005` |
| :654 | `POST /api/finance/accounts`<br>`PUT /api/finance/accounts/{id}` | `FIN-ACC-001`<br>`FIN-ACC-002` |
| :655 | `POST /api/finance/accounts/{id}/archive`<br>`POST /api/finance/accounts/{id}/restore` | `FIN-ACC-004`<br>`FIN-ACC-005` |
| :656 | `GET /api/finance/budgets`<br>`PUT /api/finance/budgets/{id}` | `FIN-BUD-002`<br>`FIN-BUD-004` |
| :657 | `PUT /api/finance/ledgers/{id}` | `FIN-LED-003` |
| :658 | `GET /api/finance/bills`<br>`POST /api/finance/bills`<br>`PUT /api/finance/bills/{id}`<br>`POST /api/finance/bills/{id}/paid` | `FIN-BUD-007`<br>`FIN-BUD-009` |
| :659 | `GET /api/finance/recurring`<br>`POST /api/finance/recurring`<br>`PUT /api/finance/recurring/{id}` | `FIN-BUD-005`<br>`FIN-BUD-006` |
| :660 | `POST /api/finance/transactions/{id}/restore` | `FIN-TX-008`<br>`FIN-TX-009` |
| :661 | `GET /api/finance/transactions?deleted=true` | `FIN-TX-009` |
| :662 | `GET /api/finance/ledgers` | `FIN-LED-002`<br>`FIN-LED-004` |
| :663 | `GET /api/finance/bills/{id}` | `FIN-BUD-009` |
| :664 | `GET /api/finance/settings`<br>`PUT /api/finance/settings` | `FIN-SET-001`<br>`FIN-SET-004` |
| :665 | `POST /api/finance/invoices`<br>`POST /api/finance/expenses` | `FIN-SPLIT-005`<br>`FIN-SPLIT-007` |
| :666 | `GET /api/finance/demo-items`<br>`POST /api/finance/demo-items` | `FIN-DEMO-001`<br>`FIN-DEMO-002` |
| :667 | `GET /api/finance/transactions/{id}/attachments`<br>`POST /api/finance/transactions/{id}/attachments` | `FIN-TX-014`<br>`FIN-EXP-007` |
| :1657 | `PUT /api/finance/recurring` | `FIN-BUD-006` |
| :1928 | `GET /api/homeos/dynamics` | `HOM-TIME-009` |
| :2022 | `GET /api/homeos/app/bundles?codes=` | `HOM-SYNC-014` |
| :2071 | `PUT /api/homeos/members/{id}/role`<br>`PUT /api/finance/transactions/{id}` | `HOM-MEM-007`<br>`HOM-MEM-008` |

## 二、非一期接口（不进入分母）

| PRD 行 | 接口 | 归属 | 说明 |
|---|---|---|---|
| :403 | `POST /api/homeos/rules` | P6 | P6 能力（规则引擎 / AI 归类随 P6 交付） |
| :409 | `GET /api/{code}/sync/changes` | {code} | {code} 面属 P2-P6 出生期 |
| :411 | `POST /api/homeos/ai/classify` | P6 | P6 能力（规则引擎 / AI 归类随 P6 交付） |
| :436 | `GET /api/{code}/dead-letters` | {code} | {code} 面属 P2-P6 出生期 |
| :795 | `POST /api/purchase/lists` | purchase | purchase 面属 P2-P6 出生期 |
| :796 | `POST /api/purchase/lists/{id}/items` | purchase | purchase 面属 P2-P6 出生期 |
| :797 | `PUT /api/purchase/items/{id}` | purchase | purchase 面属 P2-P6 出生期 |
| :798 | `POST /api/purchase/inventory` | purchase | purchase 面属 P2-P6 出生期 |
| :799 | `POST /api/purchase/inventory/{id}/out` | purchase | purchase 面属 P2-P6 出生期 |
| :800 | `GET /api/purchase/inventory/alerts` | purchase | purchase 面属 P2-P6 出生期 |
| :801 | `GET /api/purchase/inventory?location=fridge&expiring_within=2d` | purchase | purchase 面属 P2-P6 出生期 |
| :802 | `POST /api/purchase/purchases` | purchase | purchase 面属 P2-P6 出生期 |
| :803 | `GET /api/purchase/purchases/stats` | purchase | purchase 面属 P2-P6 出生期 |
| :804 | `POST /api/purchase/coupons` | purchase | purchase 面属 P2-P6 出生期 |
| :805 | `POST /api/purchase/stocktakes`<br>`PUT /api/purchase/stocktakes/{id}` | purchase | purchase 面属 P2-P6 出生期 |
| :920 | `POST /api/diet/recipes` | diet | diet 面属 P2-P6 出生期 |
| :921 | `GET /api/diet/recipes` | diet | diet 面属 P2-P6 出生期 |
| :922 | `POST /api/diet/menus` | diet | diet 面属 P2-P6 出生期 |
| :923 | `POST /api/diet/menus/{id}/purchase` | diet | diet 面属 P2-P6 出生期 |
| :924 | `GET /api/diet/fridge` | diet | diet 面属 P2-P6 出生期 |
| :925 | `POST /api/diet/fridge/{item_id}/consume` | diet | diet 面属 P2-P6 出生期 |
| :926 | `GET /api/diet/ingredients/alerts` | diet | diet 面属 P2-P6 出生期 |
| :927 | `POST /api/diet/meal-records` | diet | diet 面属 P2-P6 出生期 |
| :928 | `GET /api/diet/taste-prefs` | diet | diet 面属 P2-P6 出生期 |
| :1028 | `POST /api/trip/trips` | trip | trip 面属 P2-P6 出生期 |
| :1029 | `GET /api/trip/trips` | trip | trip 面属 P2-P6 出生期 |
| :1030 | `POST /api/trip/vehicles` | trip | trip 面属 P2-P6 出生期 |
| :1031 | `POST /api/trip/maintenance` | trip | trip 面属 P2-P6 出生期 |
| :1032 | `GET /api/trip/maintenance/alerts` | trip | trip 面属 P2-P6 出生期 |
| :1033 | `POST /api/trip/tickets` | trip | trip 面属 P2-P6 出生期 |
| :1034 | `POST /api/trip/travel-plans` | trip | trip 面属 P2-P6 出生期 |
| :1035 | `POST /api/trip/travel-plans/{id}/packing` | trip | trip 面属 P2-P6 出生期 |
| :1143 | `POST /api/kin/profiles` | kin | kin 面属 P2-P6 出生期 |
| :1144 | `GET /api/kin/profiles` | kin | kin 面属 P2-P6 出生期 |
| :1145 | `POST /api/kin/preferences` | kin | kin 面属 P2-P6 出生期 |
| :1146 | `GET /api/kin/preferences?member_id=&type=allergen` | kin | kin 面属 P2-P6 出生期 |
| :1147 | `POST /api/kin/care-tasks` | kin | kin 面属 P2-P6 出生期 |
| :1148 | `GET /api/kin/care-tasks/today` | kin | kin 面属 P2-P6 出生期 |
| :1149 | `POST /api/kin/anniversaries` | kin | kin 面属 P2-P6 出生期 |
| :1150 | `POST /api/kin/chores` | kin | kin 面属 P2-P6 出生期 |
| :1151 | `GET /api/kin/emergency/{member_id}` | kin | kin 面属 P2-P6 出生期 |
| :1152 | `POST /api/kin/pets` | kin | kin 面属 P2-P6 出生期 |
| :1255 | `POST /api/growth/growth-records` | growth | growth 面属 P2-P6 出生期 |
| :1256 | `GET /api/growth/growth-records/{member_id}` | growth | growth 面属 P2-P6 出生期 |
| :1257 | `POST /api/growth/courses` | growth | growth 面属 P2-P6 出生期 |
| :1258 | `POST /api/growth/homework` | growth | growth 面属 P2-P6 出生期 |
| :1259 | `PUT /api/growth/homework/{id}` | growth | growth 面属 P2-P6 出生期 |
| :1260 | `POST /api/growth/habits` | growth | growth 面属 P2-P6 出生期 |
| :1261 | `POST /api/growth/habits/{id}/checkin` | growth | growth 面属 P2-P6 出生期 |
| :1262 | `POST /api/growth/plans` | growth | growth 面属 P2-P6 出生期 |
| :1263 | `GET /api/growth/reports/{member_id}` | growth | growth 面属 P2-P6 出生期 |

## 三、校验结论

- ✅ 一期接口全部有 case 承接；无悬空 case 引用；无 MAP 幽灵键。
