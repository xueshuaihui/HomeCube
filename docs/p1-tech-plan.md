# 家立方 HomeCube · P1 技术方案（底座 + 财务面与日历面整面）

> **文档性质**：研发交付物，不是需求文档。需求唯一权威源是 `docs/prd-homecube.md`（24.4）；本方案只回答「15.5 的 P1 十五项交付在八服务目标拓扑、服务随面出生的当期形态下怎么落地」，不新增、不裁剪任何需求口径。与本方案冲突的需求表述以 PRD 为准；PRD 未覆盖的技术分叉在第十五章给出定版口径，属 P1 契约。
>
> **范围**：P1 = **底座 + 财务面与日历面整面**的垂直切片（双面出生，定版 ㉚），内分两个验收点——**M1**（底座 + 财务单人闭环 + 日历面出生，28-33 周 = 14-16 个 Sprint）与 **M2**（财务深度域与整面，10-12 周 = 5-6 个 Sprint）。**M1 未过不得开工 M2**（12.7 第 2 条）。收口判定 = 15.5 十五项 + 19.2 的 M1 附加门禁三项 + P1 末附加门禁三项。
>
> **当期形状**（**P1 期末态 = S5 出生之后**）：实建 **3 个服务**（`svc-homeos` + `svc-finance` + `svc-calendar`）、3 个 schema、3 个专属账号、3 条迁移序列、3 条源流、2 个前端分包（`pages/calendar` 与 `pages/finance`，经 `app/bundles` 清单按需加载）；另外五个 code 只以 **registry 条目**存在，不建服务、不建 schema、不建分包、不建流（12.7 第 1 条、卷首第 12 项）。**出生时点（㉚ §8）**：`svc-calendar` 及其 schema、专属账号、第三条迁移序列、流 `HC_CALENDAR`、`pages/calendar` 分包**统一随 S5 出生**（§十一），**S1~S4 磁盘上只有 `svc-homeos` + `svc-finance` 两个服务**，对应 2 服务 / 2 schema / 2 账号 / 2 条序列 / 2 条源流 / 1 个分包 / Compose 常驻 **7** 个容器（PRD 口径 5 个）。**registry 的八个 code 在 S1 一次性登记——登记的是命名域，不是服务出生**（§1.3、§十五 E）。**七面是面目录的当前条目数、不是家庭固定拥有的数量**：一个家庭实际挂载哪些面由 `homeos_family_module` 配置（18.8）；**日历面是其中的必选面，恒启用、不可停用（㉚）**。
>
> **明确不做**（依 12.3、15.6、24.5）：其余五面任何业务功能；规则引擎与跨面联动业务（P6）；AI 归类与预测（P6）；隐私政策与用户协议文本（P1 末附加门禁）；除 OCR/ASR 外的依赖选型定版（P1 末附加门禁②）；真实推送凭证（P1 末附加门禁③）；Kubernetes、服务网格、独立网关、分布式事务、Redis、JetStream 集群。P1 期间一切未定版外部服务以 `packages/adapter/*` 接口 + 本地实现或桩交付。

---

## 一、运行时拓扑与工程结构

### 1.1 当期拓扑（卷首第 12 项、23.2 第 1 条）

同机 Docker Compose 常驻 **8 个容器**（nginx + **3 个服务** + postgres16 + nats + web + admin；八服务全出生时为 **13 个**，本方案不预建）——**这一行是 P1 期末态**：第三个服务 `svc-calendar` 随 **S5** 出生（§十一、㉚ §8），**S1~S4 期间只有 `svc-homeos` + `svc-finance` 两个服务、Compose 常驻 7 个容器**。**计数基线**：本行的 8 含 `web`/`admin` 两个静态容器，PRD 12.9 与 15.5 第 1 项写的「当期 6 个容器」= 3 服务 + nginx + JetStream + Postgres、不含那两个静态容器，且**它写的同样是 P1 期末态**（S1~S4 对应 5 个）——两口径是同一批容器的两种数法，不是两个拓扑：

```
                ┌────────────────────────────────┐
  client ──────▶│ nginx :443/80  前缀反代 + TLS + 静态 │  客户端只对一个 base URL
                └───────┬────────────────────────┘   (18.7 第 5 条)
   /api/homeos/  /api/finance/  /api/calendar/   其余五个前缀 → 404「即将上线」
          ┌─────────────┴──────────────────────────┐
          │ svc-homeos   svc-finance   svc-calendar │  3 个 Go 服务，各一镜像一发布线
          └─────────────┬────────────────────┬─────┘
                        │                    │
         ┌──────────────▼──────┐   ┌─────────▼──────────────┐
         │ postgres16          │   │ nats (JetStream)       │
         │ 3 schema/3 账号      │   │ HC_HOMEOS HC_FINANCE   │
         │ （目标 8/8）         │   │ HC_CALENDAR            │
         └─────────────────────┘   │ + HC_ARCHIVE + HC_DL   │
                                   └────────────────────────┘
     另加：web（静态）、admin（静态）两个常驻容器，
          与一次性 migrate 初始化容器（重放三条序列后退出，不计入常驻数）
```

**上图是 P1 期末态（S5 起）**：`svc-calendar`、schema `calendar` 与账号 `hc_calendar`、第三条序列、流 `HC_CALENDAR`、`pages/calendar` 分包**统一随 S5 出生**（§十一 S5 卡），**S1~S4 那张图里只有 `svc-homeos` + `svc-finance`、2 schema/2 账号、`HC_HOMEOS` + `HC_FINANCE` 两条源流、Compose 常驻 7 个容器**；未出生期 `/api/calendar/` 前缀由 Nginx 兜「即将上线」，与其余五个前缀同形态（下面第一条）。

- **不设网关服务**：Nginx 只做前缀转发、TLS 与静态资源，**不做鉴权**；鉴权在每个服务的中间件里由同一 SDK 完成（23.2 第 8 条）。未出生面的 `/api/{code}/` 前缀由 Nginx 直接返回「**即将上线**」（18.7 第 2 条），**不得**为凑齐路由而留空服务容器。「**未出生**」与「**本家庭未启用**」是两条不同路径、**两个不同用词**（18.8 定版二分）：前者无容器、由 Nginx 兜「即将上线」；后者前缀与服务都在，由该服务中间件按 `scope=module` 拒绝并落审计，客户端呈现「未启用」。**第三个词是「依赖未就绪」（定版 ㉙）**：它**不走上面任何一条路径**——本面已启用、`/api/{code}/` 正常受理、请求照发，只是**某个功能项依赖的他面还没出生**，降级发生在**业务响应内容与客户端呈现层**，不在 Nginx 也不在鉴权中间件。当期唯一实例是 P3 饮食面的忌口过滤（数据归属 `kin`，P4 才出生）。**实现上不得让这一态借用「未启用」的错误码或「暂不可用」的入口态**：三者混用会让 19.2#9③ 的「未挂载面零请求」断言去约束一个应当正常发请求的功能。
- 发布顺序恒为 `svc-homeos` 先于业务面服务（SDK 与事件目录的提供方先行），Compose 里 `finance` 与 `calendar` 各用 `condition: service_healthy` 前置（**`calendar` 那一条随 S5 出生才进 compose；S1~S4 只有 `finance` 一条**）。**`svc-homeos` 反过来同步依赖 `svc-calendar` 的那一跳只有首页 `due_today` 一处**（17.3 方式一，超时 ≤300ms、至多重试 1 次、失败降级「暂不可用」+ 最后值与时效戳，5.7、5.8），它不构成启动顺序上的相互等待——calendar 不可达时 homeos 照常健康、首页照常出其余三区。
- 单机资源门槛按**当期容器数**实测（22.1 全栈资源行）：**S1~S4 是 7 个**常驻、**S5 起是 8 个**（一次性 `migrate` 容器退出后不计入），八服务全出生时才是 **13 个**与 4C/8G 目标阈值。实测超过当期阈值走 12.9 第 ④ 项**同镜像多进程合并部署**降级，不改代码拓扑，且写进当期风险记录。

### 1.2 仓库布局（23.4）

```
HomeCube/
├── server/
│   ├── services/                      当期只有三个（S5 起；S1~S4 是两个，svc-calendar 随 S5 出生）
│   │   ├── svc-homeos/                handler/service/repo/model/dto + 本服务订阅器
│   │   ├── svc-finance/               同上
│   │   └── svc-calendar/              同上（日历面唯一可写服务，17.2、㉚）
│   │   （purchase/diet/trip/kin/growth 的目录不创建，出生期才建）
│   ├── packages/                      仅三条约束：无业务语义、可被服务依赖、不得反向依赖服务
│   │   ├── authz/                     JWT 校验 + 权限判定 + pver 缓存（语义由 svc-homeos 维护，16.6）
│   │   ├── bus/                       outbox 投递器、JetStream 封装、durable consumer、死信
│   │   ├── rclient/                   跨服务只读客户端（超时/重试/降级三要素强制声明，15.7）
│   │   ├── sync/                      change_log 追加、delta 查询、幂等表基类
│   │   ├── proj/                      投影表写入器与重建
│   │   ├── obs/                       结构化日志、指标（常量标签 code）、审计上报
│   │   ├── registry/                  域表：code ↔ 服务名/schema/路由/subject/分包/迁移目录
│   │   └── adapter/                   外部服务边界：push/ocr/asr/map/storage/ai
│   ├── contracts/                     openapi/{code}.yaml、events/{code}.yaml，版本化
│   ├── migrations/                    当期三条序列（S5 起；S1~S4 两条）：migrations/{code}/{code}_0007_desc.up.sql
│   └── test/                          门禁级集成测试（权限矩阵、并发写、重放、对账）
├── web/                               uni-app(Vue3)：shell + pages/calendar + pages/finance
├── admin/                             Vite + React：最小管理后台
├── deploy/                            compose、nginx.conf、备份/恢复/回滚、env 样例
└── .github/workflows/                 CI 五道门禁（23.5），本仓库自建，不共享 workflow
```

**依赖方向由 CI 静态检查（23.5 第 4 道）**：

- `services/* → packages/*`，允许；`packages/* → services/*`，禁止；
- `services/A → services/B` 的任何 import 禁止——这是唯一能在**编译期**证明服务边界存在的手段，比运行期约定强；
- 跨面调用只能出现两种形态：`rclient.Call`（只读）与 `bus` 订阅/发布；
- **registry 里没有实建服务的那五个域，任何目录、迁移文件、分包、subject 配置出现即门禁 2/4 失败**（不是靠人工记住「这期还不该有它」）。

**S1 骨架落盘后，本节 tree 注释需要四点实测补充**（只登记磁盘上已成立、且不引向任何待定版的部分）：

- **`packages/obs` 的实际职责比注释多两项、少一项**。多：Gin engine 装配与 `/healthz` 探针聚合（PRD 23.2 第 10 条那两个运维端点加路由域自检），以及 §2.1 的 DSN 形态校验与 `UPLOAD_DIR` 落点校验——理由是上面那条 `services/A → services/B` 的 import 禁令本身：装配逻辑若在三个 cmd 各写一遍就是三份 `/healthz` 与三份路由规则，与 §1.3「一张域表是唯一真源」相反。少：**审计上报未实现**——审计信封、22.5 九类、`{code}.audit.recorded` 与业务写同一 outbox 事务落盘，载体分别是 S2 与 S4/S5，本包不留占位函数；注释里这一项属 §10.3 那条路径的将来时，今天还没落地。
- **分包根有一层前缀在域表之外**：`BundlePath` 存的是 PRD 17.1「前端分包」列的原样形态 `pages/{code}`，它相对 uni-app 源根 `web/src`，所以磁盘上的真实页面根是 `web/src/pages`（`homeos` 为主包目录；分包目录 **S1~S4 只有 `finance`，`calendar` 随 `svc-calendar` 在 S5 出生**，㉚）。`web` → `src` 这一段不进域表：它是工程结构，不是 PRD 17.1 登记的七个命名维度之一。registry 侧的文件系统断言（`server/packages/registry/registry_fs_test.go`）因此把该根**从域表派生**（取 homeos 行 `BundlePath` 的目录段再拼 `web/src`）而不是硬编码；它的目录列举函数在探针根不存在时直接 `t.Fatalf`——此前一版误信一个磁盘上并不存在的 `web/pages` 路径，使「不该存在的目录不存在」退化成空集通过，这条 `t.Fatal` 就是防同一种假绿。
- **服务骨架的形状 = 一个 code + 一个监听地址 flag**：`server/services/svc-{code}/cmd/svc-{code}/main.go` 里只声明本进程的 code、`-addr` 与可选的地址 env 键名，其余（路由组前缀、schema、专属账号、探针名、日志与指标的 `code`、NATS 客户端标识）全部从 registry 行查出；配置校验一次报全（`errors.Join`）并 `os.Exit(1)`，不带病启动。**各 cmd 互不 import**（S1~S4 是两个 cmd，第三个 cmd 随 `svc-calendar` 在 S5 出生，S5 起三个），`packages/*` 不 import `services/*`——`main.go` 的 import 只有 `packages/obs` 与标准库，`obs` 只 import `packages/registry`。
- **忽略面在 `.gitignore`，其卷首把权威源指回本节**：Go 产物 `/server/bin/`、`*.test`、`*.out`、`*.exe`；前端产物 `node_modules/`、`web/dist/`、`web/unpackage/`、`admin/dist/`；运行期数据 `deploy/data/`（备份包与 19.2#13 的长压归档件）、`*.log`；本机 env 只提交 `*.example`，`/deploy/env.local` 忽略。附件目录是**两条**：`/uploads/` 与 `/server/uploads/`。第二条不是冗余——`make dev-{code}` 用 `go -C server run`（§十三），进程 cwd 是 `server/`，而 `UPLOAD_DIR` 按 §8.1 与 PRD 15.7 写的是相对形态 `./uploads/{family_id}`，真实票据因此落在 `server/uploads/`，锚在仓库根的 `/uploads/` 拦不住它；origin 是 PUBLIC 仓库。

### 1.3 命名一致性：一个 code，七处生效（17.1、15.5 第 1 项）

`packages/registry` 里一张域表是唯一真源，**P1 一次性登记八个 code**（**登记 ≠ 出生**：S1 登记的是七类命名域，实建服务、schema、账号、序列、流、分包按出生期增加，`calendar` 那一族随 S5 出生），`registry.Domains()` 被服务启动、迁移工具、前端构建脚本与 CI 共用。这张表**是面目录，不是封闭清单**（18.8）：八个是当前条目数（HomeOS + 七个业务面，日历面随 ㉚ 新增一行），追加一个面即在域表加一行，其余六类命名域自动随之生效（**共七类命名维度**，即下表七行——「七类」是命名条数、不是系统个数），「家庭启用了哪些面」另有权威源（`homeos_family_module`），两者不互为替代：

| 维度 | 取值 | P1 实际落地 | 校验点 |
|---|---|---|---|
| 服务名 | `svc-{code}` | 3 个 | 镜像名、Compose 服务名、容器名 |
| schema | `{code}` | 3 个 | 连接串 `search_path={code}`、账号名 `hc_{code}` |
| 路由 | `/api/{code}/*` | 3 组 + 5 个 404 前缀 | Gin 路由组 AST 检查 |
| subject | `{code}.` | 3 条流 | JetStream 流 subjects、事件目录 |
| 表前缀 | `{code}_` | 3 个前缀 | GORM `TableName()`、迁移文件内容 |
| 前端分包 | `pages/{code}` | 2 个（calendar 与 finance） | `pages.json` 三查：**无 `tabBar` 字段**、**分包集合 = 已出生业务域 = registry 实建域 − `homeos`**（HomeOS 是主包不是分包，写成「= 已出生域」会在 P1 必然失败）、路由全三段式（导航文档 §2.4） |
| 迁移序列 | `migrations/{code}/` | 3 条 | golang-migrate 目录 |

**本列「P1 实际落地」写的是 P1 期末态（即 `svc-calendar` 在 S5 出生之后）**：S1~S4 期间这几行各减一，是 **2 个服务 / 2 个 schema 与账号 / 2 组路由 + 6 个 404 前缀（含 `/api/calendar/`）/ 2 条流 / 2 个表前缀 / 1 个分包（`pages/finance`）/ 2 条迁移序列**；八行的**登记**仍是 S1 一次性做完的，出生的是 `calendar` 那一族（§十一 S1 卡与 S5 卡）。

**CI 校验的是「已存在的东西必须合法」与「不该存在的东西必须不存在」两件事**：八域的命名规则全生效（例如有人提交 `migrations/diet/…` 即失败），但只有登记为实建的域才要求文件存在。任一处偏离即门禁 2/4 失败，「架构漂移」因此在合并前被拦住。

**这张表对非 Go 消费者的出口** = `server/packages/registry/cmd/registryd`。该命令自身不持有第二张表，只打印包 API 的返回值，`--format` 取四个值：

- `json`：整表，`registry.Domains()` 的登记序，每行含全部存值字段；
- `codes`：一行一个 code，同序；
- `bundles`：一行一个分包集合 code，即 `registry.BundleSet()` 的序——第 2 查要拿去比对 `subPackages[].root` 的那一份；
- `meta`：一份 JSON，五个键 `currentPhase` / `implemented` / `unborn` / `bundles` / `bundleRoots`。

**`bundles` 与 `meta` 两个出口存在的理由是 §1.3 自己**：当期期号与派生出的分包集合只活在 Go 表里，shell/Node 侧没有这两个出口就得自带一份「当期」，那正是唯一真源要禁掉的第二源；`bundleRoots` 给的是各域存值 `BundlePath`，脚本不得自己拼 `pages/` 前缀。`--format` 传这四个之外的值即报错并以退出码 1 结束，不静默回落到默认值。当期消费者是 `web/scripts/check-pages.mjs`（现场取 live 值）、`web/scripts/gen-domains.mjs`（写 `web/generated/domains.json` 快照，选定「提交 + 断言逐值一致」而不是忽略重生成）与 `deploy/gen-nginx-unborn.mjs`（用 `meta.unborn` 与 `json` 全表生成未出生面的 nginx include）。

