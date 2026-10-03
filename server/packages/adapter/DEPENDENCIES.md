# External Dependency Selection

This document tracks all external service dependencies for HomeCube, their candidate vendors, current P1 status, and commercial launch deadlines.

> **本文档分两节，口径不同**：下方「Dependency Selection Table」登记**外部 SaaS 供应商**（PRD 19.1、19.3：供应商、单价、免费额度、限流、合同与数据出境评估）；「客户端库选型」一节登记**随包分发的运行时 npm 库**（无供应商、无计费、无数据出境）。两节判据不得互相冒用：客户端库不进供应商定版表，供应商也不按 npm 版本口径登记。

## Dependency Selection Table

| 依赖类型 | 候选供应商 | P1 状态 | 商用前定版时间 | 备注 |
|---|---|---|---|---|
| OCR | 百度 AI / 腾讯云 OCR / 阿里云 OCR | 桩实现 | P2 开始前 | 需评估准确率与成本 |
| ASR | 百度语音 / 腾讯云 ASR / 阿里云 ASR | 桩实现 | P2 开始前 | 需评估识别率与延迟 |
| 地图 | 高德地图 / 百度地图 / 腾讯地图 | 未实现 | P3 出行面出生前 | 饮食面无需地图 |
| 对象存储 | 本地文件系统 / 阿里云 OSS / 腾讯云 COS | 本地文件系统 | P2 分包下发前 | 决定 CDN 方案 |
| 短信 | 阿里云短信 / 腾讯云短信 | 固定测试验证码 | P2 商用前 | 登录验证码通道 |
| 推送 | 极光推送 / 个推 / 友盟 | 桩实现 | P2 商用前 | 站内信 + 推送双通道 |

## Notes

- **P1 Status**: All adapters are implemented as stubs during P1 phase to enable development without external dependencies.
- **Commercial Deadline**: Vendors must be selected and credentials configured before the specified deadline.
- **Evaluation Criteria**: Each vendor should be evaluated on accuracy, cost, latency, and reliability before final selection.

## 客户端库选型（npm 运行时依赖，非外部供应商）

**登记日期**: 2026-10-03

本节登记**进入客户端运行时的库**。它不涉供应商、单价、免费额度、限流、合同与数据出境，因此**不占用 P1 末附加门禁②的定版判据**（PRD 18.2 第 14 项、19.3 的两级定版口径只判上表）。按 PRD 14.4 的边界「属研发工具者不得进入产品依赖清单」，构建期工具（`vite`、`typescript`、`@dcloudio/uni-cli-shared`、`@dcloudio/vite-plugin-uni`）不在此登记，只登记随包分发的运行时依赖。

| 库 | 版本 | 登记位置 | 用途 | 状态 |
|---|---|---|---|---|
| `pinia` | `2.1.7`（**精确固定，不用 `^` 区间**） | `web/package.json` → `dependencies`；`web/src/main.ts` → `app.use(createPinia())` | 主包 shell 级 store `web/src/stores/home.ts` 的载体 | 已定版（P1，随 shell 首页交付） |

### 用途与需求出处

- 承载**跨路由会话态**：时间窗 `period`（`'month' | 'quarter' | 'year'`）、面集合 `faces[]`（`FaceStatus[]`）、未读计数 `unreadCount`、当前家庭 `family`；另含 `dueToday`、`dynamics`、`greeting`（按家庭时区与 `Member.relation` 计算）与 `setPeriod` / `updateSummary` / `clearData`。
- 需求出处：`docs/p1-page-structure-navigation.md` §2.3 末段「**页面内状态的归属**」——跨页共享的状态（当前家庭、角色快照、时间窗 `period`、离线队列游标、免打扰）**一律由主包 shell 级 store 按 `(code, feature)` 或 `family_id` 持有**。这条规定使 shell 级 store 必须存在，pinia 即其实现载体；不新增架构决定，只登记选型。

### 范围与限制

- 同段明文：**分包不得持有跨路由的会话级状态**，页面局部状态（表单草稿、筛选展开态）随页面销毁。故 pinia store 的**唯一归属地是主包 `web/src/stores/`**，分包目录内不得新建 store。
- 现状（2026-10-03 实测）：`pages/homeos/{home,dynamics,messages,mine}` 读写该 store；分包 `pages/finance/flow/index.vue` **只读** `homeStore.period`，所有权仍在主包，属允许形态。**后续若在分包内出现 `defineStore`，即为违反 §2.3，按回归缺陷处理。**

