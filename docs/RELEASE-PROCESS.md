# HomeCube 发布流程（P1 · H5 静态先行）

> 适用范围：P1 的 HomeOS 基座 + 财务管理面。
> 口径来源：PRD 17.8「P1 只交付按需启用 + 分包清单与校验回退的代码路径；财务分包随基座一起发布，
> 真实远程下发自 P2 起用」，PRD 3.4.8 / 3.7 / 12.4。
>
> 本文取代 2026-10-09 的设计草案。那一版描述的 `build-bundle.sh`、wgt、对象存储/OSS、CDN 刷新、
> `/admin/versions`、灰度与自动回滚在本仓都不存在（也从未跑通），照着做只会得到假产物。
> P1 的发布物只有一份 H5 构建和一份清单，部署到服务器上的 nginx 静态目录。

## 1. 发布物

| 产物 | 路径 | 说明 |
|---|---|---|
| H5 站点 | `dist/releases/base/{x.y.z}/site/` | 可直接由 nginx 托管的静态文件 |
| 基座包 | `dist/releases/base/{x.y.z}/homecube-base-{x.y.z}.zip` | 上面站点的归档；客户端下载与校验的对象 |
| 清单 | `dist/releases/manifest.json` | 唯一权威清单；`verify-release.sh` 以它为准 |
| 内嵌清单 | `server/services/svc-homeos/internal/handler/bundle_manifest.json` | 与上一条逐字节相同，`go:embed` 编译进二进制 |

不产出：独立的面分包、wgt、CDN 上传、灰度规则、自动回滚脚本。

目录与线上布局（`/releases/` 是 nginx 暴露的 URL 前缀）：

```
dist/releases/                                 服务器发布根（例：/srv/homecube/releases/）
├── manifest.json                              → /releases/manifest.json
└── base/1.0.0/
    ├── site/                                  （仅本机预览用，不必上传）
    └── homecube-base-1.0.0.zip                → /releases/base/1.0.0/homecube-base-1.0.0.zip
```

## 2. 版本与命名

- 版本号是语义版本 `x.y.z`，不带前缀；标签由脚本/CI 派生为 `base_v{x.y.z}`。
- 文件名固定 `homecube-base-{x.y.z}.zip`，URL 固定 `/releases/base/{x.y.z}/homecube-base-{x.y.z}.zip`。
- `min_compat` 有两个位置，含义不同：
  - `base.min_compat`：客户端基座低于它时更新引导不可跳过（PRD 12.4，强制更新的唯一触发路径）。
    构建时由 `MIN_COMPAT` 给出，默认 `1.0.0`；**普通发版保持默认**，只有破坏性变更才提高。
  - `bundles[].min_compat`：能载入该面分包的最低基座版本。P1 分包随基座同包发布，等于本次基座版本。
- `generated_at` 是构建时间，仅供排查；CI 的漂移检查忽略它，其余字段必须逐字段一致。

## 3. 清单格式

```json
{
  "generated_at": "2026-10-26T13:39:06.680Z",
  "base": {
    "version": "1.0.0",
    "url": "/releases/base/1.0.0/homecube-base-1.0.0.zip",
    "sha256": "3ee7d0db78341580993f977fffecd129db382e32ae0abe56565ab038c160b595",
    "size": 205732,
    "update_notes": "P1：HomeOS 基座 + 财务管理面",
    "min_compat": "1.0.0"
  },
  "bundles": [
    {
      "code": "finance",
      "version": "1.0.0",
      "url": "/releases/base/1.0.0/homecube-base-1.0.0.zip",
      "sha256": "3ee7d0db78341580993f977fffecd129db382e32ae0abe56565ab038c160b595",
      "size": 205732,
      "min_compat": "1.0.0"
    }
  ]
}
```

语义：

- `bundles[]` 只含当期已出生的面（`registry.BundleSet()`，homeos 不算分包）。P1 只有 finance。
- P1 的 `url`/`sha256`/`size` 指向承载它的基座包；P2 改为每面一个包时，**接口字段不变**。
- 基座不是 bundles 条目；它的版本与 `min_compat` 由 `GET /api/homeos/app/version` 返回。

服务端从内嵌清单直接作答（`svc-homeos/internal/handler/homeos.go`）：

| 接口 | 行为 |
|---|---|
| `GET /api/homeos/app/version` | 返回 `current_version` / `update_notes` / `min_compat`；无 `family_id`、无强制更新布尔字段 |
| `GET /api/homeos/app/bundles?codes=…&installed=…` | 按 `codes` 过滤清单；未知/未出生 code 忽略；`installed` 仅接收，客户端自行比对本地 (code, version) |
| 清单不可用（构建缺陷） | 两个接口都返回 `503 {"error":"bundle_manifest_invalid"}`，不回退到任何猜测版本 |

启动时校验，任一不满足即 503：版本与 `min_compat` 是 `x.y.z`；`size > 0`；`sha256` 是 64 位小写十六进制；`url` 以 `/releases/` 开头且不含 `..`；`code` 在 registry 中、已出生、不是 homeos、不重复；**分包集合与 `registry.BundleSet()` 完全相等**。

## 4. 构建

前置：node/npm、Go 1.27（`PATH` 或 `~/sdk/go/bin/go`）、`zip`、`shasum`。缺任一项脚本直接失败。

```bash
# 在仓库根执行
bash scripts/build-base.sh 1.0.0

# 破坏性变更时显式提高最低兼容版本；更新说明写入清单与 Release 正文
MIN_COMPAT=2.0.0 RELEASE_NOTES="2.0.0：路由结构重构，旧基座需更新" bash scripts/build-base.sh 2.0.0
```

脚本六步，任一步失败即非零退出：