**七个命名维度之外，另有十一处名字是从存值派生的**（`Domain` 上的方法，域表不为其多存一格；每条都能在代码里逐字对上，出处按代码注释登记）：

- `RoutePrefixGlob()` → `/api/{code}/*`。存值是归一化的前缀形态 `/api/{code}/`（Gin 路由组与 Nginx location 拼的是前缀），glob 由这个方法给；门禁 4 的 AST 检查比存值前缀，不是比 glob。
- `StreamName()` → `HC_{CODE}`（code 大写）：§3.1 流表三行原文即 `HC_HOMEOS` / `HC_FINANCE` / `HC_CALENDAR`，其余五条在同一个表里写成 `HC_PURCHASE … HC_GROWTH`。
- `StreamSubjectPattern()` → `{code}.>`；`ArchiveSubjectPattern()` → `arch.{code}.>`；`DeadLetterSubjectPattern()` → `dl.{code}.>`：§3.1 的 subjects 列与 `HC_ARCHIVE` / `HC_DL` 两行。三条都从 `SubjectPrefix` 派生，所以带点的模式与拼接结果不可能各走一边。
- `VersionTable()` → `schema_migrations_{code}`（§2.2「落在自己 schema 内」）。
- `ProjectionTable(src)` → `{code}_proj_{src}`（§2.3 与 PRD 17.3），参数 `src` 是**他域**的 code。
- `MigrationFileName(seq, desc)` → `{code}_{序号补零 4 位}_{描述}.up.sql`：补零宽度取自两份文档自己的例子（PRD 17.6 的 `finance_0007_…` 与 `purchase_0001_…`、§1.2 的 `{code}_0007_desc.up.sql`），宽度在本包只有一处常量声明；PRD 17.1 与 23.2 第 6 条只说「序号」，P2-P6 出现第五位属文档问题而非代码决定。
- `OpenAPIContractPath()` → `contracts/openapi/{code}.yaml`、`EventsContractPath()` → `contracts/events/{code}.yaml`：两条都相对 `server/`（§1.2 与 §3.2），也就是那个唯一 Go module 的根。
- `DSNSearchPath()` → `search_path={code}`：本节 row 2 的校验点与 §2.1 的 DSN 示例。

另有两件事**刻意不在域表里**，同属本节的口径：端口、env 键名、镜像名不存字段（PRD 17.1 登记的七类不含它们，镜像与容器名在 compose 侧由 `svc-{code}` 派生）；§3.4 的 durable consumer 名在域表里**没有**派生方法，代码里也没有任何位置生成它——它的两种读法尚未归一，本节不替它选写法。至于流的创建与 subject 授权，那是 S2 的载体，`packages/bus` 今天是占位空目录。

---

## 二、数据边界：单集群，当期三 schema（S5 起三个；S1~S4 两个）

### 2.1 账号即边界（23.2 第 2 条）

一个 `postgres16` 实例，**P1 期末（S5 起）三个 schema、三个角色**（S1~S4 为两个 schema、两个账号：`homeos` + `finance`；目标形态 8/8）：

```sql
CREATE SCHEMA finance;
CREATE ROLE hc_finance LOGIN PASSWORD '…';
GRANT USAGE ON SCHEMA finance TO hc_finance;
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO hc_finance;
REVOKE ALL ON SCHEMA public FROM hc_finance;
```

- 每个服务只用自己那串 DSN（`user=hc_finance dbname=homecube search_path=finance`；`homeos` 与 `calendar` 各同构一份，**`hc_calendar` 那串随 `svc-calendar` 在 S5 出生**——S1~S4 只有 `hc_homeos`/`hc_finance` 两串，S5 起三串），**跨 schema 在权限层就做不到**，不靠代码约定。
- 表名在 schema 内仍带 `{code}_` 前缀（17.1 的双保险）：一旦出现他域前缀的建表，门禁 2 立刻发现——包括「提前替还没出生的面建表」这种 P1 特有形态。
- 无跨 schema 视图、DBLink、扩展共享；`public` schema 不放业务对象。
- 建 schema 与建账号的动作只发生在 `make up` 与迁移容器里，不由服务进程在运行期自触发（避免账号密码进应用配置）。

### 2.2 迁移（15.5 第 1 项、17.6、23.5 第 1 道）

**每服务一条独立序列**，golang-migrate 一个实例一条序列、各自版本表 `schema_migrations_{code}`（落在自己 schema 内）。CI 逐服务在空库重放自己那条序列，并校验代码声明的版本与迁移头部一致。**S1~S4 两条**（`homeos` / `finance`）、**S5 起三条**（加 `calendar`，随 `svc-calendar` 出生，§十一），其余五条随各面出生期新增——**序列数随出生期增加不是异常，是设计**。

约定：
- **版本表在第 0001 步显式创建**（见 `migrations/{code}/{code}_0001_base_schema.up.sql`），不依赖 golang-migrate 自动建表；表结构为 `(version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false)`。
- 迁移只允许 `CREATE TABLE`/`ALTER` 本域前缀的表；出现他域前缀即门禁 2 失败。
- **不存在跨服务外键**（17.3）：引用他域对象只存 `id`，无物理约束，完整性由每日对账兜住。这条纪律让各条序列彻底解耦，发布不需要跨域排序。
- 业务表必备列：`id uuid primary key`（UUIDv7，时间有序抑制索引膨胀）、`family_id uuid not null`、`version bigint not null default 1`、`created_at/updated_at`、`deleted_at/deleted_by`（15.7、23.2 第 9 条）。系统预置数据用可空 `family_id` 表达（财务的预置分类与预置账户即属此类）。
- 金额一律 `amount_cents bigint`；禁止浮点与 `numeric` 混用（15.7）。
- 索引第一列固定 `family_id`；流水表按 `family_id + occurred_at` 分区/索引（22.2），**P1 内就要按 6 万条量级建好索引口径**，不留给 M2 补课。

**落盘现状（S1，只登记磁盘上已有的，不追加规则）**：

- **时点校正（㉚ §8）**：本段的 `calendar` 那条序列（四步、8 个文件、含 `calendar_proj_homeos` 与五表口径）是 **S5 出生期的磁盘形态**，与 §2.2「`calendar` 序列随 `svc-calendar` 出生」同一条纪律；**S1~S4 骨架期落盘的是两条序列 × 4 步 × 2 = 16 个迁移文件**（`homeos`、`finance`），24 这个数是 P1 期末态。下面这段按「期末三条都在盘上」的形态描述各步内容，逐条读时把 `calendar` 侧的四步与五表一律推到 S5 卡。
- 三条序列各四步、每步 `up`/`down` 成对，磁盘上共 **24 个迁移文件**（3 序列 × 4 步 × 2 = 24），落在 `server/migrations/homeos/`、`server/migrations/finance/`、`server/migrations/calendar/`。四步的描述段依次是 `base_schema`、`outbox_dedupe_dead_letter`、`change_log_idempotency`、`proj_{他域 code}`——第四步三侧不同名：homeos 侧同一步建两张（`homeos_proj_finance` 与 `homeos_proj_calendar`）、finance 侧 `finance_proj_homeos`、calendar 侧 `calendar_proj_homeos`，与 §2.3 末行列出的**四张**投影表一一对应。`calendar` 那条序列的第 0001 步之后即建 `calendar_event`/`calendar_todo`/`calendar_reminder`/`calendar_recurring_rule`/`calendar_due_registration` 五表（5.7），**载体在 S5 那张卡**（骨架卡只建底座侧表族）。文件名形态即 §1.3 row 7 的 `migrations/{code}/` 与 §1.2 的 `{code}_{序号}_{描述}.up.sql`。
- `server/go.mod` 是这条序列所在的那个 module 的声明处：**整个 `server/` 目录只有一个 Go module**，module path 为 `github.com/xueshuaihui/HomeCube/server`，`go 1.27`，**没有 `toolchain` 指令**——构建期的宿主工具链由每个 go 调用点显式传的 `GOTOOLCHAIN=local` 钉住（§十五 该行）。该文件的注释块同时写明：**golang-migrate 故意不是这个 module 的依赖**，因为骨架卡禁止迁移执行器与自写 migrate driver（不存在 `server/cmd/migrate` 这类口），执行器属 compose 的一次性 migrate 容器侧（§2.1 末条「建 schema 与建账号只发生在 `make up` 与迁移容器里」、§10.1 的 migrate 容器行）。
- 「执行器最终落哪个文件/镜像、由哪张卡交付」与「迁移文件名口径是否需要在 `{code}_` 之外再加东西」是**待定版①**，本节不写结论；上面两条只复述磁盘上已经成立、且两种读法都不冲突的事实。

### 2.3 每个服务的底座侧表（同构命名，落各自 schema）

| 表 | 用途 | 为什么 per-service |
|---|---|---|
| `{code}_outbox` | 发布前落盘，投递器异步搬入 JetStream | 事务边界只在单库单 schema 内成立，跨服务无法同事务写总线 |
| `{code}_event_dedupe` | 消费幂等键 `(event_type, business_id)`（`business_id` 形态见 §3.3 与 PRD 11.4 取值约定） | 去重是消费方自己的状态，不能共享别人的表 |
| `{code}_dead_letter` | 死信落库与人工重放 | 死信归属消费方（23.2 第 10 条） |
| `{code}_change_log` | 同步增量游标源 | HomeOS 不代管他服务变更（15.5 第 5 项） |
| `{code}_idempotency` | `client_request_id` 去重 | 同上 |
| `{code}_proj_{src}` | 他域数据的本地投影 | 投影只允许本服务订阅器写，可随时重建（17.3）；**P1 有四张**：`finance_proj_homeos` 与 `calendar_proj_homeos`（两面各自的成员/权限快照）、`homeos_proj_finance`（首页财务格子状态句，见 6 章）、`homeos_proj_calendar`（首页日历格的状态句、角标与降级「最后一次值 + `as_of`」载体；表名是 PRD 5.7 给的唯一合法形态，**当日 `due_today` 切片不走投影而是 17.3 方式一只读调用**，5.8） |

**当期落盘的底座侧表实例**（上表的「现在有哪几张」，只登记名字与落在哪一步，不追加结构规则）：**S5 起**三条序列（S1~S4 为两条）的第 0002 步各建 `{code}_outbox`、`{code}_event_dedupe`、`{code}_dead_letter`，第 0003 步各建 `{code}_change_log`、`{code}_idempotency`，第 0004 步建投影表——homeos 侧两张 `homeos_proj_finance`/`homeos_proj_calendar`、finance 侧 `finance_proj_homeos`、calendar 侧 `calendar_proj_homeos`，即上表末行所说的「P1 有四张」已在磁盘上。六个名字全部命中 §10.2 第 2 道定版 ㉔ 的后缀白名单（`_outbox` / `_event_dedupe` / `_dead_letter` / `_change_log` / `_idempotency` / `_proj_*`），门禁 2 对它们只校验「前缀 = 所在服务」。去重表的分区口径、版本表落点等尚未定版的项本节一律不动，只登记到表名为止。

`svc-homeos` 额外持有 `homeos_family_module`（本家庭启用哪些面的唯一权威源，18.8）、`homeos_notification*`、`homeos_search_index`、`homeos_audit_log`、`homeos_event_archive`（冷存）与协作类对象 `homeos_board_message`/`homeos_vote`/`homeos_dynamic` 等全局对象；**到期对象注册表不在这批里**——原以 `homeos_due_registration` 名义登记的这张表随日历面出生改名 **`calendar_due_registration`**，与三对象、周期规则一同归 `svc-calendar`（17.2、5.7、㉚），底座一个到期字段都不再持有。**这批全局对象当期一张都还没建**——三条序列的四步（S5 起三条；S1~S4 只有 `homeos`/`finance` 两条）只建上表那六族底座侧表与四张投影表。

---

## 三、跨服务通信

### 3.1 JetStream 流规划（3.4.6、11.4）

| 流 | subjects | 保留 | P1 状态 |
|---|---|---|---|
| `HC_HOMEOS` | `homeos.>` | `max_age=90d`，`R=1` | 建 |
| `HC_FINANCE` | `finance.>` | 同上 | 建 |
| `HC_CALENDAR` | `calendar.>` | 同上 | **随 `svc-calendar` 在 S5 建**（㉚：日历面随 P1 出生，流同步建，**但「随 P1 出生」= 随 S5 那个出生时点，不是 S1**；P1 期末态是三条源流，S1~S4 只有两条，本流不存在 → 提前发 `calendar.*` 在中间件层即报 `no stream`，与本节末条同一纪律）。建好后 `calendar.reminder.fired` 与 `calendar.todo.completed` 都走这条流 |
| `HC_PURCHASE` … `HC_GROWTH`（5 条） | `{code}.>` | 同上 | **不建**，各面出生期建（registry 已登记命名） |
| `HC_ARCHIVE` | `arch.{code}.>` | `max_age=365d` | 建；归档消费者 `hc-archiver` 从当期在运的流复制，实现 22.4「热 90 天 / 冷 1 年」；回放走这条流 |
| `HC_DL` | `dl.{code}.>` | `max_age=90d` | 建；重试超限后 nack+term，同时写**消费方自己的** `{code}_dead_letter` |

- 每服务只被授权 publish 自己那条流（`$JS.API.STREAM.PUBLISH.{code}.*` ACL），发他域 subject 直接失败；未出生面的流不存在，因此「提前发他域事件」在中间件层就报 `no stream`，不靠评审拦。
- subject 命名即事件名：`{code}.{object}.{action}`（11.1），小写点分；版本不进 subject 而进信封 `version` 字段，破坏性变更走 `{action}_v2` 新事件名并保留旧名一个期（11.4）。
- 流不启用 workqueue：一条事件常有多个消费者，各消费者独立 durable consumer。

### 3.2 事件目录先于生产者（15.5 第 6 项）

`server/contracts/events/{code}.yaml` **P1 即登记八个 code 的主题域与 11.3 的事件清单**（含各自的启用期字段），`homeos.*` 另含 P1 新增的 `homeos.family.module.updated`（18.8）；`calendar.*` 两条随 ㉚ 换宿主登记：**`calendar.reminder.fired`**（原以 `homeos.` 前缀登记的触发事件，载荷含 `source_system`/`source_id`/`kind`/触发时刻，**由 `svc-homeos` 消费后走通知投递与推送**，5.5.8）与 **`calendar.todo.completed`**（完成回写，载荷与语义一字不改，只换前缀与发布方）。未出生面只有目录条目、无生产者、无消费者声明指向不存在的服务。这样做的收益：事件命名与归属在 P1 就被钉死，P2-P6 只扩不改；CI 的门禁 5 从第一天起就能判「发布未登记事件」。**登记目录 ≠ 创建流**，流与订阅器随出生期落地。

### 3.3 发布：事务内 outbox，提交后异步投递

```
业务事务: INSERT 业务行 + INSERT {code}_outbox(subject, envelope, status=pending)
投递器:   每 500ms 批量 100 行 → JetStream Publish（异步 ack）→ 成功置 sent，失败 attempts+1
崩溃恢复: 重启即扫 pending；attempts>10 告警（不丢，只是没送）
```

代价是事件传播多 ≤1 秒的固有延迟，因此 19.1 的收敛口径（P95 ≤5s、P99 ≤60s）在本实现下有余量。**明确不提供「发布即可见」语义**：任何要求同步可见的读都走自己 schema 或本地投影，不假装跨服务强一致。

### 3.4 消费：durable consumer + 幂等 + 退避 + 死信

- consumer 名 `{consumerCode}-{event_type}`（如 `homeos-finance-transaction-created`），`filter_subject` 精确匹配，`ack_wait=30s`，`backoff=[1s,10s,60s]`，`max_deliver=4`（对应 11.4 的至多重试 3 次）。
- 进 handler 第一件事：`INSERT ON CONFLICT DO NOTHING INTO {code}_event_dedupe`；已存在即 ack 返回——**重复投递不产生重复业务对象是表约束保证，不是代码纪律**。**去重键是 `(event_type, business_id)`，而 `business_id` 的形态由发布方按 PRD 11.4 的取值约定填写（同一业务对象可合法重复发生的事件必须带周期或版本后缀：`finance.budget.exceeded` = `{budget_id}:{period}`、`{code}.due.registered` = `{source_id}:{due_at}`、`calendar.reminder.fired` = `{reminder_id}:{fire_date}`（㉚ 换前缀，形态不变）、`homeos.family.module.updated` = `{family_id}:{code}:{version}`）**。**把 `business_id` 默认取成业务对象 id 会让同一对象的第二次合法事件被当重复丢弃**——预算逐月超支、提醒每次触发、面反复启停全部只生效一次，且症状是「什么都不发生」，排查成本极高；`contracts/events/{code}.yaml` 每条必须登记自己的 `business_id` 形态，门禁 5 据此校验。
- 超限失败 → publish 到 `dl.{consumerCode}.{event_type}` + 写 `{code}_dead_letter`；管理台按服务查看并人工重放（重放即原样重新入队，走同一幂等键）。
- `event_dedupe` 留存期取 **365 天 + 30 天余量**，每日清理更早的行。**留存的地板由「最宽的重新入队路径」决定，不是由热流 `max_age=90d` 决定**：死信重放（90 天内）、`HC_HOMEOS`/`HC_FINANCE`/`HC_CALENDAR` 三条源流（P1 期末态；`HC_CALENDAR` 随 S5 建，S1~S4 是两条）的 90 天窗口、**`HC_ARCHIVE` 的 `max_age=365d` 回放**三条里最长的是最后一条，取 90 天或 7 天都会让「回放一年前的归档事件」重复建业务对象（19.2#5 判「重放新增对象 = 0」）。**P1 去分区**：去重表改为普通表，`(event_type, business_id)` 做全局唯一索引，跨月插入不重复；清理口径改为**每日对账 + 滚动删除 >90 天记录**（`DELETE FROM {code}_event_dedupe WHERE created_at < now() - interval '90 days'`），不产生大事务 DELETE。

### 3.5 跨服务只读调用：声明式客户端（17.3、15.7）

