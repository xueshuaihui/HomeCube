#!/usr/bin/env bash
# scripts/verify-release.sh — 校验发布清单与真实产物逐项一致。
#
# 校验项（任一失败即 exit 1）：
#   1. 版本字段是语义版本 x.y.z；
#   2. 面条目 code 全部属于当前期已出生面（对照 web/generated/domains.json 的 meta.bundles）；
#   3. 每个条目 url 以 /releases/ 开头且不含 ..；
#   4. url 指向的文件在发布根下存在；
#   5. 文件字节数与清单 size 一致；
#   6. 文件 sha256 与清单 sha256 一致；
#   7. 清单的分包集合与 registry 当前期分包集合（domains.json meta.bundles）完全一致。
#
# 用法：
#   bash scripts/verify-release.sh [manifest] [publish-root]
#   默认 manifest = dist/releases/manifest.json，publish-root = manifest 所在目录
#   线上校验时把 publish-root 换成服务器上 /releases/ 指向的目录。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"
MANIFEST="${1:-$ROOT/dist/releases/manifest.json}"
PUBLISH_ROOT="${2:-$(dirname "$MANIFEST")}"

[ -f "$MANIFEST" ] || { echo "❌ 清单不存在：$MANIFEST" >&2; exit 2; }
[ -d "$PUBLISH_ROOT" ] || { echo "❌ 发布根不存在：$PUBLISH_ROOT" >&2; exit 2; }
command -v node >/dev/null 2>&1 || { echo "❌ 找不到 node" >&2; exit 2; }

MANIFEST="$MANIFEST" PUBLISH_ROOT="$PUBLISH_ROOT" DOMAINS="$ROOT/web/generated/domains.json" node --input-type=module <<'JS'
import { createHash } from 'node:crypto'
import { readFileSync, statSync, existsSync } from 'node:fs'
import { join } from 'node:path'

const { MANIFEST, PUBLISH_ROOT, DOMAINS } = process.env
const fail = []
const rows = []

const semver = /^\d+\.\d+\.\d+$/
const manifest = JSON.parse(readFileSync(MANIFEST, 'utf8'))
const domains = existsSync(DOMAINS) ? JSON.parse(readFileSync(DOMAINS, 'utf8')) : null
const born = new Set(domains?.meta?.bundles ?? [])

const check = (label, entry) => {
  const problems = []
  if (!semver.test(entry.version ?? '')) problems.push(`version 不是 x.y.z：${entry.version}`)
  if (!semver.test(entry.min_compat ?? '')) problems.push(`min_compat 不是 x.y.z：${entry.min_compat}`)
  if (!/^\/releases\//.test(entry.url ?? '')) problems.push(`url 未落在 /releases/ 下：${entry.url}`)
  if (String(entry.url ?? '').includes('..')) problems.push('url 含 ..')
  if (domains && entry.code && !born.has(entry.code)) problems.push(`code 不是当前期已出生面：${entry.code}`)

  const rel = String(entry.url ?? '').replace(/^\/releases\//, '')
  const path = join(PUBLISH_ROOT, rel)
  if (!existsSync(path)) {
    problems.push(`文件不存在：${path}`)
  } else {
    const size = statSync(path).size
    if (size !== entry.size) problems.push(`size 不一致：清单 ${entry.size}，实际 ${size}`)
    const sha = createHash('sha256').update(readFileSync(path)).digest('hex')
    if (sha !== entry.sha256) problems.push(`sha256 不一致：清单 ${entry.sha256}，实际 ${sha}`)
  }

  rows.push({ label, version: entry.version, size: entry.size, status: problems.length ? 'FAIL' : 'PASS', problems })
  if (problems.length) fail.push(...problems.map((p) => `${label}: ${p}`))
}

check('base', manifest.base ?? {})
for (const b of manifest.bundles ?? []) check(`bundle:${b.code}`, b)

// 分包集合必须与 registry 导出的 meta.bundles 完全一致：漏一面会让客户端以为该面"没有分包"，
// 多一面则指向不存在的产物。两者都是构建期错误，在发布前拦下。
if (domains) {
  const want = [...(domains?.meta?.bundles ?? [])].sort()
  const got = [...(manifest.bundles ?? []).map((b) => b.code)].sort()
  if (want.join(',') !== got.join(',')) {
    const p = `分包集合与 registry 不一致：清单 [${got.join(',')}]，registry [${want.join(',')}]`
    fail.push(p)
    rows.push({ label: 'bundle-set', version: '-', size: '-', status: 'FAIL', problems: [p] })
  }
}

for (const r of rows) {
  console.log(`${r.status}  ${r.label.padEnd(14)} v${r.version}  ${r.size} bytes`)
  for (const p of r.problems) console.log(`      - ${p}`)
}

if (fail.length) {
  console.error(`\n❌ 校验失败：${fail.length} 项`)
  process.exit(1)
}
console.log(`\n✅ 清单与产物一致（${rows.length} 项）`)
JS
