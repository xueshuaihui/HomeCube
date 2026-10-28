# 本地发布与加载验证（P1）

目标：在没有对象存储、没有 CDN 的情况下，用本机静态目录走通 PRD 17.8 P1 要求的四件事——
清单可读、产物可校验、失败有明确状态、缓存可清理。发布模型见 `docs/RELEASE-PROCESS.md`。

P1 的产物只有一份 H5 构建（`dist/releases/base/{x.y.z}/site/` + `.zip`）和一份清单
（`dist/releases/manifest.json`，与 `svc-homeos` 内嵌副本逐字节相同）。服务端直接读内嵌清单，
不读文件系统，所以本地验证不需要先搭 CDN，也不依赖服务器路径。

## 0. 前置

```bash
node -v          # 24.x（与 web/package-lock.json 匹配）
~/sdk/go/bin/go version   # Go 1.27；或保证 go 在 PATH
```

首次构建会自动 `npm ci`。

## 1. 构建并自检

```bash
cd /path/to/HomeCube
bash scripts/build-base.sh 1.0.0
```

成功时最后应看到：

```
PASS  base           v1.0.0  205732 bytes
PASS  bundle:finance v1.0.0  205732 bytes
✅ 清单与产物一致（2 项）
```

产物：

```
dist/releases/manifest.json
dist/releases/base/1.0.0/site/
dist/releases/base/1.0.0/homecube-base-1.0.0.zip
server/services/svc-homeos/internal/handler/bundle_manifest.json
```

构建断言（失败即停，不是警告）：`pages-finance-*.js` ≥ 1；purchase/diet/trip/kin/growth 在
构建产物里零 chunk（PRD 18.2#9）；zip 的 sha256/size 与清单一致。

## 2. 用本机静态服务模拟 nginx

P1 本机没有 nginx 时用 Python 自带的静态服务器即可；它不支持 `Cache-Control` 配置，缓存头验证
放到第 6 节按 nginx 规则核对。

```bash
# 以 dist/ 为根，这样清单里的 /releases/base/... 能原样访问
python3 -m http.server 8090 --directory dist
```

另开终端验证按清单 URL 能真的取到文件：

```bash
URL="$(node -p 'require("./dist/releases/manifest.json").base.url')"
SHA="$(node -p 'require("./dist/releases/manifest.json").base.sha256')"
curl -fsS "http://127.0.0.1:8090${URL}" -o /tmp/homecube-base.zip
shasum -a 256 /tmp/homecube-base.zip | awk '{print $1}'
echo "${SHA}"
```

两个哈希必须相同。`--directory dist` 让 URL `/releases/...` 对应 `dist/releases/...`，
与服务器上 `location /releases/` 的布局一致。

## 3. 校验器：正例与反例

正例：

```bash
bash scripts/verify-release.sh dist/releases/manifest.json dist/releases
```

反例必须真的失败（用临时副本，别改发布产物）：

```bash
# 3.1 字节数/哈希不符：复制一份发布根，截断 zip
rm -rf /tmp/hc-publish && cp -R dist/releases /tmp/hc-publish
printf 'x' >> /tmp/hc-publish/base/1.0.0/homecube-base-1.0.0.zip
bash scripts/verify-release.sh /tmp/hc-publish/manifest.json /tmp/hc-publish; echo "exit=$?"

# 3.2 文件缺失
rm /tmp/hc-publish/base/1.0.0/homecube-base-1.0.0.zip
bash scripts/verify-release.sh /tmp/hc-publish/manifest.json /tmp/hc-publish; echo "exit=$?"

# 3.3 清单里的 sha256 被改错
rm -rf /tmp/hc-publish && cp -R dist/releases /tmp/hc-publish
node -e '
const fs = require("fs");
const p = "/tmp/hc-publish/manifest.json";
const m = JSON.parse(fs.readFileSync(p, "utf8"));
m.base.sha256 = "0".repeat(64);
fs.writeFileSync(p, JSON.stringify(m, null, 2));
'
bash scripts/verify-release.sh /tmp/hc-publish/manifest.json /tmp/hc-publish; echo "exit=$?"

# 3.4 分包集合与 registry 不一致（删掉 finance 条目）
rm -rf /tmp/hc-publish && cp -R dist/releases /tmp/hc-publish
node -e '
const fs = require("fs");
const p = "/tmp/hc-publish/manifest.json";
const m = JSON.parse(fs.readFileSync(p, "utf8"));
m.bundles = [];
fs.writeFileSync(p, JSON.stringify(m, null, 2));
'
bash scripts/verify-release.sh /tmp/hc-publish/manifest.json /tmp/hc-publish; echo "exit=$?"
```

四个反例都应以 `exit=1` 结束，并打印具体失败项。

## 4. 服务端接口（不需要数据库）

两个接口的行为由单元测试覆盖，跑的是编译进二进制的那份清单：

```bash
cd server
GOTOOLCHAIN=local ~/sdk/go/bin/go test ./services/svc-homeos/internal/handler \
  -run 'Bundle|AppVersion|AppBundles' -v
```

覆盖点：