```go
rclient.Call(ctx, rclient.Request{
    Target: "homeos", Path: "/api/homeos/members/snapshot",
    Timeout: 300*time.Millisecond, Retry: 1,
    Degrade: func(ctx context.Context, err error) any { return cachedOrPlaceholder },
})
```

三个字段任一为零值即编译期不过（struct 无默认）+ CI AST 检查；**Target 只能是服务名，路径只能是 GET**；一次请求内对同一 target 至多 1 跳，不得出现「A→B→C」链式同步读（17.3 禁止 fan-out >1）。P1 的合法调用对是**三条**：`finance → homeos` 与 `calendar → homeos`（两个面各取自己的成员/权限快照），加 **`homeos → calendar`**（首页 B 今日区的 `due_today` 当日切片，17.3 方式一、5.8 的 `GET /api/calendar/events`——首页 `due_today` 由底座经 `rclient` 只读调这条到期中心读口合成，PRD 5.8 把 `GET /api/calendar/events` 登记为**到期中心的唯一读口**、无第二条读口；超时 ≤300ms、至多重试 1 次、失败降级为「暂不可用」+ 最后投影值与时效戳，**不新增第二条读口**）。**后两条随 `svc-calendar` 在 S5 出生**（被调方与调用方都不存在时不构成合法口），**S1~S4 只有 `finance → homeos` 一条**。**`homeos → finance` 仍然禁止**——首页财务格走 `homeos_proj_finance` 投影，两面在这一点上不同构是定版结果（㉚ 只把日历的读口换成方式一，没有把财务也换成方式一，也没有给首页新增第二条同步依赖）。`Target` 写成未出生域时启动即失败（registry 里没有实建服务）。SDK 统一记录 `crossservice_call_seconds` 与降级次数，两侧都算（22.5）。

### 3.6 到期注册的跨服务形态（17.4、15.5 第 4 项）

不在请求链路上调日历面的写接口（`/api/calendar/*` 的写方法是客户端直连日历面用的，别的服务不在同步链路里调它），到期语义全部异步（**下面这条链路整条随 `svc-calendar` 在 S5 出生**——S1~S4 期间 `calendar` 流不存在，`finance.due.registered` 的 publish 在中间件层即报 `no stream`，§3.1）：

```
svc-finance:   写自己的实体（含 due_at）→ 同事务 outbox → finance.due.registered
svc-calendar:  消费并按 (source_system, source_id, kind) upsert calendar_due_registration
触发器:        svc-calendar 内 30s 扫描循环 + advisory lock（多实例安全）→ calendar.reminder.fired
投递:          svc-homeos 消费 calendar.reminder.fired → homeos_notification（站内信）→ adapter/push
               ——底座是唯一投递方，日历面不直连任何推送通道（5.5.8）
回写:          完成/删除 → calendar.todo.completed → svc-finance 消费并更新自己实体；失败进死信并计入对账
撤销:          业务对象软删 → finance.due.revoked → svc-calendar 置 expired
```

幂等键 `(source_system, source_id, kind)` 不变、发布方仍是来源面（17.4）；随 ㉚ 换宿主的只有三处：消费方由 `svc-homeos` 换成 `svc-calendar`、注册表由 `homeos_due_registration` 改名 **`calendar_due_registration`**（门禁 2 的跨 schema 白名单表名同步改，见 §10.2）、两条事件换前缀（`calendar.reminder.fired`、`calendar.todo.completed`，载荷字段 `source_system`/`source_id`/`kind`/触发时刻一个不加一个不减，见 §3.2）。

P1 落地的注册来源**五行**（都来自财务面）：`finance.Bill.due_at` 与 `finance.RecurringRule.next_at`（周期记账）——这两行是 **P1-M1 的定时两路**、经 `{code}.due.registered` 注册；`finance.Budget` 超支（事件驱动非定时、不经注册契约，按 19.2#2 的事件路与 4.9 判）、`finance.Goal.deadline`（M2）、`finance.Loan→RepaymentPlan.due_at`（M2）——见 17.4 的「随出生期落地」注（**㉚ 补入 `RecurringRule` 一行**：12.4 与 4.8 都写着周期记账到期经 17.4，原表漏登该行，注册表由 18 行变 19 行）。**不在 P1 建空注册方**：其余五面的注册行不预插，日历聚合与触发逻辑不为「将来的面」写分支。**日历面原生三对象不写进注册表**：`calendar_todo`/`calendar_reminder`/`calendar_event` 在同一面同一 schema 内即聚合本体，不再对自己的域发一遍注册事件（5.5.5）——否则到期中心会出现「注册行 + 原生行」双份，19.4#2 的「可见率 100%」分母无法唯一。

后果：注册到到期中心可见的延迟 = outbox 500ms + 消费 ≈ P95 5 秒内，验收按收敛时间测（19.1 最终一致性口径），不承诺「写完立刻上日历」。业务面**不得**插入 `calendar_event`/`calendar_todo`/`calendar_reminder`/`calendar_recurring_rule`/`calendar_due_registration`（本 schema 唯一可写服务是 `svc-calendar`，17.2），也**不得**自建定时器与推送链路（17.4）；**日历面自身同样不得直连任何推送通道**——它的下游只有 `calendar.reminder.fired` 这一个事实事件，站内信、push token、投递记录、回执全部留在底座（5.5.8、§七）。

---

## 四、鉴权：一份实现，当期三处调用（16.6）

### 4.1 token

JWT access（15 分钟）+ refresh（30 天轮换、落库可撤销），claims：

```
{ sub: account_id, fid: 当前会话家庭, role: owner|member|ward|guest, pver: 权限版本, jti: 会话 id }
```

`svc-homeos` 是唯一签发方与 RS256 私钥持有者；业务面服务只带公钥（启动时从 `/api/homeos/.well-known/jwks.json` 拉取并定时轮换）。P1-M1 阶段验证码用「固定测试验证码 + 站内信桩」，商用短信通道不做（15.6）。切换家庭 = 重签发一对 token，客户端重建上下文（15.5 第 2 项验收 ≤1 秒）。`pver` 是家庭级权限版本，任何权限、成员或**面配置**（18.8）变更即 +1——面开关因此复用权限那一条失效链路，不引入第二套广播。

### 4.2 SDK 与判定

`packages/authz` 提供 `Verify(token)` 与 `Can(ctx, scope, resource, action, obj)`，**S5 起三个服务**（`svc-homeos`、`svc-finance`、`svc-calendar`）在中间件的同一位置调用（23.2 第 5 条；调用方个数随 ㉚ 由两个变三个，**三个是 P1 期末态：S4 卡接入时盘上是 homeos / finance 两个，第三个接入点随 `svc-calendar` 在 S5 出生同批补上**，SDK 本身一份实现不改）：

- 判定顺序严格按 16.2：显式拒绝 > 角色默认矩阵 > 家庭级覆盖 > 对象级 ACL。16.3 默认矩阵内置在 `authz/policy.go`，**十行全部内置**（八个系统，HomeOS 占三行、日历面一行、其余系统各一行，含未出生系统行；**逐格断言 = 10 行 × 5 角色 = 50 格**，定版 ㉚ 修订 ㉓ 的九行 / 45 格，㉓ 原文只取代 ⑳ 的 40 格、保留不删；㉚ 的拆法是**把 ㉓ 新增的第九行「HomeOS · 时间与协作对象」按对象归属拆成两行**——「HomeOS · 协作对象」（留言板 / 投票 / 动态）与「日历」（日程 / 待办 / 提醒 / 周期提醒规则），**这不是新增判定，而是同一批格子按对象归属挪到两行：10 行里有 5 格是原那一行 5 格的逐格平移，语义与判据一字不改**，16.3 表末注、导航文档 §12.7 ㉚ 行）；**P1 只实现「角色默认矩阵 + `scope=module` + 字段级可见性」三层，家庭级覆盖与对象级 ACL 随 P6 规则引擎出生（定版 ㉖）**——快照接口的响应结构里为这两层留字段（`family_overrides`、`object_acls`）但 **P1 恒返回空数组、不产生数据、不出写接口**，`GET /api/homeos/permissions/snapshot?fid=` 带 `If-None-Match: pver`，绝大多数请求命中 304。**`scope = module` 这一层的判定输入是 `homeos_family_module` 的启用状态**（18.8）：快照接口把该集合并入权限快照一起下发，业务服务不另取第二处开关，`Family.feature_flags` 只承载非面开关；日历面在该集合里**恒为启用**（必选面，5.5.9），因此它的 `scope=module` 判定实际只会命中「启用」分支。
- 进程内缓存 TTL 60 秒（按 `(account_id, fid)`），并订阅 `homeos.permission.updated` 与 `homeos.family.module.updated` 立即失效——这是「≤1 秒生效」的服务端实现，不靠 TTL 碰运气。
- 字段级可见性（16.4）在 **DTO 序列化层**实现，不在 SQL 层过滤：L3 字段不进默认导出与埋点（21.2）只有在 DTO 层才拦得住。财务的「私密标记」行级可见性即在此层实现（16.3 财务隐私默认）。
- **降级**：SDK 取不到快照（homeos 不可达）→ **拒绝写、允许读已缓存**，并落审计（15.5 第 3 项）。

### 4.3 测试即门禁（附加门禁②）

`test/authz/matrix_test.go` 按 16.3 的 **十行 × 5 角色 = 50 格**逐格生成断言（㉚ 取代 ㉓ 的 45 格，用例数由表格行数派生，表格与用例同源，避免文档改了测试没改；**逐格断言的条数随行数 9→10 增加，判据只加不减**）。P1 的打法：

- **有真实服务的五行**（HomeOS 的三行 + 日历行 + 财务行；㉓ 后 HomeOS 不再是一行，㉚ 后日历自成一格一行）：这 5 行 × 5 角色 = **25 格**，每格打**在运服务各一次**（`svc-homeos`、`svc-finance`、`svc-calendar` 各自在自己入口验证同一条判定），**三个服务都在运时即 25 × 3 = 75 次服务级断言**（P1 期末态；**S4 卡时盘上是两个服务 = 25 × 2 = 50 次，第三个服务的 25 次随 `svc-calendar` 在 S5 出生期补齐复验——判据只加不减，任何一期都不许按更少的服务数收窄**），证明判定不是只有 homeos 实现；
- 其余五行（采购 / 饮食 / 出行 / 家人 / 成长）：按「**服务未出生**」判定——请求经 Nginx 返回**「即将上线」形态**（不是「未启用」：未启用专指本家庭 `homeos_family_module` 无行，两词不得混用，18.8 与 15.5 第 7 项的用词二分）、直连服务不存在、事件目录有条目但无生产者，三种形态都要断言（16.3 的「逐格断言与本表的关系」段）；
- 再加一组「**已出生但本家庭未启用**」用例（当期即把某家庭的财务面停用；**日历面不可停用，这一组对它没有停用路径**，改按下一行的必选面负样本断言）：`authz` 拒绝写、读接口按 module 拒绝、整条链路落审计，客户端导航与深链均无入口，未挂载面零请求零分包下载——这与「未出生」不是同一条路径（18.8、19.2#9）；
- 16.3 新增的**「HomeOS · 面配置」行**同样逐格断言：管理员可写、其余三角色只读渲染（无账号角色除外）、非管理员调用 `PUT family/modules` 100 次全拒并落审计；**该行的快照下发与失效链路要涉及的服务各打一次**（原两个服务各打：`svc-homeos`、`svc-finance`，**S5 起加 `svc-calendar` 成三个**——它消费 `homeos.family.module.updated` 才能刷新自己那一行的 module 判定，第三份打在 `svc-calendar` 出生期复验，§4.3 同条纪律）；
- **必选面负样本（5.5.9）**：管理员对日历面发起停用 `PUT family/modules` 100 次全拒并落审计（**其中 8 次即 19.2#9① 的「停用日历面被拒」负样本支，与「停用最后一个可选面被拒」8 次合计 16 次；两支分别出数，100 次是接口级重复请求压测、不替代这 16 次**），同时断言 `homeos_family_module` 里日历行恒为启用、`faces[]` 里日历格恒在；
- 越权 100 次全拒并落审计（样本量与判定见 19.3#8，19.2 附加门禁、15.5 第 3 项）。

**每个面在自己出生期末复验它那一行**，判据不因 P1 已过而豁免（12.9、19.2 出生期复验条）。

---

## 五、同步底座（15.5 第 5 项、3.4.4、22.6）

- **幂等**：所有写接口要求 `client_request_id`，`{code}_idempotency(key, family_id, request_hash, response_snapshot, created_at)` 唯一索引，重放返回首次响应而非重复执行。
- **乐观锁**：实体带 `version`；写请求携带读取时的 version，不匹配返回 **409 + 双版本 payload**（server/client 两份），绝不静默覆盖。冲突只在服务内判定，不存在跨服务锁。
- **变更日志**：业务写操作在事务内向 `{code}_change_log(lsn bigserial, family_id, entity, entity_id, op, version)` 追加，由 `packages/sync` 的 repo 基类完成，业务代码不记得写。**日历面三对象随 ㉚ 进入同一套机制**：`calendar_change_log` / `calendar_idempotency` / 乐观锁 409 双版本 / 导出范围与财务面同构（3.4.4），不写第二套同步实现。
- **delta**：`GET /api/{code}/sync/changes?family_id&since_lsn&limit`，**每服务一条游标**；P1 客户端持有三条游标（`homeos`、`finance`、`calendar`，**第三条随 `svc-calendar` 在 S5 出生；S1~S4 是两条**），storage 里按 `(family_id, service)` 存 LSN，切家庭与切服务互不干扰。游标结构按域表的 8 个 code 设计（§1.3；那是登记条目数，与「七类命名维度」不是同一个数），但**不为未出生服务预建桶**——桶在订阅到该域接口时才出现。
- **状态机单向**：`draft → pending → synced`，冲突分支 `conflict` 必须由用户处置（22.6）。
- 客户端载体：`storage` 中的 `pending_ops` 队列 + 自研重放器（网络恢复 / 前后台切换 / 定时三触发），按 key 串行去重；队列按服务分桶，某服务不可用时其它桶照常重放。

验收样本是 19.2#3 的双设备并发写 200 次，脚本以两个虚拟客户端身份跑，不依赖真机。

---

## 六、搜索与投影（15.5 第 7 项、17.3、18.6）

- **索引唯一持有方是 `svc-homeos`**：它订阅当期在运流的可索引对象事件，写 `homeos_search_index(ref_domain, ref_id, family_id, title, keywords tsvector, updated_at)`。客户端只调 `GET /api/homeos/search` 一个接口，**不在前端并行打多个服务拼装结果**（18.7 第 4、5 条）。
- 索引的 `ref_domain` 取值域按域表登记的 code（§1.3，P1 一次性登记 8 个条目；「七类命名维度」是命名条数、不是这个取值域的个数），**P1 实际有 `homeos`、`finance`、`calendar` 三个写入来源**（日历面随 ㉚ 出生，其三对象的事件进同一张 `homeos_search_index`；**`calendar` 那一路随 `svc-calendar` 在 S5 出生，S1~S4 索引只有 `homeos` 与 `finance` 两个写入来源**）；未出生面的查询返回空并提示「**即将上线**」而非报错（15.5 第 7 项验收；用词按 18.8 二分，未出生 ≠ 本家庭未启用）。结果分组按会话家庭的挂载集合过滤：**已出生但本家庭未启用的域不出现在分组里**（18.8），索引本身不因停用而清理——停用只关入口，不销毁可检索内容。
- 中文分词在应用层做 **bigram 切分**后写入 `simple` 配置的 tsvector，不引 `zhparser`（换分词器要改 Postgres 镜像，属 23.1 第 2 条评审项）。P1 的样本库 = 6 万流水 + **日历面三对象**（日程/待办/提醒；原以「HomeOS 三对象」名义构造的那批样本随 ㉚ 换归属，**样本条数与 22.1 的 ×1.5 下限一字不改**）+ 底座协作对象（留言板/投票/动态），判 P95 ≤500ms（端到端口径，含事件传播与投影写入）；**跨七面命中 ≥45/50 在 P6 判定**（19.2#4：面数由六面随 ㉚ 增为七面，命中阈值 45 与查询样本 50 不变，判据只加不减）。
- L3 内容不进索引（21.1）；L2 只索引脱敏字段——这两条写在**索引写入器的字段白名单**里，不是调用方自觉。
- **高频跨面渲染走本地投影**：`{code}_proj_{src}` 只允许本服务订阅器写入，可随时全量重建（重建脚本随 M1 交付，否则投影表会变成第二数据源）。P1 期末有四张（**含 `calendar` 的两张随 S5 出生，S1~S4 只有 `finance_proj_homeos` 与 `homeos_proj_finance` 两张在运**）：`finance_proj_homeos` 与 `calendar_proj_homeos`（两个面各自的成员/权限快照）、**`homeos_proj_finance`（按「自然月桶」聚合的收支与预算剩余快照，供 `home/summary` 的 `finance.headline` 与角标计算——首页格子不得为了渲染而去同步调 `svc-finance`）**、**`homeos_proj_calendar`（首页日历格的状态句、角标与降级「最后一次值 + `as_of`」载体，表名是 5.7 给的唯一合法形态）**：投影的桶粒度**固定为月**，季/年两档由服务端在月桶上求和后下发（⑬ 三档不能各存一套投影，否则三档之间必然出现两个数）；`period` 的季/年换算只发生在服务端查询层，客户端拿到的仍是单个已算好的值，其余投影表随 P2-P6 各面出生。财务那一格的投影同时是 `home/summary` 中该格 `headline` 与角标的唯一数据源；**日历格不同构**——当日 `due_today` 切片走 17.3 方式一只读调用 `svc-calendar`（5.8、§3.5），`homeos_proj_calendar` 只承载该调用失败时的最后值与时效戳，不是第二条数据源。**面被停用时投影照常更新**（统计口径不变，18.8；日历面是必选面、无停用路径，5.5.9），只是不再出现在 `faces[]` 里。
- 动态流与搜索共用 `{code}_change_log`，避免两套变更捕获。`finance.*` 与 `calendar.*` 事件写 `homeos_dynamic` 时标注 `on_behalf_of`（代管场景，15.5 第 2 项）。
- **每日对账**（19.7 的 P1 部分）：投影 vs 来源逐表比行数与抽样字段；孤儿逻辑引用（存了 id 但对方无此行）进对账报表并告警；`calendar_due_registration` 与财务来源对象的存在性也在这条对账里（注册行仍在、来源被硬删即孤儿；㉚ 换表名不换对账口径）。这是无物理外键的必然配套，不是可选优化，**每个出生期末为阻断项**。