### 版本口径：为何锁 `2.1.7` 且不用 `^`

- uni-app 工具链把 Vue 钉在 **3.4.21**：`@dcloudio/uni-app`、`uni-h5`、`uni-cloud`、`uni-shared`、`uni-cli-shared`、`uni-h5-vite`、`uni-nvue-styler`、`vite-plugin-uni` 均以精确版本依赖 `@vue/shared@3.4.21`；实测 `node_modules/@vue/shared` 与 `node_modules/vue` 均为 3.4.21（`package-lock.json` 亦解析为 3.4.21）。
- pinia 的 Vue 3 peer 区间随版本上移（逐版本实测 `npm view`）：**2.1.7–2.2.4 = `^2.6.14 || ^3.3.0`**（兼容 3.4.21）→ **2.2.5–2.2.8 = `^2.6.14 || ^3.5.11`** → **2.3.0–2.3.1 = `^2.7.0 || ^3.5.11`**。后两段要求 Vue ≥ 3.5.11，与 uni-app 的 3.4.21 冲突。**兼容上界因此是 2.2.4，不是 2.1.7**；本次取 2.1.7。
- 不写 `^` 的实测理由：`^2.1.7` 会浮动到 2.x 最新 **2.3.1**，隔离树里 `npm install --dry-run --strict-peer-deps` 报 `ERESOLVE unable to resolve dependency tree` —— `peer vue@"^2.7.0 || ^3.5.11" from pinia@2.3.1` 对 `Found: vue@3.4.21`。写成区间等于把「uni-app 何时抬 Vue」这个未定事件交给 npm 的浮动解析决定。

### 安装口径：`--legacy-peer-deps`（且明确不用 `--force`）

- 冲突源不是 pinia 自身的 vue peer（2.1.7 的 `^2.6.14 || ^3.3.0` 已被 3.4.21 满足），而是它的 `peerOptional` **`@vue/composition-api@^1.4.0`**：该区间浮动到 **1.7.2**，其 peer 为 `vue ">= 2.5 < 2.7"`——一个 **Vue 2 的组合式 API 兼容层**，对本 Vue 3 uni-app 项目不适用，**有意不安装**（`web/node_modules/@vue/composition-api` 不存在；`web/package-lock.json` 内该包仅以 `peerDependencies` + `optional: true` 元数据出现，无安装节点）。`vue-demi@0.14.10` 另带一条同族的 `peerOptional @vue/composition-api@^1.0.0-rc.1`，指向同一个 shim。strict 解析仍因此报 `ERESOLVE could not resolve` / `Conflicting peer dependency: vue@2.6.14`。
- `--legacy-peer-deps` 后的实测产物树：`pinia@2.1.7 + vue-demi@0.14.10 + vue@3.4.21`，无 `@vue/composition-api`。
- **`--force` 已被否决**：隔离环境下实测两个 flag 都能装完，差别不在成败而在**放开的范围**——`--force` 接受任何冲突（npm 原话为接受「incorrect (and potentially broken) dependency resolution」），会把未来 vue↔pinia 的**真实**断裂一并吞掉；`--legacy-peer-deps` 只跳过 peer 校验这一件事，与「一条不适用的可选 Vue 2 peer」这个已知且唯一的偏差相称。本仓安装口径固定为 `--legacy-peer-deps`。

### 升级约束

- 本条版本**锁在 uni-app 的 Vue pin 上**：`vue` 越过 3.4.21 与 pinia 升 2.2.5+ / 2.3.x **必须同批评估、同批定版**，不得单动一侧。
- 前置条件：uni-app 侧先发布把 `@vue/shared` 抬到 ≥ 3.5.11 的版本（本仓 `@dcloudio/*` 为 `3.0.0-5020620260917001`）。届时若升 pinia 2.3.x，可顺带消掉 composition-api 的可选 peer（**2.3.0 起该 peer 已移除**），`--legacy-peer-deps` 口径同时复议。

## References

- PRD 19.3: P1 dependency selection requirements
- PRD 19.1 依赖总表 / PRD 18.2 第 14 项（P1 末附加门禁②）/ PRD 14.4（研发工具不得进入产品依赖清单）：上表供应商口径与「客户端库选型」分节的边界依据
- docs/p1-page-structure-navigation.md §2.3「页面内状态的归属」：`pinia` 的用途与分包限制依据
- docs/p1-tech-plan.md §十一 S19: P1-M2 closeout requirements
