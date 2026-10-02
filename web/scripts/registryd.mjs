// registryd 调用口：web 侧唯一允许读取域表的地方。
//
// 为什么走命令而不是 import 一份常量：docs/p1-tech-plan.md §1.3 把「前端构建脚本」列为
// registry.Domains() 的四个消费者之一，而 PRD 22.5 第 4 道要求分包集合按
// 「出生期 ≤ 当期 − homeos」判定 —— 当期号与派生集合只存在于 Go 那张表里
// （registryd 文件头即为此写明 --format=meta/--format=bundles 的由来）。
// 所以 web 侧一律现场取，不落一份手抄 code 清单。
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const WEB_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..')
export const REPO_DIR = resolve(WEB_DIR, '..')
// 默认按 monorepo 布局取 ../server（PRD 22.4）；CI 里 web 与 server 分开放时用它覆盖。
export const SERVER_DIR = process.env.HC_SERVER_DIR || join(REPO_DIR, 'server')

const FORMATS = new Set(['json', 'codes', 'bundles', 'meta'])

function goCmd() {
  const fromEnv = process.env.HC_GO
  if (fromEnv) return fromEnv
  const sdk = join(homedir(), 'sdk', 'go', 'bin', 'go')
  return existsSync(sdk) ? sdk : 'go'
}

/** 取 registryd 的一种导出（返回 stdout 字符串）。 */
export function registryExport(format) {
  if (!FORMATS.has(format)) throw new Error(`registryd 不支持的 format: ${format}`)
  const prebuilt = process.env.HC_REGISTRYD_BIN
  const cmd = prebuilt || goCmd()
  const args = prebuilt ? [`--format=${format}`] : ['-C', SERVER_DIR, 'run', './packages/registry/cmd/registryd', `--format=${format}`]
  try {
    return execFileSync(cmd, args, {
      encoding: 'utf8',
      env: { ...process.env, GOTOOLCHAIN: 'local' },
      maxBuffer: 8 * 1024 * 1024
    })
  } catch (err) {
    const detail = (err.stderr || err.message || '').toString().trim()
    throw new Error(
      `调用 registryd 失败（--format=${format}）：${cmd} ${args.join(' ')}\n${detail}\n` +
        `可用 HC_GO=/path/to/go 指定 go，或 HC_REGISTRYD_BIN=/path/to/registryd 指定预构建二进制。`
    )
  }
}

/** --format=meta：currentPhase / implemented / unborn / bundles / bundleRoots。 */
export function registryMeta() {
  return JSON.parse(registryExport('meta'))
}

/** --format=codes：registry 登记顺序的全部 code（七处命名域的第一处即由它给出）。 */
export function registryCodes() {
  return registryExport('codes')
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
}

/** 生成物与比对共用的同一形状：先取 live，再落盘或断言。 */
export function registrySnapshot() {
  return { codes: registryCodes(), meta: registryMeta() }
}