---

## 七、通知下发（15.5 第 4 项，P1 只做到站内信可测）

链路：`svc-calendar` 扫描到点 → 发 `calendar.reminder.fired` → **`svc-homeos` 消费** → `homeos_notification`（站内信，落库即算投递成功）→ `packages/adapter/push` 异步投递。**日历面触达用户的唯一出口就是这一个事件**：日历面既不直连 `adapter/push`，也不写 `homeos_push_device`/`homeos_notification*`（5.5.8、§3.6）；推送通道选型与凭证（二十章 20.4）仍留在底座。

```
adapter/push/
├── provider.go        // interface: Send(ctx, Device, Msg) (Receipt, error)
├── stub.go            // P1-M1 默认实现：写日志 + 返回伪造回执，保证链路可测
└── (apns.go / vendor.go 在 P1 末附加门禁③凭证与 20.3 选型定版后新增，不改接口)
```

`homeos_push_device`（token 轮换需 upsert）、`homeos_notification_delivery`（每次投递一行，`dedupe_key` 唯一索引去重）、回执埋点按 20.5 字典；这三样连同 push 凭证都留在底座，**不随日历面迁出**（㉚ §4 归属拆法）。19.2#2 的服务端触发成功率 ≥99.9% **在 P1-M1 以站内信链路测得**，真实通道接入后同口径复测（19.2 P1 末附加门禁③）——这是门禁延后拍板的直接技术后果，不新增指标。**㉚ 之后 19.2#2 的两个口径归属拆开、判据一字不减**（5.5.8）：**触发口径**改为「`svc-calendar` 在 T±60s 内成功投递 `calendar.reminder.fired`（即 `svc-homeos` 成功消费）/ 应触发数」，在 `svc-calendar` 侧计量；**投递口径**仍归 `svc-homeos`（站内信落库 + `adapter/push` 回执），措辞与阈值不变。分母的构造由「`homeos` + `finance` 两域注册项」改为「**`calendar` 原生（日程/待办/提醒）+ `finance` 定时两路（账单、周期记账）**；`finance.Budget` 超支另走**事件路**」——**定时路 500 条一条不扣、≥99.9% 阈值不变、注册项条数不变，只换归属方；19.2#2 ㉚ 起在同一分母上另加 ≥1000 条合成 `finance.budget.exceeded` 事件走事件路（原 30 组在 0.1% 阈值上不可分辨，改为 ≥1000 条，分母只增不减）**。**各面不自建推送链路**（17.4、18.7 第 1 条），这条对日历面同样成立且更硬。

**通知分型与未读（3.6、12.3 第 ④ 项、18.1）**：`homeos_notification.type` 是受控枚举 `budget_alert` / `system` / `reminder`，新增取值须同时改事件目录与客户端文案库；**㉚ 只换分型的触发源、不换分型的归属**——`reminder` 一类的来源改为消费 `calendar.reminder.fired`，`budget_alert` 仍来自 `finance.budget.exceeded`，而这个枚举、未读计数与站内信落点全部留在底座（不随日历面迁出）。未读口径唯一为 `read_at IS NULL`，`GET /api/homeos/notifications` 按 `type` 返回分组与未读计数，**同一个计数同时供给顶栏消息入口的红点与 D 行「消息」的红点 + 计数**（首屏由 `home/summary.unread` 带出，客户端只存一份，导航文档 §3.4）。**L3 内容不进 `content`**（二十一章），分型只作用于渲染与路由，不作用于投递优先级。

---

## 八、双面落地拆法：财务面（M1 闭环域 + M2 深度域）与日历面（M1 随底座出生）

### 8.1 M1 段：单人闭环（15.5 第 10 项）

| 对象 | 表 | 关键实现点 |
|---|---|---|
| 账本（报表分组） | `finance_ledger` | 只是标签 + 成员范围 + 时间窗，**不是数据隔离域、不做账本级权限边界**（4.5.1、16.3） |
| 账户 | `finance_account` | 预置 + 自定义，余额为派生值：按 `amount_cents` 累计，**不做增量计数器**（并发写口径 19.3#5，另按 19.2#3 判双设备同步）；M1 建列 `is_archived`/`archived_at`，**归档前余额非 0 一律拒绝**（4.5.9、19.3#10），归档只移出默认账户位与汇总，历史流水与净资产照计 |
| 分类 | `finance_category` | `family_id` 可空 = 系统预置字典 + 家庭自定义；二级；图标集名、禁 emoji；跨面唯一费用字典（17.2）；M1 建列 `is_active`/`sort_order`/`version`，**停用不影响历史流水的显示与统计**，排序由记账选择器/报表图例/筛选 chips 三处共用（4.5.9、19.3#10） |
| 流水 | `finance_transaction` | `type` 收支转账三类同一张表，`transfer_group_id` 成对；分区与索引按 22.2 |
| 预算 | `finance_budget` + `finance_budget_period` | 月度 + 分类 + 固定/周期，超支走 `finance.budget.exceeded` 事件，**不自建定时器** |
| 账单 | `finance_bill` | 新建对象，`due_at` 经 17.4 注册；周期记账规则可作语义基线但账单是新建（4.5.5） |
| 票据附件 | **不建财务侧表**：附件的唯一归属是 HomeOS 的 `File`（`homeos_file`），`finance_transaction.receipt` 只存 `file_id` 引用（17.2、17.3 逻辑 id） | 单机磁盘 `./uploads/{family_id}/`，限额 ≤3 张/笔、≤5MB/张、jpg/png/webp（3.6 `File` 行）；**门禁 2 会拒绝 `finance_attachment` 这类未登记表名** |
| 回收站 | 软删列 + `deleted_at` 索引 | 30 天可恢复，**M1 必做**（4.5.1） |

接口按 3.7 与 4.8 的 M1 行交付（**两张表都是登记面**：实现用到而未登记即缺陷，先补表再写码），**导出必须三参数全生效**（`type/days/format`，4.5.3 硬约束，19.3#7 的回归防护项）。录入侧 AI：小票 OCR 与语音记账随本期交付，供应商按 20.3「随面定版」在 M1 开工前定版；识别失败必须 100% 回落手输，手工路径同批次（12.3 AI 交付期口径）。

**时间窗与统计口径全部在服务端（19.3#9、定版 ⑬）**：财务与首页的读接口统一接受 `period`，**三档编码 `YYYY-MM` / `YYYY-Qn` / `YYYY` 在 M1 全部实现**（家庭时区口径）；**格式非法或季号越界一律 400，服务端不回落为当前月**——静默改参会让客户端与服务端算出两个不同的「本期」。流水/报表/预算/首页格子四处读的是 shell 持有的同一个值，**首页也渲染时间窗条**（财务格状态句读它）。环比、累计余额、周期合计由预聚合表按 `period` 计算后下发，**环比的上期随粒度取上月/上季/上年，上期为空返回「无对比期」标识而不是 0 或除零**，客户端不得自行逐期累加（该条在 19.3#9 里以「前端二次累加检查为 0」判定）。**预算周期是对象、`period` 是查询参数，服务端不得把月度预算乘 3/12 折算成季/年预算**：只有月度预算时季/年档返回「未设该周期预算」的显式标识，首页格子与预算页一致呈现（⑬ 红线）。首页 `home/summary` 的 `finance.headline` 即取这套服务端结果，与 `finance/asset`（M2 净资产）同源（4.5.8、18.2）。

### 8.2 M2 段：深度域（15.5 第 11 项）

借还款与还款计划（`finance_loan` + `finance_repayment_plan`）、储蓄目标（`finance_goal`）、AA 分账与结算中心（`finance_split` + `finance_settlement`）、资产负债与净资产（`finance_liability` + 报表聚合）+ 多币种、发票与报销、报表增强、信用卡。四条实现纪律：

1. **AA 零误差**（19.3#6）：以「分」为整数单位，`Σ各参与人净额 = 0` 且 `Σ分摊 = 原始金额`，余数分配规则固定且可复现（按成员 id 排序取前 N 名各 +1 分），服务内用不变量断言而不是事后校对。
2. **净资产依赖负债对象先补齐**（4.5.8）：报表口径不得为净资产单独存一份金额副本。
3. **多币种**：汇率源属 M2 依赖（20.1），每日更新一次，记账按当日汇率、可手改，降级用上次有效汇率并标注更新时间。
4. **深度域的到期注册同批接通**：还款计划每期前 3 天、目标截止前 30 天（17.4），不在 M2 之后补。

### 8.3 财务面 demo 表：跨服务五步（附加门禁①）

M1 期间 `svc-finance` 交付一张 demo 表 + 五个接口，用途是**把跨服务链路走通**，不是业务功能。**㉚ 把附加门禁① 由「财务面跨服务五步全通」改为「财务面与日历面各走通五步」**：财务面沿用下面的 demo 表五步，日历面**不新建 demo 表**（17.2 里它已是登记面，再建 demo 就是第二个白名单），用自己的真实三对象走同构五步（见 §8.4）——判据是两支各绿，不是取其一：

| 步 | 动作 | 证明的跨服务能力 |
|---|---|---|
| 1 注册 | 本服务写 `finance_demo_item` 并发出 `finance.due.registered` | outbox → JetStream → **`svc-calendar` 消费** → 注册表（`calendar_due_registration`）幂等落库 |
| 2 写 | `POST /api/finance/demo-items`（带 `client_request_id`） | 本服务内幂等 + 乐观锁 + `version` |
| 3 只读 | **`svc-finance` 经 `rclient` 只读 `GET /api/homeos/members/snapshot`**（demo 项的「参与成员」字段渲染与该快照同源） | `rclient` 三要素、超时降级、鉴权 SDK 跨服务生效。**方向只能是 `finance → homeos`**：首页财务格的 `headline` 走 `homeos_proj_finance` 投影，**不允许 homeos 反向同步调 finance**（17.3、§6 投影条、18.2 首屏单请求） |
| 4 事件 | `finance.demo_item.created` 被 homeos 消费进索引与动态 | 目录校验、durable consumer、去重表 |
| 5 提醒 | `svc-calendar` 到点触发 `calendar.reminder.fired` → 底座投递 → 完成时 `calendar.todo.completed` 回写财务 | 事件驱动触发 + 异步回写 + 死信兜底（触发引擎在日历面、投递在底座，两步分属两个服务） |

**判据看端到端**：第 1 步到到期中心可见的收敛时间、第 3 步的降级正确性、第 5 步的回写到达都要有埋点数字，不是「接口返回 200」。五步全绿即 M1 附加门禁① 的第一支；**同一判据在 P2-P6 每个面出生期末复验一次**（19.2 出生期复验条、12.9），所以这套 demo 是模板而不是 P1 的临时把戏。

demo 表不进 17.2 权威表；`migrations/finance/` 里单独一个 demo 目录（`migrations/calendar/` **没有** demo 目录与白名单，日历面的五步只用真实对象，§8.4）。**删除时点 = S19（M2 收口）**，与 §十一 的 S14 建 / S19 删和导航文档 §十 同一条时间线；M2 期间 demo 表**不得被任何真实业务对象引用**，门禁 2 对其收紧白名单、每日对账不纳入该表，S19 首张任务卡即删除表与接口并在验收报告里留删除迁移号。

### 8.4 日历面落地拆法（S5 那张卡，4 周）

P1 双面例外的理由（㉚ §1、12.3）：日历是其余六面到期语义的依赖，不与底座同批出生就会让「到期」永远有两个主人。所以日历面**没有 demo 表、没有独立的闭环期**，整面随 M1 的 S5 一次出生；S5 因此由 2 周扩为 **4 周**（§十一），M1 其余五张卡时长不变。

| 部件 | 交付内容 | 本方案落点 |
|---|---|---|
| 服务出生 | 新增 `server/services/svc-calendar/`（cmd + internal + Dockerfile + `/healthz` + `/metrics`）、schema `calendar`、角色 `hc_calendar`、流 `HC_CALENDAR`、前缀 `/api/calendar/`、第三条迁移序列 `calendar/` | §1.1、§1.2、§1.3、§2.1、§2.2、§3.1 |
| 五表 | `calendar_event` / `calendar_todo` / `calendar_reminder` / `calendar_recurring_rule` / `calendar_due_registration`（5.7，序列第 0001 步之后即建） | §2.2、§2.3 |
| 三对象与注册表迁入 | 三对象与周期规则从底座口径整体改归 `svc-calendar`（17.2）；注册表历史上以 `homeos_due_registration` 名义写在底座口径里，**磁盘上从未建过**——S5 是首次落地，改名只改口径、不搬存量 | §2.3、§3.6 |
| 触发引擎 | 30s 扫描循环 + advisory lock（多实例安全）→ `calendar.reminder.fired`；全平台唯一定时器与唯一触发方 | §3.6、§七 |
| 十三条接口 | `/api/calendar/*` 共十三条（5.8 登记表的六个路径模板：`events`、`events/{id}`、`todos`、`todos/{id}`、`reminders`、`reminders/{id}`，方法集按 5.8 逐路径给定，展开后 = 迁入的 6 条路由 9 个方法+路径 + 补齐 5.2「三类对象的增删改查」与 5.4「三视图读写」新增的 4 个：`PUT`/`DELETE events/{id}`、`DELETE todos/{id}`、`DELETE reminders/{id}`；`POST events` 原为管理台补录口、本轮对用户侧放开、不计新增），首页 B 区 `due_today` 由底座只读调 `GET /api/calendar/events` 合成——**5.8 未登记任何第二条到期读口**，不新造接口 | §3.5、§九 |
| 9 页分包化 | 导航文档 §4.4「时间与到期中心」9 页整体迁出为 `pages/calendar`，路由改三段式 `calendar/calendar/index\|detail`、`calendar/todo/index\|create\|edit\|detail`、`calendar/reminder/index\|create\|edit`（**9 位 / 实建 9**） | 导航文档 §2.5、§4.4、§12.8 |
| 面目录 | 日历面以**必选面**进 `homeos_family_module`：恒启用、管理员不得停用、客户端不渲染它的开关（5.5.9、18.8） | §九、§4.3、导航文档 §3.4 |
| 客户端 | 面识别色与图标进 23.4 令牌与 18.8 清单；首页 B 今日区改调 `svc-calendar` 只读接口 | §九、§3.5 |

**归属拆法清单（不随日历面迁出的四族）**：通知投递与投递记录、`homeos_push_device` 与 push 凭证、未读计数与站内信分型、留言板 / 投票 / 动态与 `homeos_file`——这些继续归 `homeos`（㉚ §4）。这正是「厚面」与「把时间与协作整族搬走」的区别：**日历面只带走时间语义（三对象 + 注册表 + 周期规则 + 触发引擎），投递与协作全部留在底座**。

日历面那一支的跨服务五步（附加门禁① 的第二支，用真实对象、不建 demo 表）：

| 步 | 动作 | 证明的跨服务能力 |
|---|---|---|
| 1 注册（日历面为**消费方**） | 打 `finance.due.registered` 1000 条 ×3 重放 → `calendar_due_registration` 幂等落库 | 幂等键 `(source_system, source_id, kind)`、重复到期对象 = 0、漏登记 = 0（19.4#3） |
| 2 写 | `POST /api/calendar/todos`（带 `client_request_id`）建 `calendar_todo` | 本服务内幂等 + 乐观锁 + `version` |
| 3 只读 | `svc-calendar` 经 `rclient` 只读 `GET /api/homeos/members/snapshot`（负责人与可见范围渲染同源） | `rclient` 三要素、超时降级；方向是 `calendar → homeos`（§3.5） |
| 4 事件 | `calendar.todo.created` / `calendar.event.created` 被 `svc-homeos` 消费进索引与 `homeos_dynamic` | 目录校验、durable consumer、去重表 |
| 5 提醒 | 到点 `calendar.reminder.fired` → 底座投递 → 勾选完成 `calendar.todo.completed` 回写来源 | 触发与投递分属两个服务、异步回写、死信兜底 |

两支五步全绿才算 M1 附加门禁①（**判据由一支变两支，只加不减**）；`svc-calendar` 的 ≥24 小时长压窗口与第三个分母在 P1-M1 判定（19.2#13、§10.3）。

---

## 九、客户端 shell 与管理台（15.5 第 8 项、十八章）

> 页面清单、路由与分包、深链映射、步数预算与交互流的完整规格在 `docs/p1-page-structure-navigation.md`，本章只写服务端要为它兑现什么。

