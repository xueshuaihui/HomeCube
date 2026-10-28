# scripts/

发版相关脚本只有两个，都只做本机可验证的事；上传与部署由人执行（仓库是公开的，
不放服务器地址与凭据）。

| 脚本 | 作用 | 会改什么 |
|---|---|---|
| `build-base.sh` | 构建 P1 的 H5 基座，生成唯一清单并自检 | `dist/releases/`、`server/services/svc-homeos/internal/handler/bundle_manifest.json` |
| `verify-release.sh` | 校验清单与真实产物逐项一致 | 只读 |

## build-base.sh

```bash
bash scripts/build-base.sh 1.0.0
MIN_COMPAT=2.0.0 RELEASE_NOTES="2.0.0：路由结构重构" bash scripts/build-base.sh 2.0.0
```

- 版本参数是语义版本 `x.y.z`，也接受 `base_vx.y.z`；标签由脚本派生，不手写。
- `MIN_COMPAT`（默认 `1.0.0`）是"客户端低于它必须更新"的最低版本，必须 `<=` 本次版本；
  普通发版保持默认，破坏性变更才提高。
- 前置：node/npm、Go 1.27（`PATH` 或 `~/sdk/go/bin/go`）、`zip`、`shasum`；缺一即失败。
- 失败即停：registry 读不到、H5 产物缺 `index.html`、finance chunk 为 0、任一本出生面出现 chunk、
  清单自检不通过，都不会产出"看起来成功"的结果。
- 产物：
  - `dist/releases/base/{版本}/site/`（静态站点）
  - `dist/releases/base/{版本}/homecube-base-{版本}.zip`（固定 mtime，可复现同哈希）
  - `dist/releases/manifest.json`（发布用）
  - `server/services/svc-homeos/internal/handler/bundle_manifest.json`（`go:embed` 用，内容与上一条逐字节相同）
- 清单里的分包集合来自 `server/packages/registry`（当期已出生面），脚本不自行维护面清单。

构建结束会打印手工发布命令，脚本本身不上传。

## verify-release.sh

```bash
# 本地
bash scripts/verify-release.sh dist/releases/manifest.json dist/releases

# 线上（把第二个参数换成服务器上 /releases/ 指向的目录）
bash scripts/verify-release.sh dist/releases/manifest.json /srv/homecube/releases
```

逐项检查并在任一失败时 `exit 1`：

1. `version` / `min_compat` 是 `x.y.z`；
2. 面条目 `code` 属于 `web/generated/domains.json` 的 `meta.bundles`；
3. `url` 以 `/releases/` 开头且不含 `..`；
4. `url` 指向的文件在发布根下存在；
5. 文件字节数等于清单 `size`；
6. 文件 sha256 等于清单 `sha256`；
7. 清单分包集合与 registry 当期分包集合完全一致。

## 不在这里的事

- 上传/发布：见 `docs/RELEASE-PROCESS.md` 第 5 节（`rsync` + nginx + 线上校验）。
- 回滚：按产品取舍只保留手工路径，不提供自动备份/回滚脚本。
- 独立面分包、CDN、灰度：P2 范围。