- 内嵌清单与 `registry.BundleSet()` 逐项相等；
- `codes=finance` 只返回 finance；`codes=finance,purchase` 忽略未出生的 purchase；
  `codes=homeos,base` 返回空；未知 code 返回空；`installed` 接收但不参与过滤；
- `/app/version` 的 JSON 恰好是 `current_version` / `update_notes` / `min_compat` 三个键；
- 清单被改坏时（版本非语义版本、sha 长度不对、url 穿越、code 未出生/未知/homeos/重复、
  分包集合与 registry 不一致）启动校验失败，两个接口都返回 503 `bundle_manifest_invalid`。

## 5. 真实服务联调（可选，需要 Postgres）

只有本机数据库可用时才需要这一步。启动 svc-homeos 的最小环境变量（值按本机实际替换）：

```bash
# DSN 是 key=value 形式（见 deploy/env.local.example:57），不是 URI
export HOMEOS_DSN='host=127.0.0.1 port=5432 user=hc_homeos password=<password> dbname=homecube search_path=homeos'
export UPLOAD_DIR='./uploads/{family_id}'
export HOMEOS_JWT_PRIVATE_KEY_PEM='<RS256 私钥文件路径>'
export HOMEOS_ADDR=':8080'

cd server
~/sdk/go/bin/go run ./services/svc-homeos/cmd/svc-homeos
```

注意：`HOMEOS_DSN` 的用户必须是 registry 登记的专属账号 `hc_homeos`（PRD 22.2 第 2 条禁止共用账号），
`UPLOAD_DIR` 必须是相对数据根的路径且最后一段是 `{family_id}`——启动校验会拒绝不合规的值。
`HOMEOS_JWT_PRIVATE_KEY_PEM` 是私钥**文件路径**；也可用 `HOMEOS_JWT_PRIVATE_KEY` 直接传 PEM 内容。

拿到登录 token 后：

```bash
TOKEN='<access_token>'

# 版本检查：三字段，无 family 数据
curl -s -H "Authorization: Bearer ${TOKEN}" http://127.0.0.1:8080/api/homeos/app/version | jq

# 分包清单：P1 只应有 finance
curl -s -H "Authorization: Bearer ${TOKEN}" \
  'http://127.0.0.1:8080/api/homeos/app/bundles?codes=finance' | jq

# 未出生面不在清单里
curl -s -H "Authorization: Bearer ${TOKEN}" \
  'http://127.0.0.1:8080/api/homeos/app/bundles?codes=purchase' | jq   # {"bundles":[]}
```

未带 token 的请求应被中间件拒绝（401），证明它不是免鉴权口（定版 ㉕）。

## 6. 缓存与清理

- 面分包的 URL 带版本号，内容不可变；nginx 可对 `/releases/base/{版本}/` 下发长期缓存
  （`Cache-Control: public, max-age=31536000, immutable`），版本升级即换 URL，不存在"旧缓存挡住新版本"。
- `manifest.json` 用 `no-cache`，它是给运维/校验用的，客户端不直接读它。
- 发新版本后，旧版本目录可以删除；删除前确认没有回滚需求。删除动作不带脚本（按产品取舍，
  不自动备份/回滚）：

```bash
# 服务器上；<旧版本> 换成要清理的版本
rm -rf <WEB_ROOT>/base/<旧版本>
bash scripts/verify-release.sh <WEB_ROOT>/manifest.json <WEB_ROOT>
```

- 浏览器侧：站点资源文件名带 content hash，`index.html` 不缓存或短缓存；若本地看到旧页面，
  硬刷新（macOS Chrome：`Cmd+Shift+R`）后应加载新 hash 的资源。

## 7. 覆盖清单（对照 PRD 17.8 P1 行）

| 要求 | 本仓现状 |
|---|---|
| 分包清单接口 | ✅ 内嵌清单 + `/app/bundles` 过滤；单元测试覆盖 |
| 版本接口与 `min_compat` 同源 | ✅ `/app/version` 读同一份清单；字段集合由测试钉住 |
| sha256/size 校验 | ✅ 构建期生成、`verify-release.sh` 逐字节校验；反例 3.1–3.3 |
| 清单与 registry 一致 | ✅ 构建断言 + 校验器 + 服务端启动校验（反例 3.4） |
| 失败回退 | ⚠️ 服务端 503 已实现；客户端加载器（下载→校验→失败保留旧版/显示「暂不可用」）属于前端 shell 的待建项，本仓 `web/src` 目前没有加载器 |
| 缓存清理 | ✅ 版本化 URL + nginx 缓存规则 + 旧目录删除步骤（第 6 节） |

## 8. 已知的历史证据

`server/test/acceptance/result-g1.json` 里 `/api/homeos/app/bundles` 与 `/app/version` 的记录
来自 2026-10-09 的占位实现（响应体里是 `/static/bundles/...` 与 `placeholder_sha256_*`），
与当前实现不符。P1 验收重跑时应重新生成该文件，不能把旧记录当作通过证据。

## 9. 清理

```bash
# 停止 python 静态服务器后
rm -rf /tmp/hc-publish /tmp/homecube-base.zip
# dist/ 已被 .gitignore 忽略，可保留供下次预览
```