- **单一 base URL**：`https://{host}/api/{code}/*`，由 Nginx 前缀反代；客户端不持有各服务地址、不做服务发现（18.7 第 5 条）。
- **路由模型**：页面全部为三段式独立路由 `pages/{code}/{feature}/{action}`，**不注册原生 `tabBar`**（顶栏一行、底部五项、首页 C 区服务矩阵都由 shell 自定义组件渲染，一级页无栈互切、二级页压栈），破坏性动作走统一弹层不成页（18.7 第 6、7 条）。**顶栏中段不放面切换器（定版 ⑫，导航文档 §1.4）**，因此面入口全 App 只有首页矩阵一处，自面内切面恒定 3 步。`pages.json` 的 CI 三查见导航文档 2.4。
- **面集合是会话级变量，不是常量七**：本家庭挂载哪些面 = registry 面目录 ∩ `homeos_family_module` 已启用 ∩ 服务已出生（18.8；目录里当期七个业务面，日历面随 ㉚ 新增一行）。首页矩阵、「＋」目标对象、搜索分组、**到期中心注册项**、动态流筛选**五处共用同一个集合**（18.8 原六处，⑫ 移除顶栏 chip 后减为五处），深链落地与分包加载同样以它为准，客户端不硬编码、不补齐、不各自存一份——不同源即 19.2#9 判失败。**其上还叠一层角色裁剪**（服务端按当前成员的 `scope=module` 再裁一次，⑯）：owner 视角格数 = 已启用面数，且**日历格恒在**（必选面不可停用，5.5.9）；儿童与访客可以是「**除必选面外无可见面**」的 0 可选格态（㉚ 改词，原口径写作「0 格」；其 C 区是否整区不渲染仍按 19.2#12① 原判据复测，不因改词放宽）。
- **未挂载的面在客户端不存在**：不占首页格、不出现在其余四个消费位、不建空分包、不发任何请求。「描边格 / 置灰位 / 即将上线占位」这一类导航位**已从定版模型中废除**（原「首页六格恒在：1 实心 + 5 描边」口径由 18.8 取代）。全量面目录只在 `homeos/family/modules` 一页呈现（未出生面以只读「即将上线」出现、无开关），该页同时复用为新家庭创建的强制选面步骤。
- **首页家庭服务矩阵是变长宫格**：格数 = 已启用面数（1~2 面每行 2 格、3~4 → 2×2、5~6 → 2×3、≥7 每行 3 格换行）；**卡片尺寸固定、高度由内容决定，不设首屏占比阈值**（原「C 区占首屏 ≥45%」的分配口径 0929 作废，导航文档 §12.4 ⑨）。四区（家庭/今日/矩阵/动态）**与区之上的时间窗条**由 `GET /api/homeos/home/summary?period=` **一个请求**返回：`family.members[]` 带 **`relation`（家庭内称谓，⑭ 定版的唯一权威源，P1 起建档即写）**与 `member_count`（头像排「前 3 + 虚线圈 +N」的 N 之源，⑮）；`due_today` 的 `count` 为当日全量、`items[]` 恒 ≤3 按 `due_at` 升序（B 区时间轴只画这三张），**供数是 `svc-homeos` 在服务端经 `rclient` 只读调 `svc-calendar`（17.3 方式一、5.8，§3.5）而不是客户端发第二个请求**——首屏仍是一个 `home/summary`（18.2 首屏单请求结论不因 ㉚ 改变），这一跳超 300ms 或失败则该格降级为「暂不可用」+ 最后一次投影值与 `as_of`（载体 `homeos_proj_calendar`，§六）；`dynamics.items[]` 每条带 `code`/`actor_name`/`action`/`summary`/`at`，`code` 同时决定条目左侧的面图标（与矩阵同一套 registry 图标，**不建第二套缩写符号**；日历面随 ㉚ 在 registry 里带自己的识别色与图标，进 23.4 令牌与 18.8 面目录清单，主包与日历分包引用同一份值）；`faces[]` **只含已挂载面**、顺序按 registry 登记序，每项带 `availability`（`available`/`unavailable` 两值）、`headline`、`badge`、`as_of`，**必选面在 `faces[]` 里恒有一项且不带可停用标记**（5.5.9），同响应带 `unread`（见下两条）；**状态句与累计值一律由服务端算出**（前端二次累加在 19.3#9 里判 0；问候语的「早上/下午」时段划分属客户端按时区的格式化，是唯一例外），聚合走投影与只读接口，**不在前端并行打多个服务拼装**（18.2、18.7 第 4 条）。
- **分包按需加载（热插拔的客户端侧）**：`GET /api/homeos/app/bundles?codes=&installed=` 返回每面分包的 `version`/`url`/`sha256`/`size`/`min_compat`；首次进入该面或该面被启用时拉取并校验，**校验失败、超时、`min_compat` 不满足都不白屏**——该面转「暂不可用」，基座与其它面照常可用。缓存键 `(code, version)`，停用或版本更新即清理，超配额按 LRU 淘汰；首进到该面可用按 19.2#9② 判定（Wi-Fi P95 ≤3s、4G ≤8s）。P1 交付「清单契约 + 按需加载 + 校验回退的代码路径」，**真实远程下发链路自 P2 起启用**（18.8 阶段表）；iOS 侧只承载 wgt 资源包，**不使用动态代码执行**。
- **面配置变更的传播**：`PUT /api/homeos/family/modules`（`version` 乐观锁）→ 落审计 + `pver+1` + 发 `homeos.family.module.updated` → 各服务 module 判定缓存立即失效 → 客户端重建首页矩阵与其余四个消费位（19.2#9①：P95 ≤2s、P99 ≤5s）。**停用被服务端拒绝的两条路径**（18.8 第 4 条 + 5.5.9）：其一，**日历面是必选面，任何停用请求直接拒绝**（管理员也不例外，客户端不渲染它的开关）；其二，**仅剩最后一个可选面时停用被拒**——两条负样本各测（尝试停用日历面被拒 = 必选面支 **8 次**；停用最后一个可选面被拒 = 原边界支 **8 次**），**16 次负样本拆成两支各 8 次、总数不减**（19.2#9①，㉚）。因此 `faces[]` 恒 ≥1 项且日历项恒在。**停用只让入口消失，不删数据、不改统计口径**：该面对象继续参与归集、AA、净资产、备份与动态流，重新启用即刻可见（18.8）。
- **`period` 是 shell 级时间窗，三档在 M1 全开（定版 ⑬）**：编码 `YYYY-MM` / `YYYY-Qn` / `YYYY`，**非法值 400 且不回落为当前月**；首页今日区与财务格子、流水、报表、预算读同一值，四处一致是 19.3#9 的判定项；**环比的上期随粒度**（上月/上季/上年），缺上期返回「无对比期」而非 0 或除零。**服务端不得为凑出季/年档的数而折算预算**：只有月度预算时，季/年档的 `headline` 与预算接口返回「未设该周期预算」的显式标记，**乘 3 或乘 12 造出的假剩余在 19.3#9 里判失败**。
- **未读只有一个源**：顶栏消息入口的红点（只显点）与 D 行「消息」的红点 + 计数，读的都是 shell 里**一份 `unread` store**；服务端口径唯一为 `homeos_notification.read_at IS NULL` 按 `member_id` 聚合（3.6），**出口只有两个**——`home/summary` 的 `unread`（首屏带出，红点不额外发请求）与 `notifications` 及其 `read` 响应（导航文档 §3.4、18.1）。`unread` 由服务端按权限过滤后给出，客户端不自行判断某条通知是否可见；**分包自建未读计数即门禁 4 失败**。`unread`（未读消息）与 `faces[].badge`（待处理数）是两个语义，服务端不合并、客户端不互替。
- **主题是一份令牌，不是八套样式**（㉚：系统与面目录各多一行，「一套令牌 / 每面一套」的对比基数由七变八，令牌本身仍是一份）：亮/暗/跟随系统三态（18.9）由主包出**一份 CSS 变量**（两态各一套值），分包只引用变量；**服务端不参与主题判定**（设备与账号本地偏好，不入库、不同步、不进 3.4.4 导出范围）；门禁 4 新增「分包内出现硬编码色值即失败」的静态检查，两态逐屏对比度按 **19.2#10** 判（P1-M1 样本 = **主包实建 22 + 日历分包 9 + 财务分包 29 = 60 条实建路由** × 2 态 = **120 屏**；位账是 23 + 9 + 29 = 61 位，差额是主包 `vote/detail` 当期不建、由导航文档 §2.5 逐条重算并写明，⑰ 总数守恒不因 ㉚ 改变——变的只是分布：原「主包 31 + 财务 29」改为「主包 22 + 日历 9 + 财务 29」）。切换主题不改路由、不丢栈、不清 `period`。
- 分包严格按 `pages/{code}`，主包承载 shell 与 HomeOS 全部页面（导航、服务矩阵、＋、深链、状态组件、格式化、埋点 SDK、**分包加载器**，18.7 第 8 条）；**S5 起在盘的分包是两个：`pages/finance` 与 `pages/calendar`（㉚，`pages/calendar` 随 S5 日历面出生；S1~S4 只有一个分包 `pages/finance`）**；`pages.json` 由 CI 校验（门禁 4 的前端项），**分包集合必须等于「已出生域 − `homeos`」，多一个少一个都失败**——HomeOS 的页面在主包，`pages/homeos` 这个分包本就不该存在。
- 全局「＋」是**独立路由页** `pages/homeos/quick-add/index`（18.4），不是 action sheet：按当前会话家庭与权限过滤可建对象，无权限项**不显示**而非置灰；目标面手选只列本家庭已挂载面（含日历面），手选路径 ≤2 步以保「记账 ≤3 步」（4.9）；未提交草稿不落任何业务对象。**两段来源随 ㉚ 改词不改结构**（18.4、导航文档 §6.3）：业务面对象段与 `faces[]` 同源（现在含日历面），**底座自身可建对象段**改为「**日历面三类（待办 / 提醒 / 日程）+ 底座留言**」——即这三类的写接口现在是 `/api/calendar/*`（5.8）；面可见集为 0 的成员「＋」仍然可用这一结论**保持不变**（19.2#12③）。
- 深链 `homecube://{code}/{entity}/{id}`；**日历面三对象的 `code` 是 `calendar`**，落地页在日历分包（`calendar/todo/detail`、`calendar/reminder/index`、日程详情 `calendar/calendar/detail`），**不再是 `homeos/…` 形态**（㉚、导航文档 §2.5 深链表）；**本家庭未启用**的面落地为「未启用」态页 + 管理员侧「去开通」引导（跳 `homeos/family/modules`；日历面是必选面，不存在它的「未启用」落地态），**未出生**的面返回「即将上线」——两个词不得混用（18.8、15.5 第 7 项、㉙ 四态用词），两者都不下载分包；进入 L3 对象触发会话内二次确认（18.6、21.2）。
- 离线：核心写走 `pending_ops`；跨家庭切换、邀请、权限修改、AI 录入离线不可用（22.6），四类入口在 shell 层统一禁用，不各自判断。
- 某面服务不可用时该面入口与首页对应格显示「**暂不可用**」态、其余面照常可用；依赖它的投影或最后值显示最后一次值并标注时效 `as_of`（13.3）。用词按 ㉙ 四态：**暂不可用** = 已出生但此刻不可达，与「未启用（本家庭未挂载）」「即将上线（未出生）」「依赖未就绪」不混用；`svc-calendar` 故障正是这一态，今日区与日历分包入口同口径降级（§3.5、19.4#7）。
- i18n：文案不硬编码、语言资源按 `code` 分文件，**英文本地化本身在 P6 末统一交付**（13.4）。
- 图标统一图标集，`check-no-emoji` 进 CI（15.7）。
- **`svc-homeos` 要为首页四区兑现的契约**：`home/summary` 的 `faces[]` 由 registry 面目录 × `homeos_family_module` × 服务出生状态**三源在服务端一处合成，只返回已挂载面**；`availability` 两值判定也在服务端完成（客户端不得自行推断某面是否出生或是否被本家庭启用），`unavailable` 时带最后一次投影值与 `as_of`（13.3）。`faces[]` 与 `GET /api/homeos/family/modules` 的启用集合**必须逐项一致**，契约测试对两者做集合比对（导航文档 §3）。新增一面在服务端只改 registry 一行与该家庭的配置行，**不改这两个接口的形状**——这是 18.8「面集合可配置」在契约层的对应物。
- **`home/summary` 的其余三区同样是服务端契约**：`due_today` 由 **`svc-calendar` 的只读接口**供数（17.3 方式一、5.8），数据 = 到期中心注册项（17.4）∪ 日历面原生三对象（5.5.4、5.5.5）——**底座不再自己数到期，也不新增第二条读口**；`count` 是全量、`items[]` 恒给前 3 条（截断在服务端做，客户端不数；这里的「服务端」对 `due_today` 而言是 `svc-calendar`，`svc-homeos` 只透传并缓存最后值）；`dynamics.items[]` **在 SQL 层按当前 `member_id` 的角色与字段级权限裁剪后再取前 20**（16.4、导航文档 §7），客户端不做二次过滤；`family.members[]` 的 `relation` 权威源在 `homeos_member`（17.2、⑭），**`svc-kin` 不建关系表**，P4 关系图与 P5 对象选择器按 17.3 走只读接口消费同一字段。矩阵格的「重试」**不是一个服务端契约**：它是重发一次 `home/summary`（必要时同时重走按需加载），不新增接口、不进幂等键与离线队列（18.2）。
- **设备锁 PIN（3.4.8、12.3 第 ⑪ 项）**：PIN 散列与比对全部在客户端本地，**服务端不存 PIN、不下发任何可离线校验的凭据**；服务端只提供「忘记口令 → 手机号验证码重验 → 允许重设 PIN」这条链路（复用 3.1 身份域的验证码接口，不新增认证因子）。L3 字段的解密仍按二十一章在服务端按权限判定，PIN 只是本地解锁载体，**不构成新的授权层**。
- **登录后静默检查更新（定版 ㉕ 收紧时机）**：`GET /api/homeos/app/version` 只读、可被 Nginx 侧缓存，**但它是常规鉴权接口——走 `svc-homeos` 自己的中间件、必须带 token，23.2 第 8 条之下不存在任何免鉴权读口**（本节此前写的「唯一免鉴权例外」是对架构红线的擅自豁免，已撤销）；响应不含任何家庭数据与 `family_id`，这一性质是它的**结果**而不是给它开后门**的理由**。触发时机 = **登录换到 token 之后**，未登录冷启动不检查；首屏仍只有 `home/summary` 一个业务请求，版本检查不占用它、也不新增第二个首屏请求。返回当前版本 + 更新说明 + `min_compat`，客户端静默比对、可跳过更新引导，**检查失败不阻断首屏**（3.4.8、13.4）。P1 期间该接口只需返回静态配置，出包与灰度通道属 P1 末附加门禁③。

`admin/`（Vite + React）P1 范围五项：家庭与成员查询、权限矩阵断言结果、**按服务**的死信查看/重放、审计与最小指标看板、对账报表入口。不做业务功能，不做用户侧配置。

---

## 十、部署、备份与 CI（15.5 第 1、9、12 项，23.4、22.3、23.5）

### 10.1 Compose 与一条命令

`deploy/` 内 compose：`nginx` + `nats`(JetStream) + `postgres16` + `svc-homeos` + `svc-finance` + **`svc-calendar`（这一行随 S5 出生加入 compose）** + `web`/`admin` 静态容器 + 一次性 `migrate` 初始化容器（按当期序列逐服务重放后退出：S1~S4 两条、S5 起三条）。常驻服务容器 **S1~S4 为 2 个、S5 起 3 个**；compose 全栈 **S1~S4 为 7 个容器、P1 期末（S5 起）8 个**（§1.1 计数基线）。

```
make up          # 干净机器 → 可用环境：建当期 schema 与账号（S1~S4 两个、S5 起三个）→ 迁移（S1~S4 两条序列、S5 起三条）→ 种子 → 当期在运服务健康检查（≤30 分钟，附加门禁③；**容器数按当期实际测：S1 判 7 个、P1 期末判 8 个，时限 30 分钟不随容器数增减放宽**）
make seed-perf   # 按 22.1 上限 ×1.5 构造压测样本（6 万流水 + **日历面三对象** + 底座协作对象；原以「HomeOS 三对象」名义构造的样本换归属、条数不变，㉚），CI 缓存
make soak        # 19.2#13 的长压窗口：在同构压测实例上起持续请求流 ≥24 小时（**P1 期末三个服务三个窗口**，㉚ 由两个增为三个；**三个窗口都在 S14/M1 收口判，S5 之前没有第三个分母可判，不许提前给未出生的服务起窗口**，多出的墙钟落在 S14 缓冲里），用**专用压测家庭**（独立 family_id，跑完整库丢弃，不并入 19.2#4/19.3#2 的 6 万条样本库、不进每日对账与动态流口径），报告落 deploy/data/soak/report.json
make backup      # 当期各 schema 一份 pg_dump -Fd + NATS 流快照 + uploads tar → 加密落 deploy/data/backups
make restore     # 恢复到指定备份点：整集群 → 逐服务重放迁移 → 起当期服务与消费组（演练入口）
make rollback    # 只切回上一版本镜像 tag，校验备份完整性并打印，不自动动数据
```

- RPO ≤15 分钟靠 **WAL 连续归档**（`archive_command` 每分钟搬运一段），全量每日 1 次 + 每月留存 12 份（22.3）。
- **不允许只恢复单个 schema**：跨面逻辑引用会立刻失配（22.3 恢复顺序行）。
- M1 必须完成**首次真实恢复演练**（含全栈 Compose 重建）并记录耗时——15.5 第 9 项的门禁；**此后每季度一次**（13.3 数据备份行），演练记录经 `GET /api/homeos/data/backup-status` 可查、经 `POST /api/homeos/data/restore` 触发（PRD 3.7 的管理台口）。**只做首次不做季度 = 13.3 的「每季度 1 次」是一句没有载体的话**。
- 回滚刻意保持薄：数据库向后兼容由迁移纪律保证，不执行 down 迁移；**部署包上传与目标机执行由人手动完成**，脚本不做远程编排。
- 健康检查：每服务 `/healthz` 报告「本 schema 可连 + JetStream 已连」，`/metrics` 带常量标签 `code`（23.2 第 10 条）；**S5 起三个服务**（homeos / finance / calendar）都要可拉，**S1~S4 为两个**（homeos / finance——`svc-calendar` 还没出生，不存在可拉的第三个口，S1 卡不判它），每新增一个服务即在期末复验这一条（15.5 第 9 项）。S1 落地的形态（在 `packages/obs`，只登记代码里已有的；`svc-calendar` 的 healthz 随 S5 与**当时在运的两个 cmd** 同源同构，S5 起是三个 cmd，§1.2）：
  - 两项探针各是一次真往返。**schema 项** = 连得上 + `current_schema()` 等于本域 schema（证明 §2.1 的 `search_path` 真生效、该账号确实看得见自己的 schema）+ `information_schema.tables` 里至少有一张本域前缀的表（空 schema 意味着序列没重放或 §2.1 的 GRANT 没落）。**JetStream 项** = 连接仍在 + 向服务端要一次账户级 JetStream 状态（这比 ping 多证明「服务端开了 JetStream 且本账号可用」）。两项都**不要求本域的流已存在**——流的创建是 S2 的载体（§3.1 的「建」是流规划，`deploy/env.local.example` 也写明流的创建与 subject 授权不在 env 侧），要求一条还没人建的流会让正确的检出永远回 503。
  - `/healthz` **两处都挂**：本域前缀下的 `/api/{code}/healthz` 与文档字面写的根 `/healthz`。原因是 23.2 第 10 条与本节末条都写裸 `/healthz`，而 23.2 第 8 条要求路由分域并禁止根路径业务接口——两种读法同时兑现，不在实现里择一。`/metrics` 只挂根路径这一处：它同样被 23.2 第 10 条写成裸名，且不是业务路由。
  - 失败语义：全绿 → 200；任一项失败 → 503 并逐项给出失败原因。不存在「降级仍回 200」——那会让 compose 健康检查与 15.5 第 9 项的「可被拉取」跟健康态无法区分；单项探针超时即计为失败。
  - 启动时同一批检查先跑一轮（preflight），未通过即进程退出、码非零：§2.1 的「账号即边界」与本节的健康门都预设一个没带病起来的进程，不留一个永远回 503 的服务。