1. 前置检查，解析语义版本与 `MIN_COMPAT`（必须 `<=` 本次版本）；
2. 从 registry 读取当期已出生面（分包集合的唯一真源）；
3. `npm run build:h5`；
4. 产物断言：`index.html` 存在、`pages-finance-*.js` 至少 1 个、未出生面（purchase/diet/trip/kin/growth）零 chunk（PRD 18.2#9）；
5. 归档：固定 mtime 后 `zip -X`，计算 sha256/size；同一份源码重复构建得到**相同**哈希；
6. 生成清单（发布副本 + embed 副本，逐字节相同）并执行 `scripts/verify-release.sh` 自检。

当前 P1 基线（2026-10-26）：finance chunk 13 个、未出生面 0 个、zip 205,732 字节、sha256 `3ee7d0db…c160b595`，连续两次构建一致。

## 5. 发布（手动）

上传与部署由人执行；脚本不代传，也不写服务器地址。

### 5.1 二进制

内嵌清单随源码编译，**必须用生成该清单的同一份源码构建** svc-homeos，否则服务端会发布一个与线上 zip 不符的哈希：

```bash
cd server
~/sdk/go/bin/go build -o bin/svc-homeos ./services/svc-homeos/cmd/svc-homeos
```

用你已经使用的方式（本机替换 / 重建镜像）部署新二进制。

### 5.2 静态产物

```bash
# <user>@<host>:<WEB_ROOT> 换成服务器上 /releases/ 指向的目录（例：/srv/homecube/releases）
rsync -av --delete dist/releases/ <user>@<host>:<WEB_ROOT>/
```

`--delete` 会删除服务器上不再存在的旧版本目录；保留上一版用于回滚时不要加，或先把旧目录另存。

nginx：

```nginx
# <WEB_ROOT> 的父目录是 /srv/homecube，它的 releases/ 即发布根
location /releases/ {
    alias /srv/homecube/releases/;
    # 版本号在路径里，产物永不变更，可长期缓存
    add_header Cache-Control "public, max-age=31536000, immutable";
}

# 清单会被同版本重写（排查/复核用），不缓存
location = /releases/manifest.json {
    alias /srv/homecube/releases/manifest.json;
    add_header Cache-Control "no-cache";
}
```

### 5.3 线上校验

```bash
# 在能读到发布根的机器上执行；把公共前缀换成实际路径
bash scripts/verify-release.sh dist/releases/manifest.json /srv/homecube/releases

# 逐项输出 PASS  base / PASS  bundle:finance，最后一行「清单与产物一致」
```

接口校验（必须带 token；`app/version` 不是免鉴权口，定版 ㉕）：

```bash
TOKEN='<登录后拿到的 access_token>'
curl -s -H "Authorization: Bearer ${TOKEN}" https://<host>/api/homeos/app/version | jq
curl -s -H "Authorization: Bearer ${TOKEN}" \
  "https://<host>/api/homeos/app/bundles?codes=finance&installed=%7B%7D" | jq
```

期望：前者恰好三个字段且值等于清单的 `base`；后者只有 finance 一项，`url` 指向 `/releases/...zip`。

## 6. 失败与回退

- 校验失败（size/sha256 不符、超时、`min_compat` 不满足）：客户端丢弃本次下载，保留上一版已安装分包；该面显示「暂不可用」，基座与其他面照常可用，不进白屏（PRD 17.8、导航文档 §6.11）。P1 客户端加载器尚未接线，服务端只负责给出可校验的清单。
- 清单无效（构建缺陷）：服务端 503，版本检查失败不阻断首屏（PRD 3.4.8）。
- 缓存：产物 URL 带版本号、内容不可变，可长期缓存；清单 `no-cache`。发布新版本后旧目录可由运维择期删除，删除前请确认没有回滚需求。

回滚没有自动化脚本，按产品取舍只保留手工路径：重新部署上一版二进制 + 把上一版 `dist/releases/` 目录恢复到发布根，然后跑 5.3 的校验。

## 7. CI

`.github/workflows/release.yml` 只由 `base_v*` 标签（或手动触发）驱动，单 job：

1. `go test ./...`（失败即停，没有 `|| true`）；
2. `bash scripts/build-base.sh <版本>`（含产物断言与清单自检）；
3. 生成物漂移检查：`bundle_manifest.json`（忽略 `generated_at`）与 `web/generated/domains.json` 必须与本次构建一致——不一致说明二进制会发布过期哈希或旧域表，直接失败；
4. 上传 `manifest.json` + zip 为构建产物（`actions/upload-artifact@v4`）；
5. 用 `gh release create/upload` 挂到 GitHub Release。

标签触发固定使用 `MIN_COMPAT=1.0.0`（普通发版可跳过更新）；破坏性变更请用手动触发并填 `min_compat`。

CI 不接触服务器、不持有部署凭据；部署仍是第 5 节的手工步骤。

## 8. P2 起

- 每个面独立打包（`homecube-{code}-{x.y.z}.zip`），`bundles[]` 各指向自己的包；
- 真实远程下发（CDN/对象存储）、灰度、自动回滚、差分更新；
- `/app/bundles` 的字段与 `/app/version` 的字段保持不变，客户端不需要改协议。

## 9. 命令速查

```bash
# 构建 + 自检
bash scripts/build-base.sh 1.0.0

# 只校验已有清单与产物
bash scripts/verify-release.sh dist/releases/manifest.json dist/releases

# 本地静态预览（无需 nginx）
python3 -m http.server 8090 --directory dist/releases

# 发布（手动）
rsync -av --delete dist/releases/ <user>@<host>:<WEB_ROOT>/
bash scripts/verify-release.sh dist/releases/manifest.json <WEB_ROOT>

# 打标签（发布流程的最后一步）
git tag base_v1.0.0
git push origin base_v1.0.0
```
