#!/usr/bin/env bash
# scripts/build-base.sh — 构建 P1 的 H5 基座包，并生成唯一一份分包清单。
#
# 口径（PRD 17.8「客户端热插拔」P1 行）：P1 只产出一份 H5 构建（shell + 已出生面的分包都在里面），
# 财务分包随基座一起发布；清单接口、sha256 校验、失败回退与缓存清理走同一条代码路径。
# 因此本脚本不产出"独立面包"：清单里每个面条目指向承载它的基座包，P2 改为独立下发时由本脚本的
# 输出结构演进（届时每面一个包 + 各自的 sha256）。
#
# 与上一版的区别（2026-10-26 复核后重写）：
#   - 版本参数是语义版本 x.y.z，tag 由脚本派生（base_v{x.y.z}），不再混用；
#   - 不再静默降级：go / npm / 产物断言 / registry 校验任一失败即退出；
#   - 清单写到 dist/releases/manifest.json（发布用）与 svc-homeos 的 embed 文件（编译进二进制）；
#   - 上传不由脚本代做（本机/服务器路径与凭据不进公开仓），脚本末尾只打印可直接粘贴的命令。
#
# 用法：
#   bash scripts/build-base.sh 1.0.0
#   RELEASE_NOTES="1.0.1 修复对账" bash scripts/build-base.sh 1.0.1
set -euo pipefail

VERSION_IN="${1:-}"
if [ -z "$VERSION_IN" ]; then
    echo "用法: bash scripts/build-base.sh <x.y.z>   （也接受 base_vx.y.z）" >&2
    exit 2
fi

VERSION="${VERSION_IN#base_v}"
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "❌ 版本必须是语义版本 x.y.z（收到：$VERSION_IN）" >&2
    exit 2
fi
TAG="base_v${VERSION}"

# min_compat 是「客户端低于它就必须更新」的最低版本，不是当前版本：
# 默认 1.0.0 让普通发版可跳过更新，只有破坏性变更才显式提高。
# 每个面分包的 min_compat 另取「能载入它的最低基座版本」，P1 与基座同包，故等于本次版本。
MIN_COMPAT="${MIN_COMPAT:-1.0.0}"
if ! printf '%s' "$MIN_COMPAT" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "❌ MIN_COMPAT 必须是语义版本 x.y.z（收到：$MIN_COMPAT）" >&2
    exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB="$ROOT/web"
OUT="$ROOT/dist/releases"
SITE_OUT="$OUT/base/$VERSION/site"
ZIP_OUT="$OUT/base/$VERSION/homecube-base-$VERSION.zip"
MANIFEST_OUT="$OUT/manifest.json"
# 该文件由 go:embed 编译进 svc-homeos；每次构建必须与产物同步，否则接口会发布错的哈希。
MANIFEST_EMBED="$ROOT/server/services/svc-homeos/internal/handler/bundle_manifest.json"

echo "🏗️  HomeCube 基座构建：$TAG"
echo "    产物根：$OUT"

# ---- 前置条件：缺什么就停，不再"跳过继续" ----------------------------------
echo ""
echo "1/6 检查前置条件"
NODE_BIN="$(command -v node || true)"
NPM_BIN="$(command -v npm || true)"
[ -n "$NODE_BIN" ] || { echo "❌ 找不到 node" >&2; exit 2; }
[ -n "$NPM_BIN" ] || { echo "❌ 找不到 npm" >&2; exit 2; }

GO_BIN="${GO:-}"
if [ -z "$GO_BIN" ]; then
    if command -v go >/dev/null 2>&1; then
        GO_BIN="$(command -v go)"
    elif [ -x "$HOME/sdk/go/bin/go" ]; then
        GO_BIN="$HOME/sdk/go/bin/go"
    else
        echo "❌ 找不到 go（可用 GO=/path/to/go 指定；本仓 Makefile 默认 ~/sdk/go/bin/go）" >&2
        exit 2
    fi
fi
echo "    node: $NODE_BIN"
echo "    go:   $GO_BIN"

# MIN_COMPAT 不能高于本次版本；相等是允许的（本次即最低兼容版本）。
if ! "$NODE_BIN" -e '
const [v, m] = process.argv.slice(1).map((s) => s.split(".").map(Number));
const cmp = v[0] - m[0] || v[1] - m[1] || v[2] - m[2];
process.exit(cmp >= 0 ? 0 : 1);
' "$VERSION" "$MIN_COMPAT"; then
    echo "❌ MIN_COMPAT=$MIN_COMPAT 高于本次版本 $VERSION" >&2
    exit 2
fi
echo "    min_compat: $MIN_COMPAT"

# ---- registry 是唯一真源：当前期已实建面 = 本次允许进清单的面 ------------------
echo ""
echo "2/6 读取 registry（当前期已实建面）"
# 域表 JSON 是前端与 verify-release.sh 的共同输入；每次构建从 registry 重新导出，
# 避免 registry 已换期而 generated/domains.json 仍是上一期（那会让校验器用旧的分包集合）。
mkdir -p "$WEB/generated"
(cd "$ROOT/server/packages/registry/cmd/registryd" && GOTOOLCHAIN=local "$GO_BIN" run main.go --format=json) > "$WEB/generated/domains.json"
REGISTRY_META="$(cd "$ROOT/server/packages/registry/cmd/registryd" && GOTOOLCHAIN=local "$GO_BIN" run main.go --format=meta)"
BUNDLE_CODES="$(printf '%s' "$REGISTRY_META" | "$NODE_BIN" -e '
let s = ""
process.stdin.on("data", d => (s += d)).on("end", () => {
  const m = JSON.parse(s)
  if (!Array.isArray(m.bundles) || m.bundles.length === 0) {
    console.error("registry 没有已出生的面")
    process.exit(1)
  }
  process.stdout.write(JSON.stringify(m.bundles))
})')"
echo "    已出生面（分包）：$BUNDLE_CODES"