### 10.2 CI 五道门禁（23.5）

| # | 门禁 | 实现 | 失败即 |
|---|---|---|---|
| 1 | 迁移 | 逐服务在空库重放自己那条序列 + 版本表与代码声明一致 + 迁移文件只触碰本 schema | 阻断合并 |
| 2 | 数据归属 | **业务表**名与 `TableName()` 比对 17.2 白名单；表前缀与所在服务一致；**未出生域的表/迁移目录/分包出现即失败**；**底座运行表族按命名后缀白名单放行、不进 17.2**（定版 ㉔）：`_outbox` / `_event_dedupe` / `_dead_letter` / `_change_log` / `_idempotency` / `_proj_*` / `homeos_search_index` / `homeos_audit_log` / **`calendar_due_registration`** / `homeos_event_archive`，命中后缀只校验「前缀 = 所在服务」；**到期注册表随 ㉚ 改名换宿主**：它现在是 `svc-calendar` 在 schema `calendar` 里、且在 17.2 登记的业务表，本条列它只为让旧名 `homeos_due_registration` 出现在任何 schema 即失败（防口径回退），不是给它开底座豁免；`finance_demo_item` 走单独收紧白名单（§8.3） | 阻断合并 |
| 3 | 验收指标 | 19.2 与 19.3 各条自动化用例（并发写 200、重放 1000×3、越权 100 次、搜索样本 P95、收敛时间、AA 两项等式、**面开通/停用生效时限与「未挂载面零请求零下载」抓包（19.2#9）**、**非功能基线五项 + 40 条主链路冒烟 ×3（19.2#11）**、**角色可见集与称谓回落链（19.2#12）**、**按服务分别计量的可用性（19.2#13：每服务一次 ≥24 小时、净样本 ≥10 万次的长压判 SLO，每服务各 20 次故障注入判行为；㉗ 新增、人类定版 P1 即判；㉚ 后 P1 期末已出生三个服务，故分母是三个、故障注入共 3 × 20 = 60 次；三个分母各算各的、都在 S14/M1 收口出数，`svc-calendar` 那个分母随 S5 出生才存在，S5 之前不判第三条）**、**设备锁与 L3 解锁链路抽检（19.2#14，㉗ 新增）**）产出报告 | 阻断灰度/发布。**19.2#13 的长压不是 CI 同步作业**：门禁 3 校验的是归档件 `deploy/data/soak/report.json` 存在、**三个已出生服务（homeos / finance / calendar）各自 ≥99.9% 且净样本各 ≥10 万次**（㉚：两个分母 → 三个分母，各算各的、不许合并平均）；≥24 小时的连续窗口在 CI 之外跑（§十三），缺该件即判「未测」、按不通过处理 |
| 4 | 分域与鉴权 | 路由前缀 AST、handler 内裸 SQL 与自写判定分支禁用（必须经 authz SDK）、业务表 `version`+软删、跨 schema SQL / 跨服务 import / 请求链路同步写调用禁用、`rclient` 三要素齐备、消费者幂等键存在、`pages.json` 分包命名与三查（无 `tabBar`／**分包集合 = 已出生域 − `homeos`**／路由全三段式）、**七条前端同源检查**：四条结构同源（分包内无切换家庭调用、无硬编码面清单、搜索只调 `/api/homeos/search`、无自造时间窗状态，18.8、4.5.3）+ 三条 UI 同源（分包内无硬编码色值、无自建未读计数或消息入口、**无第二处面入口或自建面切换器**，18.9、18.1、⑫）、`check-no-emoji` | 阻断合并 |
| 5 | 契约 | `contracts/openapi/*` 与 `contracts/events/*` 向后兼容 diff；发布未登记事件、引用不存在的服务、破坏性变更未升 version | 阻断合并 |

**P1 期末（S5 起）**有两个业务服务（财务、日历）加一个底座服务，共三个服务——**S1~S4 只有一个业务服务（财务）加底座，共两个服务**；但**五道门禁从 M1 起全部接驳且规则按域表的八个 code 生效**（㉚ 多登记一个 code，门禁清单一字不改、覆盖范围自然扩大）：P2-P6 每新增一个服务，门禁清单不变、覆盖范围继续自然扩大（12.9 的「服务随面出生的重复验证风险」的处置方式）。按服务并行：某服务的 lint/test/build 只在该服务目录变更时跑。

### 10.3 可观测与合规素材（15.5 第 9 项）

- 指标最小集（全部带 `family_id` 与 `code`，当期各服务分别出图）：接口 P95/P99、错误率、`/healthz` 存活、outbox 积压、死信数、事件端到端收敛时间、跨服务调用耗时与降级次数、同步冲突数、投递回执率、依赖调用量（未定版项为 0，OCR/ASR 有真实调用量）。告警阈值写进 `deploy/alerts.yml`（22.5）。S1 落地的形态（代码在 `packages/obs`）：
  - `code` 是**构造期绑定的常量标签**：注册器在装配服务时用 `prometheus.WrapRegistererWith` 把 `code` 绑上去，值取自 registry 行——与本服务的路由前缀、schema 同源，不可能各说一遍。因此本包注册的每一条序列都自带 `code`，调用点既不需要传也**没法漏**，也拿不出一条无标签的序列。
  - 每服务一个独立指标 Registry，`/metrics` 只吐自己的数字（23.2 第 10 条「每服务可独立运维」、22.5「每服务分别出图」）。
  - 上面那一条清单里，**当期只有三项有真数据**：接口延迟直方图（P95/P99 由查询层对桶算）、错误率（按状态码打标签的计数器，5xx 占比即错误率）、以及进程收集器。`route` 标签取的是匹配到的路由模板而不是 URL，未被任何路由接受的请求落一个固定占位值——用 URL 会让标签基数失控。其余各项（outbox 积压、死信数、端到端收敛、跨服务调用耗时与降级、同步冲突、投递回执率、依赖调用量）**今天不注册 collector**：它们的数值来自 S2/S5/S6/S13，而一个没人测的零值 gauge 在仪表盘上会被当成数据。
  - **`family_id` 维度当期没有出**。本条开头那句「全部指标带 `family_id` 与 `code`」里的 `code` 已由上面的常量标签兑现；`family_id` 的唯一权威来源是 S4 的 authz 中间件从 JWT claim 里取（15.5 第 3 项），当期那个中间件还不存在，用一个空值去注册一条任何代码路径都填不满的维度属假维度，因此按「未实现」登记在这里，而不是写成已满足。
  - 本包的职责里**没有审计上报**（见 §1.2 的落盘补充）：本节下一条的九类落库路径整条待 S2 的 outbox 与 S4/S5 的审计事件，代码里没有占位函数。
- 审计事件按 22.5 的**九类**落 `homeos_audit_log`（P1 可测八类，「规则启停」随 P6 规则引擎出生），各服务在**自己**的流上发 `{code}.audit.recorded`（P1 = `homeos.audit.recorded` + `finance.audit.recorded` + **`calendar.audit.recorded`**，三条流三条审计事件，与各自业务写同一 outbox 事务落盘），`svc-homeos` 消费后统一落 `homeos_audit_log`——因此 homeos 不可达时审计不丢，只延后收敛；连续 5 次越权通知管理员。**审计落点仍是底座这一张表**：日历面只发自己域的事件，不自建审计表（㉚ §4 归属拆法）。
- **合规素材底稿四项**：L2/L3 字段清单（**由 `contracts/data-levels.yaml` 生成，该文件是 21.1 的机器可读形态、由 21.1 派生而非手写，定版 ㉔**；19.2#7 的逐字段抽检与门禁共用这一份源）、埋点字典（20.5）、保留期表（22.4）、第三方 SDK 与数据出境清单（P1 含 OCR/ASR 两家，其余为空表 + `adapter/` 接口清单）。P1 末附加门禁①只判文本定版，不判素材收集（12.9）。

---

## 十一、P1 排期与拆卡

**M1 = 28-33 周（14-16 个 Sprint）**，由三段相加：底座段 **S1-S6 = 14 周**在前（S1、S2、S3、S4、S6 各 2 周 + **S5 4 周**，2+2+2+2+4+2 = 14；S5 的 2→4 周是 ㉚ 对 ㉑ 排期的唯一改动，其余五张卡时长不变），财务闭环段 **S7-S13 = 13-16 周**（S7-S8 / S9-S10 / S11-S12 各 4 周、S13 1-4 周——S13 的浮动来自 20.3 的 OCR/ASR 供应商定版时限），收口与缓冲 **S14 = 1-3 周**。**下界 14+13+1 = 28，上界 14+16+3 = 33**，与 12.3 的 P1-M1 = 28-33 周同源（㉑ 原写「12 周底座段、M1 = 26-31 周」，原文不删、由 ㉚ 修订，见导航文档 §12.7 的 ㉑ 行标注）。两段同期串行但**底座段不含业务功能、财务段不改底座主干**——**日历面是这个「不含业务功能」的例外**：它是双面例外（㉚ §8「每期交付当期出生面的整面；P1 例外为双面」）里的第二个出生面，服务端与五表在 S5 卡出生、9 页客户端在 `pages/calendar` 分包（§8.4、导航文档 §4.4），因此底座段多出的 2 周全部花在日历面，不是花在财务功能上。
**M2 = 10-12 周（S15-S19）**：S15-S16 = 4 周、S17-S18 = 5-6 周、S19 = 1-2 周缓冲；下界 4+5+1 = 10，上界 4+6+2 = 12。人类验收切片上限 = **当期 1 面 × 每面 2 个功能域**（12.7 第 4 条），超出即顺延，不许压缩验收；**P1 双面例外按面各自起算**——财务面（M1 闭环域 + M2 深度域）与日历面（M1 的 S5 一次出生）各按「每面 2 个功能域」上限算，单面判据与条数不因此稀释（㉚ 只加一支面，不摊薄任何一支）。**P1 合计 38-45 周（28-33 + 10-12）；六期净工作量 150-183 周（原 148-181，+2 周来自 S5 扩卡）；期末验收与发布收口约 8 周不变；墙钟 158-191 周（原 156-189，㉚ §8），36-44 个月的区间表述不变。**

| Sprint | 目标 | 关键卡 | 出口判据 |
|---|---|---|---|
| S1（2 周） | 骨架可跑 | registry **八个 code** 一次性登记（**登记的是七类命名域，不是服务出生**，命名仍是七类维度，§1.3）+ 实建 **2 服务**（homeos / finance；**`svc-calendar` 及其 schema、账号、序列、流、分包一律随 S5 出生，S1 不建空服务、不建空 schema、不建空分包**，12.7 第 1 条、15.5）+ Nginx 前缀反代（**未出生的 6 个前缀**返回「即将上线」——含 `/api/calendar/`，用词按 18.8 二分；S5 起该前缀转真实反代、未出生前缀减为 5 个）+ **2 schema / 2 账号**（`homeos`+`finance`、`hc_homeos`+`hc_finance`；S5 起 3/3）+ **两条迁移序列**与工具链（S5 起三条，§2.2）+ **底座运行表族按命名后缀白名单过门禁 2（㉔：`_outbox`/`_event_dedupe`/`_dead_letter`/`_change_log`/`_idempotency`/`_proj_*` 与 `homeos_search_index`/`homeos_audit_log`/**`calendar_due_registration`**/`homeos_event_archive`，只校验前缀 = 所在服务、不校验 17.2；注册表的**命名条目**随 ㉚ 用新名登记，旧名 `homeos_due_registration` 出现即失败——登记命名不建表，该表随 S5 出生）** + Compose（**当期 7 个容器**：nginx + 2 服务 + postgres16 + nats + web + admin；P1 期末即 S5 起才是 8 个，§1.1 计数基线）+ 门禁 1/2/4 接驳 + Jarvis Workbench 建 HomeCube 项目与任务模板 | `make up` 干净机器 ≤30 分钟（附加门禁③，**容器数按当期实际测：S1 判 7 个、S5 出生起判 8 个、P1 期末复验 8 个，30 分钟时限不因当期少一个容器而放宽**）；**两个服务任一单独重启不影响另一个**（「三个服务任一单独重启不影响另两个」随 `svc-calendar` 在 **S5 卡**判、P1 期末复验，S1 无第三个服务可判）；门禁 1/2/4 全绿 |
| S2（2 周） | 总线与契约 | **两条源流**（`HC_HOMEOS`/`HC_FINANCE`，§3.1；**`HC_CALENDAR` 随 `svc-calendar` 在 S5 建**——流随服务出生（§十五 G），S2 不建未出生面的流，§3.1 末条「未出生面的流不存在」；P1 期末态才是三条源流）+ 归档流 + 死信流 + outbox 投递器 + durable consumer 框架 + 去重表 + **八个 code 的事件目录**（含 11.3 事件清单与启用期字段；`calendar.reminder.fired` 与 `calendar.todo.completed` 随 ㉚ 换宿主登记，**登记目录 ≠ 创建流**，§3.2）+ 契约 diff + 门禁 5 接驳 | 重放 1000 事件 ×3 不产生重复对象；服务停 5 分钟事件不丢；未登记事件 CI 拒绝；**未出生六个域的 publish 因 `no stream` 失败（S2 时点 = `calendar` + 其余五面；S5 建 `HC_CALENDAR` 后减为五个，§3.1）** |
| S3（2 周） | 身份 | 账号/验证码（测试码 + 站内信桩）、家庭、成员（**`homeos_member.relation` 家庭内称谓：建档即可写、`PUT /members` 可改，权威归属 `homeos`，⑭**）、邀请码/链接/二维码三态、多家庭列表与切换（重签发 token）、JWKS 分发 + **`homeos_family_module` 表与 `GET/PUT /api/homeos/family/modules`（乐观锁 + 审计 + `pver+1` + `homeos.family.module.updated`）** + **强制选面的服务端约束**：`POST /families` 后未落任何 `FamilyModule` 行即视为半成品家庭，鉴权侧按 0 可见面处理、写接口一律拒绝；**日历面行由建家庭的服务端自动落且恒为启用——它是必选面（5.5.9），不进强制选面的可选项、不接受任何把它置为非启用的写入（㉚）**；仅剩最后一个**可选**面时停用被拒（18.8 的**服务端**兜底，前端引导态只是它的表现） | 3.4.1 场景走查；切换后上下文重建 ≤1 秒；**面开通/停用 P95 ≤2 秒生效（19.2#9①）**；非管理员调用写接口 100% 被拒并落审计；**门禁 2 断言 `kin` schema 内无 `kin_relation` 一类的第二称谓源（⑭）** |
| S4（2 周） | 鉴权 SDK | `Permission` 表 + `authz/policy.go` 内置**十行**矩阵（八个系统、HomeOS 占三行 + 日历面一行，㉓ 的九行由 ㉚ 修订为十行：**「时间与协作对象」行按对象归属拆成「协作对象」与「日历」两行，日历行 5 格是原行 5 格的逐格平移、语义与判据不变**，§4.2） + **在运服务中间件接入**（S4 时点 = homeos / finance 两个，同一位置调用；**`svc-calendar` 的接入点随 S5 出生同批补上，S5 起三个**，§4.2） + **家庭级覆盖与对象级 ACL 两层在 P1 只留空结构、不出写口（㉖）** + `pver` 与 `permission.updated`、`homeos.family.module.updated` 失效（**在运的服务都要订阅面配置事件**：S4 两个、S5 起三个，§4.3） + 字段级可见性落在 DTO 层 + 降级策略 + **50 格逐格断言用例（10 行 × 5 角色 = 50；有服务的 25 格 × **当期在运的两个服务** = 50 次执行，**第三服务的 25 次随 `svc-calendar` 在 S5 出生期补齐，S4 卡不判、也不许删这一份——判据只加不减**，§4.3）** | **附加门禁②通过（判据由 45 格升 50 格，只加不减）**；权限变更 ≤1 秒跨服务生效；越权 100 次全拒并落审计；**尝试停用日历面 100 次全拒（必选面负样本，5.5.9；其中 8 次计 19.2#9① 的负样本支，与「停用最后一个可选面被拒」8 次合计 16 次、两支分列）** |
| S5（**4 周**，㉚ 由 2 周扩卡：本卡 = **日历面出生** + 底座通知投递与首页供数） | **日历面出生**与到期、通知 | **A. 日历面出生（扩出来的 2 周花在这里，§8.4）**：`svc-calendar` 出生（第三个 cmd/internal/Dockerfile/`healthz`，三份同源）+ schema `calendar` + `hc_calendar` + 第三条序列 + 流 `HC_CALENDAR` + **`calendar_*` 五表**（5.7：`calendar_event`/`calendar_todo`/`calendar_reminder`/`calendar_recurring_rule`/`calendar_due_registration`）+ **三对象与到期注册表自底座口径迁出**（17.2；S5 是首次落地——历史名 `homeos_due_registration` 只是口径改名，**不搬任何存量**）+ 17.4 的注册契约（`{code}.due.registered`/`{code}.due.revoked` 发布方仍是来源面、**消费方改为 `svc-calendar`**；触发与回写事件 `calendar.reminder.fired`/`calendar.todo.completed`）+ **30s 扫描 + advisory lock 的触发引擎归 `svc-calendar`**（全平台唯一定时器）+ `/api/calendar/*` 十三条接口（5.8：迁入的 6 条路由展开 9 个 + 补齐 5.2 增删改查与软删新增的 4 个）+ **9 页客户端分包化**（`pages/calendar`，路由 `calendar/calendar/index\|detail`、`calendar/todo/index\|create\|edit\|detail`、`calendar/reminder/index\|create\|edit`，导航文档 §4.4）+ **面目录必选面接通**（日历行进 `homeos_family_module` 恒启用，5.5.9）。**B. 底座保留段（A 扩卡不动它，一条不迁）**：站内信（**含 `type` 三枚举 `budget_alert`/`system`/`reminder` 与未读计数接口**，分型与未读都归底座，§七）+ push adapter/stub + `dedupe_key` 与回执埋点 + **`home/summary` 的 `faces[]` 合成（registry 面目录 × `homeos_family_module` × 服务出生状态 ×**当前成员角色的 `scope=module` 判定**，四层同一处算完，只返回该成员可见的那一份；`family/modules` 的 `visible` 字段用同一个判定，**客户端不得二次裁剪**，⑯）** + **L0 家庭对象两张表：`homeos_vote`（只有对象与接口，创建入口随饮食 P3/家人 P4）与 `homeos_board_message`（3.4.3 留言板，评论写入经事件、不由业务面直写；留言板/投票/动态两张表留在底座，㉚ §4）** + **`home/summary` 的 `unread`（服务端按 `member_id` 聚合 `read_at IS NULL`，与 `notifications` 同一口径，18.1）** + **`home/summary` 的 A/B/D 三区供数：`family.members[]`（含 `relation` 与 `member_count`，⑭⑮）、`due_today`（`count` 全量、`items[]` 服务端截前 3；**B 区供数改为底座经 `rclient` 只读调 `svc-calendar`（17.3 方式一、5.8），底座不再自己数到期**）、`dynamics.items[]`（`code`/`actor_name`/`action`/`summary`/`at`，**按当前成员角色与字段级权限在 SQL 层裁剪后取前 20**）** | **出生完整性（本卡把 S1 的「两个服务」升为「三个服务」）**：三个服务任一单独重启不影响另两个（**这条判据自 S1 卡移入本卡，S1 只判两个服务之间互不影响**）；`HC_CALENDAR` 建成，**源流自此为三条、未出生域由六个减为五个**；`/healthz` 与 `/metrics` 三个 cmd 各可拉（§10.1）；§4.3 的 **25 × 3 = 75 次服务级断言在本卡补齐**（S4 只跑了 50 次那两份）；**财务对象可注册可触发；日历原生三对象由本面直接持有并可触发（不经注册契约，17.4 末注）**；**注册收敛**（财务写成功 → **`svc-calendar` 侧该注册项可扫描**）P95 ≤5s、P99 ≤60s，样本 ≥100 条注册；**回写收敛**（到期触发 → 源对象状态回写可见，经 `calendar.todo.completed`）P95 ≤5s，样本取 19.2#2 的 500 条定时提醒子集（19.1 最终一致性口径）；19.2#2 ≥99.9%（站内信口径；**触发口径在 `svc-calendar` 侧计量、投递口径仍归底座，两支都要出数**，§七）；回写失败进死信且计入对账；**日历面磁盘形态**：五表 + 十三接口 + `pages/calendar` 9 页落地，非 `svc-calendar` 对 `calendar_*` 的插入被门禁 2 全拒；**`calendar.reminder.fired` 之后底座投递成功、日历面零直连推送（`adapter/push` 不出现在 svc-calendar 依赖树，门禁 4 静态断言）**；**注册表重放 1000 条 ×3：重复到期对象 = 0、漏登记 = 0（19.4#3）**；**到期中心（日历面页面）聚合全平台注册项 + 原生三对象可见率 100%、跨面来源标注正确（19.4#2，≥100 次）**；**必选面：停用日历面 100 次全拒并落审计（8 次计 19.2#9① 负样本支、16 次总数见该行）、`faces[]` 日历格恒在**；**首页 `due_today` 走只读接口且降级正确：杀 `svc-calendar` 20 次注入 → 该格「暂不可用」+ 最后值与 `as_of`、其余面与其余三区不受影响（19.4#7、㉙ 用词）**；**`faces[]` 与 `family/modules` 逐项一致、格数随家庭配置变化、`availability` 两值各有样例**；**`unread` 在 `home/summary` 与 `notifications` 两个出口逐值一致，「全部已读」后两处同时归零**（18.1）；**`due_today.items[]` 恒 ≤3 而 `count` 为全量、`dynamics.items[]` 零越权条目**（儿童身份的账号抽检其父辈私密流水不出现在条目与摘要里） |
| S6（2 周） | 同步与检索 | **三份** `change_log`/幂等表（homeos / finance / **calendar**——日历面三对象与财务同构进同一套机制，3.4.4、§五）+ delta 接口（**每服务一条、当期三条游标**）+ `version` 乐观锁与 409 双版本 + 客户端 per-`(family_id, service)` 队列与重放 + `homeos_search_index`（**`ref_domain` 三个写入来源：homeos / finance / calendar**，§六）+ bigram + 投影写入器与重建脚本（**当期四张投影**，§六）+ 每日对账（**含 `calendar_due_registration` vs 财务来源对象的存在性对账**）+ **`GET /api/homeos/app/bundles` 分包清单契约与客户端校验回退（18.8；清单里当期两个分包：`pages/finance`、`pages/calendar`）** | 19.2#3、#4（当期口径）通过；**三条游标互不干扰**；对账报表可出且零孤儿引用；**19.2#9 三项达标（生效时限、未挂载面零请求零下载、停用前后统计一致；日历面必选、无停用样本可取，改按「停用被拒」断言）**；**底座段收口 = 15.5 第 1-7 项 + 附加门禁②③**（第 8 项前端 shell 与第 9 项数据安全在 S14 收口） |
| S7-S8（4 周） | 财务主干 | `finance_account`/`category`/`transaction`/`ledger` 四表 + 记账写路径（幂等、乐观锁、软删）+ 流水筛选与游标分页 + 单条查询 + 回收站 + **`is_active`/`sort_order`/`is_archived`/`archived_at` 四列与停用·排序·归档·恢复四个接口** | 19.3#1 记账 ≤3 步 ≤5s；19.3#8 跨家庭越权 100 次全拒；**19.3#10 五项断言全过（含余额非 0 归档被拒）** |
| S9-S10（4 周） | 余额与统计 | 余额派生计算 + 统计四视图（概览/趋势/分类/成员）+ 预聚合表 + 分区与索引（22.2）+ **`period` 三档（`YYYY-MM`/`YYYY-Qn`/`YYYY`，⑬）贯通首页/流水/报表/预算四处 + 环比（上期随粒度、缺上期返回「无对比期」）+ 累计余额服务端逐期累加 + 只有月度预算时季/年档返回「未设该周期预算」标识（不折算）** | 19.3#2 ≤300ms（1000 条）与 6 万条 ≤500ms；19.3#3 报表 ≤1s；**19.3#9 四处时间窗一致率 100%、三档逐档抽样 30 格逐值一致、前端二次累加为 0、无折算出的假预算剩余**；**非法 `period` 400 且不回落** |
| S11-S12（4 周） | 预算与账单 | `finance_budget` + 周期记账 + 超支事件 + `finance_bill` 注册到期中心 + 票据附件（单机磁盘 + adapter/storage）+ **`finance.bill.paid` 事件与「标记已付」写路径**（`svc-finance` 生成流水并经事件回写账单状态，**不在请求链路上同步调 homeos**，17.4） | 19.3#4 漏报 0 误报 0、回退不重复提醒；账单可注册可触发；**「标记已付」→ 账单态与生成流水的收敛 P95 ≤5s**（19.1 最终一致性口径） |
| S13（1-4 周） | 导出与录入 AI | 导出三参数全生效 + 小票 OCR + 语音记账 + 手工降级同批（20.3 随面定版已在前完成） | 19.3#7 行数一致无缺列无乱码 + 三参数回归防护；OCR/语音失败可 100% 手输完成同一笔账 |
| S14（1-3 周，缓冲；**三个 ≥24 小时的长压窗口（㉚ 由两个窗口增为三个，19.2#13 的第三个分母是 `svc-calendar`）占墙钟 1 天（三套同构压测栈并行跑，+1 天而非 +3 天，§十三），落在本卡缓冲内。M1 总区间 28-33 周与六期墙钟 158-191 周（㉚ 修订 ㉑ 的 26-31 与 156-189，原值保留不删）**） | **M1 收口** | **附加门禁① 两支：财务面跨服务五步 demo + 日历面用真实三对象走五步（§8.3、§8.4，㉚ 判据由一支变两支）** + 前端 shell 收口验收（D 行五项 + **顶栏一行**（家庭切换器 + 搜索 + 带红点的消息入口，**中段不放面切换器，⑫**）+ **时间窗条三档（⑬）** + **首页变长服务矩阵（全 App 唯一面入口；**日历格恒在——必选面，5.5.9**）** + ＋ 独立路由页（两段来源：业务面对象段含日历面、底座段改为「日历面三类 + 留言」，§九）+ **`pages/finance` 与 `pages/calendar` 两个分包与按需加载（三处分包：主包 + 日历 + 财务，㉚）** + **CI 三查：无 `tabBar`／分包集合 = 已出生域 − `homeos`（当期命中恰好两个）／路由全三段式** + **七条前端同源检查（含「第二处面入口 0 命中」）**）+ `GET /api/homeos/app/version` + 设备锁「忘记口令重验」链路 + admin 五功能（**死信与对账当期按三个服务查看**）+ 备份首次恢复演练 + **数据分级 L2/L3 落盘加密与备份包抽检（19.2#7，`contracts/data-levels.yaml` 逐字段，㉒㉔）**——按 ㉒ 的两路判定：文本类 L2 列 = 字段级密文、金额与余额 = 库内明文列 + 磁盘与备份密文 + 合规素材四项 + S1 单人路径走通 S6 财务段 + **19.2#11 非功能基线一轮**（启动耗时、一级/二级页响应、主包与分包体积、弱网与断网重放，各 ≥100 次；**含 40 条主链路冒烟集 ×3 = 120 次，作为 19.2#8 设备数不足 500 时的崩溃替代判据**）+ **19.2#10 亮/暗两态逐屏对比度走查（60 条**实建**路由 × 2 态 = 120 屏；60 = 主包 22 + 日历 9 + 财务 29，位账 23 + 9 + 29 = 61 的差额是 `vote/detail` 当期不建——导航文档 §2.5 逐条重算，⑰ 总数守恒、㉚ 只改分布）并归档报告** + **19.2#13 每服务一次 ≥24 小时、净样本 ≥10 万次的长压（P1 即判 ≥99.9%，**三个服务三个分母各算各的**（homeos / finance / **calendar**，㉚ 由两服务两分母改为三分母）、不得合并成全栈平均值；同构压测实例 + 专用压测家庭，见 §十三）+ 每服务各 20 次故障注入的行为断言（串扰 0、恢复 ≤60s、每服务 20/20 全过，**当期 3 × 20 = 60 次**，注入排在长压窗口之外）** + **19.2#14 设备锁与 L3 解锁链路抽检（100 / 30 / 30 / ≥100 次，㉗）** + **日历面出生期报告按 19.4 七条出数（创建步数与 P95、到期中心可见率 100%、注册幂等重放 ×3 零重复、回写零失败、周期规则单一阈值、月视图与跨面聚合读性能、面级降级 20 次注入——七条各带度量/分母/判定；19.2 的十四条不因此重复计量）** + 15.5 第 1-10 项验收报告 | **19.2#13 三个已出生服务各自的长压窗口出数（净样本各 ≥10 万次、各自 ≥99.9%、`report.json` 归档）+ 每服务 20 次注入行为断言全过（共 60 次）**；**19.2#14 设备锁与 L3 链路抽检 100 / 30 / 30 / ≥100 次全过**；**附加门禁①（双面各五步）③（一条命令 ≤30 分钟，容器 +1 口径不变）通过**；**owner 视角矩阵格数 = 本家庭已启用面数且日历格恒在（角色可见集另按 19.2#9/#12 断言；「除必选面外无可见面」的 children/guest 按 19.2#12① 原判据复测 C 区是否整区不渲染，㉚ 只改词不放宽）**，未挂载面零请求零下载；**自面内切面实测 3 步、全 App 走查无第二处面入口（⑫）**；已建路由数与导航文档一致（**实建：主包 22 + 日历 9 + 财务 29 = 60；位账：23 + 9 + 29 = 61，差额 = `vote/detail` ③ 未建**）；**停用前后同一时间窗统计值一致（停用样本取财务面；日历面按「停用被拒」断言）**；M1 验收报告提交人类；**M1 未过不得开工 M2** |
| S15-S16（4 周） | 借贷与目标 | `finance_loan` + `finance_repayment_plan` + `finance_goal` + 两者接入到期中心 + 还款登记回写 | 还款计划每期前 3 天可触发；目标截止前 30 天可触发；回写收敛实测 |
| S17-S18（5-6 周） | 分账与净资产 | `finance_split`/`settlement` + AA 余数规则 + 负债对象 + 净资产与资产负债报表 + 多币种 + 信用卡 + 发票与报销 | 19.3#6 两项等式全成立、余数可复现；**19.3 全部 10 条达标（M1 的九条全部重跑，不豁免复测）** |
| S19（1-2 周，缓冲） | **M2 收口与 P1 末门禁** | 删除 demo 表与接口、`svc-finance` 契约冻结、功能覆盖走查对照主流记账 App、P1 末附加门禁三项（合规文本、依赖选型整表、推送凭证与出包）+ P1 验收报告 | **4.9 全部达标 + 覆盖走查 ≥80%（19.7）+ P1 末三项门禁通过**；通过才对外发布，并作为 P2 开工前置 |

**任务卡**按 23.3 建在 Jarvis Workbench 的 HomeCube 项目下，每卡必填「归属服务」「归属数据源（17.2）」「外部依赖（20.1，无则填『无』）」，生效前走强制人工审核。**M1 与 M2 的卡不得混排在同一期开工**（12.7 第 2 条）。

---

## 十二、风险与本方案刻意保守之处

1. **缺陷形态**：跨服务逻辑的 bug 从「写错」变成「时序与补偿错」。所有跨服务判据一律写成收敛时间与死信处置，不写「调用成功」；对账从 S6 起是常驻卡而非一次性卡。
2. **P1 是双段切片 + 双面出生，最大风险是段间返工**：财务段（S7-S14）会用到底座段（S1-S6）的鉴权、到期、同步、搜索四块，任何一块口径在财务段被推翻都要重做底座。处置：S6 末的底座段收口即**冻结底座契约**（23.2 第 7 条），财务段发现缺口一律走 23.1 第 2 条偏离评审，不允许在财务段顺手改底座。**㉚ 的叠加风险与处置**：日历面在服务端属于底座段（S5 卡），但它的注册与触发是财务段账单/预算/还款到期链路的**前置依赖**——`svc-calendar` 的契约（`/api/calendar/*` 十三条、`calendar_due_registration` 幂等键、`calendar.reminder.fired` 载荷）随底座契约一起在 S6 末冻结，晚于该时点的日历侧变更一律走 23.1 第 2 条；S5 由 2 周扩为 4 周是底座段的真实占位，若该卡滑期，滑的是 M1 下界（14 周只增不减），不允许用压缩 S6 或跳过必选面断言来找平。
3. **OCR/ASR 提前进 P1**：依赖选型的两级定版（20.3）意味着 M1 开工前必须定这两家供应商，是全案唯一在 P1 就要签的真实外部依赖。处置：先定版、再排 S13；未定版期间代码只允许出现在 `adapter/ocr`、`adapter/asr` 接口后，业务域不感知供应商。
4. **单机资源**：**P1 期末 8 个常驻容器**（= nginx + postgres + nats + **3 个服务** + web + admin，㉚ 比原口径多 1 个 `svc-calendar`；八服务全出生为 13 个），**S1~S4 是 7 个**（少 `svc-calendar`）；在家庭自托管机器上的占用是真实风险，S1 末即按当期 7 个实测并回填 22.1，**S5 起再按 8 个复测**（当期阈值，不是八服务阈值）。
5. **NATS 单节点 `R=1`**：流不跨节点复制，磁盘故障可能丢掉「已 ack 但未归档」的窗口。缓解：outbox 在 Postgres 侧（有 WAL 归档）是发布方的权威留存，归档流进每日备份范围；不做 JetStream 集群（15.6）。
6. **发布顺序耦合**：SDK 与事件目录由 homeos 服务提供，其契约破坏性变更会同时打断财务面。处置：契约门禁 5 + 语义化版本 + 旧 subject 保留一个期 + homeos 先行发布。**㉚ 新增第二个耦合点**：到期触发引擎与注册表由 `svc-calendar` 持有，它同时被财务段（账单/预算/还款的注册方）与底座段（首页 B 区 `due_today` 的读方、`calendar.reminder.fired` 的投递方）依赖——日历面不可停用（必选面）意味着这一跳没有「关闭后照常运行」的形态，只能靠降级兑现（17.3 方式一超时重试 + 「暂不可用」+ 最后值与时效戳，§3.5）。处置同上：门禁 5 冻结 `calendar.*` 契约 + S5 随底座段先发布。
7. **pver 变更风暴**：一次批量权限调整会让所有服务缓存失效并回源拉快照。缓解：快照接口按 `fid` 聚合（一次拉全量而非逐成员），回源带抖动与 singleflight。
8. **bigram 召回上限**：当期只判 P95 ≤500ms，**跨七面命中**（面数随 ㉚ 由六增为七，阈值 45 与样本 50 不变）在 P6 判；若届时命中 <45/50 才评审 `zhparser`（需换镜像，走 23.1 第 2 条）。
9. **demo 表变影子数据源**：S14 建、S19 删，中间若忘了删就会长期成为第二数据源。处置：S19 首张卡即删除（**不是 M2 首卡**，两处表述已归一），门禁 2 对 `finance_demo_item` 收紧白名单、对账与 17.2 均不纳入，且 P2 出生期复验五步时用真实采购对象，不再新建 demo 表。
10. **墙钟串行**：P1 的 **28-33**（㉚ 修订 ㉑ 的 26-31）+ 10-12 周是后续五期的前置，任何滑期整段后移（12.9 墙钟风险；六期净工作量与墙钟按 ㉚ 为 **150-183 / 158-191 周**，S5 扩卡 +2 周是唯一来源）。处置：M1/M2 两个验收点让底座缺陷最迟在 S6 末暴露，S14 与 S19 各留 1-3 周缓冲，且**缓冲只用于顺延，不用于压缩完整度**；三个长压窗口并行跑只占 1 天墙钟（§十三），不把 S14 缓冲吃成三个整天。
11. **面集合从常量变成会话级变量**：凡是隐含「面集合是常量」的地方都要改成读同一集合——**五个导航消费位**（首页矩阵、「＋」目标对象、搜索分组、到期中心注册项、动态流筛选）+ 埋点维度 + 测试夹具；**顶栏不在其列**（⑫ 后它不消费面集合的任何形态，把它列进消费者就是给「第二处面入口」留后门）。一处漏改就是「未启用面出现在导航里」这类 19.2#9 直接判失败的表现。处置：集合与角色可见集只在服务端一处合成并同时供给 `family/modules` 与 `home/summary` 两个接口（比对口径 `enabled ∧ visible`），客户端从基座取一次、不得二次裁剪；CI 与验收用「未挂载面零请求、零分包下载」的抓包断言兜住，不靠人工走查。