# ---- 构建 H5 ----------------------------------------------------------------
echo ""
echo "3/6 构建 H5（npm run build:h5）"
cd "$WEB"
if [ ! -d node_modules ]; then
    echo "    安装依赖（npm ci）"
    npm ci
fi
npm run build:h5

H5_DIR="$WEB/dist/build/h5"
[ -f "$H5_DIR/index.html" ] || { echo "❌ 构建产物缺失：$H5_DIR/index.html" >&2; exit 1; }

FIN_CHUNKS="$(find "$H5_DIR/assets" -maxdepth 1 -name 'pages-finance-*.js' | wc -l | tr -d ' ')"
[ "$FIN_CHUNKS" -ge 1 ] || { echo "❌ 构建产物里没有 finance 分包 chunk（分包未被构建）" >&2; exit 1; }
for code in purchase diet trip kin growth; do
    stray="$(find "$H5_DIR/assets" -maxdepth 1 -name "pages-${code}-*" -print -quit)"
    if [ -n "$stray" ]; then
        echo "❌ 未出生面 $code 出现在构建产物里（PRD 18.2#9 要求零分包）：$stray" >&2
        exit 1
    fi
done
echo "    finance chunk: $FIN_CHUNKS 个；未出生面：0 个"

# ---- 归档 ------------------------------------------------------------------
echo ""
echo "4/6 归档到 $OUT/base/$VERSION"
rm -rf "$OUT/base/$VERSION"
mkdir -p "$SITE_OUT"
cp -R "$H5_DIR/." "$SITE_OUT/"
# 固定 mtime 再打包：zip 会写入文件时间，不归一化则同样的源码每次产出的 sha256 都不同，
# 清单哈希就失去"可复核"的意义（也挡住了 CI 重跑与本地构建的比对）。
find "$SITE_OUT" -exec touch -t 198001010000 {} +
( cd "$SITE_OUT" && zip -X -q -r "$ZIP_OUT" . )

if command -v shasum >/dev/null 2>&1; then
    SHA256="$(shasum -a 256 "$ZIP_OUT" | awk '{print $1}')"
else
    SHA256="$(sha256sum "$ZIP_OUT" | awk '{print $1}')"
fi
SIZE="$(wc -c < "$ZIP_OUT" | tr -d ' ')"
echo "    $ZIP_OUT"
echo "    sha256=$SHA256  size=$SIZE"

# ---- 清单：dist 发布副本 + svc-homeos embed 副本，内容必须逐字节一致 ----------
echo ""
echo "5/6 生成分包清单"
BASE_URL="/releases/base/$VERSION/homecube-base-$VERSION.zip"
RELEASE_NOTES="${RELEASE_NOTES:-P1：HomeOS 基座 + 财务管理面}"
VERSION="$VERSION" \
MIN_COMPAT="$MIN_COMPAT" \
BASE_URL="$BASE_URL" \
SHA256="$SHA256" \
SIZE="$SIZE" \
RELEASE_NOTES="$RELEASE_NOTES" \
BUNDLE_CODES="$BUNDLE_CODES" \
MANIFEST_OUT="$MANIFEST_OUT" \
MANIFEST_EMBED="$MANIFEST_EMBED" \
"$NODE_BIN" --input-type=module <<'JS'
import { writeFileSync } from 'node:fs'

const { VERSION, MIN_COMPAT, BASE_URL, SHA256, SIZE, RELEASE_NOTES, BUNDLE_CODES, MANIFEST_OUT, MANIFEST_EMBED } = process.env
const byteSize = Number(SIZE)

const base = {
  version: VERSION,
  url: BASE_URL,
  sha256: SHA256,
  size: byteSize,
  update_notes: RELEASE_NOTES,
  // 客户端低于 min_compat 才强制更新（PRD 12.4）；未显式提高时普通发版可跳过。
  min_compat: MIN_COMPAT,
}

// P1：面与基座同版发布（PRD 17.8 P1 行），面条目的 url/sha256/size 指向承载它的基座包；
// P2 独立下发时这里改为每面一个包，接口字段不变。
const bundles = JSON.parse(BUNDLE_CODES).map((code) => ({
  code,
  version: VERSION,
  url: BASE_URL,
  sha256: SHA256,
  size: byteSize,
  // 面分包的 min_compat 含义是「能载入它的最低基座版本」（PRD 17.8），
  // P1 与基座同包发布，故等于本次基座版本。
  min_compat: VERSION,
}))

const doc = { generated_at: new Date().toISOString(), base, bundles }
const text = JSON.stringify(doc, null, 2) + '\n'
writeFileSync(MANIFEST_OUT, text)
writeFileSync(MANIFEST_EMBED, text)
JS

# ---- 自检：清单里的每个 url 都必须能在发布根下找到，且大小/哈希一致 -----------
echo ""
echo "6/6 自检清单与产物"
bash "$ROOT/scripts/verify-release.sh" "$MANIFEST_OUT" "$OUT"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ 构建完成：$TAG"
echo "   清单：$MANIFEST_OUT"
echo "   embed：$MANIFEST_EMBED（随 svc-homeos 一起编译）"
echo ""
echo "发布（由你手动执行；把 <WEB_ROOT> 换成服务器上 /releases/ 指向的目录）："
echo "   rsync -av --delete $OUT/ <user>@<host>:<WEB_ROOT>/"
echo "   # nginx: location /releases/ { root <含 releases 的父目录>; }"
echo ""
echo "验证线上："
echo "   bash scripts/verify-release.sh $MANIFEST_OUT <服务器上的发布根>"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