---

## 十三、联调与环境

- 本地：`make up` 起全栈，`make dev-{code}` 把某服务跑出容器外调试（`dev-homeos` / `dev-finance` / **`dev-calendar`（㉚ 新增第三个，随 `svc-calendar` 在 S5 出生；S1~S4 只有前两个）**），端口与 env 在 `deploy/env.local.example`。**这句里的「端口」半句现在有落盘对价**：该文件登记 `HOMEOS_ADDR=":8080"` 与 `FINANCE_ADDR=":8081"` 两个监听地址键是 ㉚ 前的现状，**日历面出生即按同形态补 `CALENDAR_ADDR=":8082"`（S5 卡的 env 项）**——键名形态仍是 `{CODE}_ADDR`，不引入第二套口（同一文件另有 nginx 的宿主端口与三个上游地址键，属 S1-D 侧登记的口），键名与取值口径的源头就是下面这条三层解析。
  - **监听地址的三层解析（S1 已落，代码在 `packages/obs`）**：优先级恒为 cmd 的 `-addr` flag > 本服务的可选环境键（形态 `{CODE}_ADDR`，当期即 `HOMEOS_ADDR` / `FINANCE_ADDR` / **`CALENDAR_ADDR`**）> 按 `registry.Implemented()` 的**登记序**派生的默认端口（基数 8080 + 下标）。当期解析结果即 `svc-homeos` → `:8080`、`svc-finance` → `:8081`、`svc-calendar` → `:8082`（**8082 这一格是登记序派生的结果，不是新口诀**；**`:8082` 上的可拉进程随 S5 才存在，S1~S4 只有两个 dev 目标在运**），**S5 起三个 dev 目标**可同时跑而不撞号。`make dev-*` 都不传 `-addr`（配方只有 `go -C server run <pkg>`，env 由 `set -a` + `source deploy/env.local` 注入），所以实际生效的是第二层、第三层是兜底，两层取值相同——改端口只改那一个文件。`make up` 下每服务各自一个容器（§10.1），进程绑的监听端口与 nginx 拨号的上游端口要成对一致（`*_UPSTREAM` 的端口段跟着 `*_ADDR` 走）。
  - 第三层那条规则（**基数 8080 + `registry.Implemented()` 登记序**）此前只写在代码里，文档从未登记，本节登记其存在。它**刻意不进域表**（§1.3「端口、env 键名、镜像名不存字段」），也不是一条要人记住的口诀：后续面出生即自动占下一格，无需在这里加行；写成未登记为实建的 code 会被拒绝启动，而不是与谁撞号。第二层那个键名同理来自代码的 `addrEnvKey`，如今两边都已落盘。
- **可用性长压环境（19.2#13）**：一套与家庭实例**同构但独立**的 Compose 栈（同编排、同资源规格、同迁移序列），`make soak` 起；负载由栈内容器打持续请求流，**不经真机、不占 19.1 的客户端测试环境口径**，写流量只进专用压测家庭。**窗口排他**：一次 ≥24 小时的连续窗口内不得做首次恢复演练（`make restore` 会全栈重建）、不得做故障注入（杀容器即断开该前缀），这两项排在窗口之外；当期**三个服务各起一套压测栈并行跑**（㉚：第三套属于 `svc-calendar`，19.2#13 的三个分母、三个 ≥24 小时窗口；**三个窗口都在 S14/M1 收口判——`svc-calendar` 随 S5 才出生，S5 之前只有两套在运**），墙钟 +1 天而非 +3 天。
- 契约测试：`test/contract/` 用事件目录与 OpenAPI 做双端断言——提供方改动若破坏消费方，CI 在合并前失败，不靠联调期发现。
- 事件回放：`make replay?stream=HC_ARCHIVE&from=<时间>&subject=<前缀>`，仅运维口，回放走同一幂等键，不产生重复对象（19.2#5）。
- 时间旅行调试：所有事件信封带 `causation_id`/`correlation_id`（11.2），管理台死信详情页可反查链路。

---

## 十四、本方案与 PRD 的对应关系

| 本方案章节 | 回答的 PRD 条目 |
|---|---|
| 一、拓扑与工程结构 | 15.5 第 1 项、卷首第 12 项、23.2、23.4 |
| 二、数据边界 | 15.5 第 1 项、17.1、17.2、17.6、23.5 门禁 1/2 |
| 三、跨服务通信 | 15.5 第 6 项、3.4.6、11.1-11.4、17.3、17.4 |
| 四、鉴权 | 15.5 第 2、3 项、16.2-16.6、19.2 附加门禁② |
| 五、同步底座 | 15.5 第 5 项、3.4.4、22.6、19.2#3 |
| 六、搜索与投影 | 15.5 第 7 项、17.3、18.6、19.2#4、19.7 对账 |
| 七、通知下发 | 15.5 第 4 项、3.4.2、**18.1（未读单一源）**、19.2#2、20.1 推送行 |
| 八、双面落地拆法（财务面 + 日历面） | 15.5 第 10、11、12 项、四章、**五章（5.5 到期中心、5.7 五表、5.8 十三接口、5.5.9 必选面）**、4.9、17.2、17.4、19.3、**19.4（日历面七条）**、19.2 附加门禁①（**双面各五步，㉚**） |
| 九、客户端与管理台 | 15.5 第 8 项、十八章（含 18.8、**18.9**）、**4.5.3（时间窗三档）**、3.6（`Member.relation`、`unread`）、19.2#9、**19.2#10**、19.3#9、13.4 |
| 十、部署与 CI | 15.5 第 1、9 项、22.3、22.5、23.4、23.5 |
| 十一、排期与拆卡 | 12.3 P1 两行、12.7、23.3 |
| 十四（本章） | 全局追溯 |
| 十五、定版口径 | PRD 未覆盖的技术分叉，属 P1 契约 |

---

## 十五、技术决定的定版口径

以下十八处是 P1 契约的一部分：底座段口径冻结至 **P1-M1 末**（23.2 第 7 条）——**日历面口径随底座段一起冻结在该时点**（它的服务、五表与触发引擎在 M1 的 S5 卡出生，不存在「M2 末再冻一次」的口径），财务面口径冻结至 **P1-M2 末**；变更须走 23.1 第 2 条偏离评审。标 ★ 的三处是「服务随面出生」模型带来的口径修订，与**八服务目标形态（㉚ 由七服务增为八服务：HomeOS 底座 + 七个业务面）**并存而非替代。

| # | 决定 | 内容 | 已接受的代价 |
|---|---|---|---|
| A | 迁移序列 | 每服务一条独立序列 + 独立版本表（golang-migrate per-service），**序列数随出生期增加** | 无全局版本视图；跨服务变更靠契约测试而非排序保证 |
| B | 会话与权限载体 | JWT access(15min)+refresh(30d 轮换)，claims 带 `fid`/`role`/`pver`，RS256 + JWKS 分发 | 自管会话撤销表；纯 session 每请求一查被排除 |
| C | 中文检索 | 应用层 bigram + `simple` tsvector，不改 Postgres 镜像 | 召回弱于词典分词；命中不足阈值才评审 zhparser |
| D | 客户端离线队列 | `storage` 中 `pending_ops` + 自研重放器（三触发） | 容量受 storage 上限约束；本地 SQLite 需原生插件且 H5 不具备 |
| E ★ | 服务边界与出生时机 | 目标形态一面一服务 + homeos 底座服务共 **8 个**（㉚：registry 新增 calendar 一行，系统总数 7→8），不设网关服务；**P1 期末建 3 个**（homeos / finance / calendar，服务随面出生——日历面与财务面同在 P1 出生，**日历这一支的出生时点是 S5，S1~S4 只建 2 个**），**其余五个**只有 registry 条目（8 − 3 = 5，数字不变），目录/schema/分包/流一律不预建，CI 按 registry 校验命名合法性并禁止提前出现 | P2-P6 每期都要重走一遍五道门禁与接入清单（12.9 的重复验证风险）；换来的是当期交付面唯一、骨架不空转 |
| F | 数据隔离 | 单集群 + 每服务一个 schema 与一个专属账号（**S5 起 3/3，S1~S4 为 2/2**，目标形态 8/8），账号权限即边界 | 无跨 schema JOIN；聚合读靠本地投影或 17.3 的只读调用——首页财务格走投影、首页日历格走 `svc-calendar` 只读接口（㉚），两种形态都不许在 SQL 里跨 schema |
| G | 总线载体 | NATS JetStream：每服务一条源流 + 归档流 + 死信流，`R=1`，流随服务出生（**P1 的落地即 S2 建两条、S5 随 `svc-calendar` 建第三条，期末态三条**） | 单节点无流复制；发布方权威留存靠 outbox |
| H | 一致性模型 | 无分布式事务；事务内 outbox + 消费端幂等表 + 每日对账 | 跨服务可见有 ≤5s 延迟；引用完整性靠对账而非约束 |
| I | 鉴权实现 | 单一 SDK（`packages/authz`）+ **S5 起三服务**（S1~S4 两个）同一中间件位置，内置矩阵含**八个系统行 / 十行断言 = 50 格**（㉚ 修订 ㉓ 的九行 45 格，其中日历行 5 格为逐格平移）；不可达时拒写允读 | homeos 是鉴权面单点，需健康检查与缓存兜底 |
| J | 跨面读 | 三选一：只读 HTTP（≤300ms/≤1 跳/必带降级）／本地投影／逻辑 id | 数据有多份视图副本，必须可重建 + 对账 |
| K | 到期注册 | 一律事件异步（`{code}.due.registered` / `{code}.due.revoked` → **`svc-calendar`**，消费方由底座改为日历面，㉚；触发 `calendar.reminder.fired` 由底座消费投递，回写走 **`calendar.todo.completed`**） | 到期中心可见非实时；注册失败靠死信与对账发现；**日历面不得直连任何推送通道**（投递唯一归底座，5.5.8） |
| L ★ | 同步游标与搜索归属 | per-service `{code}_change_log`，客户端游标 `(family_id, service)`——**P1 期末三条**（homeos / finance / calendar；**`calendar` 那条随 S5 出生，S1~S4 是两条**），**桶随出生期增加，不预建**；索引只在 `svc-homeos`，`ref_domain` 取值域按域表八个 code 定义、**P1 有三个写入源**（㉚：日历面对象进同一张索引，S5 起） | 客户端要管随期增长的游标；无全局一致性点；未出生域搜索返回空并提示「即将上线」（≠ 本家庭未启用，两词不得混用，15.5 第 7 项、18.8） |
| M ★ | 验收与发布单位 | 每服务一镜像一发布线，homeos 先行，降级才允许多进程合并；**期末验收对象 = 当期出生的整面**（㉚：期原则为「每期交付当期出生面的整面」，P1 例外为财务 + 日历**双面**，两支各按 19.2/19.3/19.4 自己的条数判、不互相摊薄），19.2 附加门禁①②③在每个出生期末以同一判据复验，不因重复而豁免 | 发布次数随期增加；复验成本固定（P1 因双面例外多付一支五步 demo + 一份 19.4 报告）；灰度按家庭白名单不变 |
| N | 客户端导航载体 | **不注册 uni-app 原生 `tabBar`**：底部五项、顶栏一行与首页 C 区服务矩阵都由 shell 自定义组件渲染，一级页无栈互切、二级页压栈；页面全部三段式独立路由 `pages/{code}/{feature}/{action}`，破坏性动作走统一弹层不成页（18.7 第 6、7 条）；`pages.json` 的三查进 CI（导航文档 §2.4）。**定版 ⑫：面切换器不进顶栏，面入口只有首页矩阵一处**（早期方案曾把已启用面平铺成顶栏 chip，该部分已作废） | 放弃原生 TabBar 的系统级手势与渲染红利，切面全靠 JS；**切面代价从 1 步升为恒定 3 步**（面内 → 首页 → 点格），这是换取"顶栏与面数彻底解耦、面集合只有一处渲染"的定价；页面数随功能线性增长（财务面 42 条路由），必须靠成页判定表与封闭动词表约束，否则路由表会失控 |
| O | 面集合的权威源 | 挂载集合 = `registry`（面目录）∩ `homeos_family_module`（家庭选择）∩ 服务已出生，三源在 `svc-homeos` 一处合成，同时供给 `family/modules` 与 `home/summary.faces[]`；客户端五个消费位（首页矩阵、＋ 目标、搜索分组、到期中心注册项、动态流筛选）同源读它，**不再有顶栏这一处（⑫）**；`Family.feature_flags` 不再承载面开关；**日历面是必选面：它在合成结果里恒为启用、客户端不渲染它的开关（5.5.9，㉚）** | 两个接口必须同源否则契约测试失败；多一张配置表与一次传播（P95 ≤2s）；「停用不改统计口径」要专门断言（日历面没有这条路，改断言「停用请求被拒 + `faces[]` 日历格恒在」） |
| P | 前端分包下发 | 每面一分包 + `app/bundles` 清单（`version`/`url`/`sha256`/`size`/`min_compat`）+ 客户端校验与失败回退；**P1 只落契约与代码路径，真实远程下发链路自 P2 起启用**；iOS 侧只用 wgt 资源包，**不引入动态代码执行** | 清单与校验是 P1 就得维护的一条路径；包体积与兼容性判断由服务端配置承担，出包流程随 P2 变重 |
| Q | 主题与未读的单一源 | **主题 = 主包一份 CSS 变量（亮/暗两套值，三态含跟随系统）**，分包只引用变量；**未读 = shell 一份 `unread` store**，服务端一个聚合口径（`read_at IS NULL` 按 `member_id`）、两个出口（`home/summary` 首屏、`notifications` 列表与已读），顶栏红点与 D 行红点共读它。两者都由门禁 4 做静态检查（18.1、18.9、19.2#10） | UI 规格必须先出两态色板并逐屏走查（M1 即 120 屏，是新增的固定成本）；首页聚合接口因此承担通知计数，`home/summary` 多一次按 `member_id` 的聚合查询；主题不进服务端意味着换设备要重设 |
| R | 宿主 Go 工具链 | **只认本机已有的那一个工具链，构建期绝不静默下载**：宿主 `go1.27.1 darwin/amd64`，装在 `~/sdk/go`，**不在默认 PATH 上**（交互使用先 `export PATH=~/sdk/go/bin:$PATH`）。取 go 的口径按消费者不同：`Makefile` 的变量口是 `$(HOME)/sdk/go/bin/go`（可用 `GO=` 覆盖），找不到就**打印归属并以非零码退出，不回落到 PATH**——make 配方走 `/bin/sh`，用户级 shell rc 里的 PATH 对它无效；两个 Node 侧调用点先探 `~/sdk/go/bin/go`、不存在才回落 `go`，另有 `HC_GO`（指 go）、`HC_REGISTRYD_BIN`（直接给预构建二进制）、`HC_SERVER_DIR` 三个覆盖口。`server/go.mod` 声明 `go 1.27` 且**不带 `toolchain` 指令**；每个 go 调用点显式传 `GOTOOLCHAIN=local`（用户级 go env 文件里该值也已是 `local`，显式传参是第二重保险）。CI 侧的 Go 安装方式属尚未交付的项——§1.2 登记的 `.github/workflows/` 今天是占位目录，五道门禁的接驳还没进 CI | 换机器或换 CI 环境要先自备 1.27.x，否则命令口以非零码退出并打印归属提示；`go.mod` 的 `go 1.27` 与宿主实际版本靠人工对齐，工具链升级是一次显式动作而不是自动发生 |

**R 行同时登记一处错引（已登记、Makefile 修正待另一卡）**：`Makefile` 里解释「构建期不许静默下载工具链」的那句注释，把这个策略的定版口径指向 §十五 的 **B 行**，而 B 行是会话与权限载体（JWT + JWKS），与工具链无关；正确的指向是上面新增的 R 行。`Makefile` 归另一张卡持有，**本节只登记这条错引与其修正方向，不改那个文件**。
