// pages.json 三查 + 七条前端同源检查（PRD 22.5 第 4 道 / docs/p1-tech-plan.md §10.2 第 4 道 的前端项）
//
// 判据原文：
//   §2.4「CI 对 pages.json 做三查」：① 无 tabBar 字段；② 分包集合 = 已出生域 − homeos；
//   ③ 每条路由三段式、action ∈ §2.1 封闭动作表、无第四段。
//   §2.4 末段「七条前端同源检查」+ §1.2 区域所有权表的「校验点」列。
//   §2.1 动作段封闭表：五个通用动词 + 十四个具名动作段，**路径恒为三段**。
//
// 纪律：本脚本不接受任何放宽开关 —— 无 --force、无告警模式、无白名单特例（禁令 1、3）。
// 任一检查命中即以非零码退出；S1-E 把它接进 CI 时不需要再改语义。
//
// 域信息一律现场取自 registryd（见 scripts/registryd.mjs），脚本内不出现 code 清单。
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { isAbsolute, basename, dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { registrySnapshot, WEB_DIR } from './registryd.mjs'

const SRC = join(WEB_DIR, 'src')
const PAGES_JSON = join(SRC, 'pages.json')
const GENERATED_DIR = join(WEB_DIR, 'generated')
const GENERATED_JSON = join(WEB_DIR, 'generated', 'domains.json')
const GENERATED_IMPORT = 'generated/domains.json'

// §2.1 封闭动作表（原文抄自 docs/p1-page-structure-navigation.md §2.1 两行）。
const ACTION_GENERIC = ['index', 'list', 'detail', 'create', 'edit']
const ACTION_NAMED = [
  'login',
  'family-create',
  'family-join',
  'modules',
  'pin-set',
  'board',
  'invite',
  'permission',
  'security',
  'trash',
  'export',
  'archive',
  'sort',
  'settlement'
]
const CLOSED_ACTIONS = new Set([...ACTION_GENERIC, ...ACTION_NAMED])
const MAIN_PACKAGE_CODE = 'homeos' // §2.4：主包 = shell + HomeOS 全部页面，没有 pages/homeos 分包

const failures = []
const passes = []
const notMeasured = []
let current = ''

function check(label, fn) {
  current = label
  const beforeF = failures.length
  const beforeN = notMeasured.length
  fn()
  // A check counts as PASS only if it found nothing wrong AND was not declared "nothing to
  // inspect this phase". The notMeasured branch is the honest answer to a check whose scope is
  // empty by document: it must NOT print a tick (that is the "scanned nothing → passed" false
  // green this card removes); it is reported separately and never counted toward the pass total.
  if (failures.length === beforeF && notMeasured.length === beforeN) passes.push(label)
}

function fail(msg) {
  failures.push(`[${current}] ${msg}`)
}

function markNotMeasured(msg) {
  notMeasured.push(`[${current}] ${msg}`)
}

// ---------------------------------------------------------------- 工具
function readText(file) {
  return readFileSync(file, 'utf8')
}
function listFiles(dir, exts) {
  const out = []
  if (!existsSync(dir)) return out
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...listFiles(p, exts))
    else if (exts.some((e) => name.endsWith(e))) out.push(p)
  }
  return out
}
function listDirs(dir) {
  if (!existsSync(dir)) return []
  return readdirSync(dir).filter((n) => statSync(join(dir, n)).isDirectory())
}
function lineOf(text, needle) {
  const idx = typeof needle === 'number' ? needle : text.indexOf(needle)
  if (idx < 0) return 0
  return text.slice(0, idx).split('\n').length
}
/** 去注释后再扫描，避免把文档引用式注释里的路径/色值当成代码命中。 */
function stripComments(code) {
  let out = ''
  let i = 0
  const n = code.length
  while (i < n) {
    const c = code[i]
    const d = code[i + 1]
    if (c === '/' && d === '/') {
      while (i < n && code[i] !== '\n') i++
      continue
    }
    if (c === '/' && d === '*') {
      i += 2
      while (i < n && !(code[i] === '*' && code[i + 1] === '/')) i++
      i += 2
      out += '\n'
      continue
    }
    if (c === '<' && code.startsWith('<!--', i)) {
      const end = code.indexOf('-->', i)
      i = end < 0 ? n : end + 3
      out += '\n'
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      out += c
      i++
      while (i < n) {
        if (code[i] === '\\') {
          out += code[i] + (code[i + 1] ?? '')
          i += 2
          continue
        }
        if (code[i] === c) {
          out += code[i]
          i++
          break
        }
        out += code[i++]
      }
      continue
    }
    out += c
    i++
  }
  return out
}
function matches(re, text) {
  const out = []
  for (const m of text.matchAll(re)) out.push(m)
  return out
}

/**
 * 提取一个模块里所有「被 import/require 的说明符」（静态 import from、副作用 import、
 * export ... from、动态 import()、require()）。用真实解析而不是单一路径正则，堵掉
 * 无扩展名（'../../../../generated/domains'）与 ./、@/ 别名等写法（S1-F 自定的纪律：
 * 面集合的客户端唯一来源是服务端 faces[]，generated/ 是构建期快照，不得进运行时）。
 */
function moduleSpecifiers(text) {
  const specs = new Set()
  const patterns = [
    /(?:^|[\s;}])(?:import|export)\b[^'";]*?\bfrom\s*['"]([^'"]+)['"]/g, // import x from '...' / export * from '...'
    /(?:^|[\s;}])import\s+['"]([^'"]+)['"]/g, // 副作用 import '...'
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g, // 动态 import('...')
    /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g // require('...')
  ]
  for (const re of patterns) {
    for (const m of matches(re, text)) specs.add(m[1])
  }
  return [...specs]
}

/**
 * 把说明符解析成绝对路径，判断它是否落在构建期生成物目录内。解析口径：
 *   - './' / '../' 相对 → 相对文件所在目录解析；
 *   - '@/' → 相对 SRC 解析（工程 tsconfig 的 baseUrl 别名）；
 *   - 其余（bare module，如 'vue-i18n'）不可能是 web/generated 下的快照，跳过。
 * 只要解析结果等于生成物目录本体、或落在其子路径内，即视为引用了那份构建期生成物 ——
 * 与带不带 .json 扩展名无关（真实打包器会补扩展名，判据也按目录归属而不是字符串）。
 */
function resolvesIntoGenerated(file, spec) {
  let abs = null
  if (spec.startsWith('./') || spec.startsWith('../')) abs = resolve(dirname(file), spec)
  else if (spec.startsWith('@/')) abs = resolve(SRC, spec.slice(2))
  else return null
  const rel = relative(GENERATED_DIR, abs)
  const inside = rel === '' || (!rel.startsWith('..') && !isAbsolute(rel))
  return inside ? abs : null
}

/** 递归收集一个 JSON 对象的所有键名（判断 i18n 里是否把未出生域的 code 写成了命名空间键）。 */
function jsonKeys(node, acc = []) {
  if (Array.isArray(node)) {
    for (const v of node) jsonKeys(v, acc)
  } else if (node && typeof node === 'object') {
    for (const [k, v] of Object.entries(node)) {
      acc.push(k)
      jsonKeys(v, acc)
    }
  }
  return acc
}

// ---------------------------------------------------------------- 取数
let pagesDoc
try {
  pagesDoc = JSON.parse(readText(PAGES_JSON))
} catch (e) {
  console.error(`check:pages 无法解析 ${relative(WEB_DIR, PAGES_JSON)}：${e.message}`)
  process.exit(1)
}

let live
try {
  live = registrySnapshot()
} catch (e) {
  console.error(`check:pages ${e.message}`)
  process.exit(1)
}
const meta = live.meta
const allCodes = live.codes
const expectedRoots = meta.bundleRoots
const codesOfExpected = expectedRoots.map((r) => r.split('/').pop())

const mainRoutes = (pagesDoc.pages ?? []).map((p) => p.path)
const subPkgs = pagesDoc.subPackages ?? []
const roots = subPkgs.map((s) => s.root)
const routesOfSub = subPkgs.flatMap((s) => (s.pages ?? []).map((p) => `${s.root}/${p.path}`))
const allRoutes = [...mainRoutes, ...routesOfSub]

// ---------------------------------------------------------------- 三查
check('三查① pages.json 无 tabBar 字段', () => {
  const raw = readText(PAGES_JSON)
  if (Object.prototype.hasOwnProperty.call(pagesDoc, 'tabBar')) {
    fail(`pages.json 顶层出现 tabBar 字段（§2.4 第 1 查、§1.3 第 2 条：一级五项由 shell 组件承载，不是路由）`)
  }
  const hits = matches(/"tabBar"\s*:/g, raw)
  if (hits.length) {
    fail(`pages.json 第 ${lineOf(raw, hits[0].index)} 行出现 "tabBar" 键（§2.4 第 1 查）`)
  }
})

check('三查② 分包集合 = 已出生域 − homeos', () => {
  const expectedSet = new Set(expectedRoots)
  const actualSet = new Set(roots)
  const missing = expectedRoots.filter((r) => !actualSet.has(r))
  const extra = roots.filter((r) => !expectedSet.has(r))
  if (missing.length) fail(`registryd(${meta.currentPhase}) 要求的分包缺失：${missing.join(', ')}（少一个即失败）`)
  if (extra.length) fail(`pages.json 出现 registry 未要求本期为分包的 root：${extra.join(', ')}（多一个即失败）`)
  for (const s of subPkgs) {
    if (!s.root) fail('subPackages[].root 不得为空')
    if (!Array.isArray(s.pages) || s.pages.length === 0) fail(`分包 ${s.root} 里一条页面都没有：不建空分包（17.8、18.2#9）`)
  }
  const dupRoots = roots.filter((r, i) => roots.indexOf(r) !== i)
  if (dupRoots.length) fail(`subPackages[].root 重复：${[...new Set(dupRoots)].join(', ')}`)

  // 22.5 第 4 道原文：HomeOS 的页面在主包，不存在 pages/homeos 这个分包。
  if (roots.includes(`pages/${MAIN_PACKAGE_CODE}`)) {
    fail(`出现 pages/${MAIN_PACKAGE_CODE} 分包（主包页面不得注册为分包，§2.4 第 2 查）`)
  }
  for (const code of meta.unborn) {
    if (roots.includes(`pages/${code}`)) fail(`未出生域 ${code}（出生期 > ${meta.currentPhase}）不得建分包（17.8、18.2#9）`)
  }
  // 工程目录里也不许出现未出生域的空分包：不建空目录是 17.7 第 4 条与 22.5 第 2/4 道的同一判据。
  const pageDirs = listDirs(join(SRC, 'pages'))
  const allowedDirs = new Set([MAIN_PACKAGE_CODE, ...codesOfExpected])
  for (const dir of pageDirs) {
    if (!allowedDirs.has(dir)) {
      const why = meta.unborn.includes(dir) ? '（未出生域）' : '（不在 registry 的 bundle 集合里）'
      fail(`src/pages/${dir} 目录存在但没有对应的分包注册${why}：不建空分包`)
    }
  }
  for (const code of meta.implemented) {
    if (code === MAIN_PACKAGE_CODE) continue
    if (!pageDirs.includes(code)) fail(`已出生域 ${code} 应有 src/pages/${code} 分包目录，实际不存在`)
  }
})

check('三查③ 路由全部三段式且 action ∈ 封闭动作表', () => {
  const seen = new Map()
  for (const route of allRoutes) {
    // 「三段式」数的是 pages/ 之后的语义段 {code}/{feature}/{action}（§2.1 命名表），
    // 所以 pages/homeos/home/index 是三段；pages/finance/tx/detail/attachment 是四段，必须失败。
    const tokens = route.split('/')
    const segs = tokens[0] === 'pages' ? tokens.slice(1) : null
    if (!segs || segs.length !== 3 || segs.some((s) => !s)) {
      fail(
        `路由 ${route} 不是三段式 pages/{code}/{feature}/{action}` +
          (segs && segs.length > 3 ? `（pages/ 之后出现 ${segs.length} 段，即第四段；§2.1「路径恒为三段，不得出现第四段」）` : '')
      )
      continue
    }
    const [code, feature, action] = segs
    if (!CLOSED_ACTIONS.has(action)) {
      fail(`路由 ${route} 的 action「${action}」不在封闭动作表里（§2.1：${ACTION_GENERIC.join('|')} + ${ACTION_NAMED.join('|')}）`)
    }
    if (!feature || feature === action) fail(`路由 ${route} 的 feature 段不合法`)
    if (mainRoutes.includes(route)) {
      if (code !== MAIN_PACKAGE_CODE) fail(`主包路由 ${route} 的 code 段必须是 ${MAIN_PACKAGE_CODE}（§2.4：主包 = shell + HomeOS 页面）`)
    } else {
      const owner = roots.find((r) => route.startsWith(`${r}/`))
      if (!owner) {
        fail(`路由 ${route} 不属于任何已登记的 subPackages[].root`)
        continue
      }
      const ownerCode = owner.split('/').pop()
      if (code !== ownerCode) fail(`分包路由 ${route} 的 code 段 ${code} 与所属分包 ${owner} 不一致`)
    }
    if (seen.has(route)) fail(`路由 ${route} 重复登记（同目录内不得出现两个页面承担同一动作，§2.4 第 3 查）`)
    seen.set(route, true)
  }
})

check('路由 ↔ 页面文件一一对应（登记的路由必须有页面文件，存在的页面文件必须登记）', () => {
  for (const route of allRoutes) {
    const file = join(SRC, `${route}.vue`)
    if (!existsSync(file)) fail(`路由 ${route} 没有对应的 src/${route}.vue`)
  }
  const vueFiles = listFiles(join(SRC, 'pages'), ['.vue'])
  for (const f of vueFiles) {
    const route = relative(join(SRC, 'pages'), f).replace(/\.vue$/, '').split(sep).join('/')
    const full = `pages/${route}`
    if (!allRoutes.includes(full)) fail(`${relative(WEB_DIR, f)} 存在但未登记为路由（未登记的页面即 §1.3 第 1 条之外的页面）`)
  }
})

check('生成物纪律：generated/domains.json 与 registryd 当前输出逐值一致，且不进运行时', () => {
  if (!existsSync(GENERATED_JSON)) {
    fail(`缺少 ${relative(WEB_DIR, GENERATED_JSON)}：先跑 npm run gen:domains（本卡选的纪律是「提交 + 断言逐值一致」）`)
    return
  }
  const snap = JSON.parse(readText(GENERATED_JSON))
  if (JSON.stringify(snap) !== JSON.stringify({ codes: allCodes, meta })) {
    fail(
      `${relative(WEB_DIR, GENERATED_JSON)} 与 registryd 当前输出不一致：` +
        `生成物=${JSON.stringify(snap)}\n        live  =${JSON.stringify({ codes: allCodes, meta })}\n` +
        `        registry 变更后必须重跑 npm run gen:domains 并提交`
    )
  }
  // 构建期快照不得进运行时：面集合在客户端的唯一来源是服务端 faces[]（§1.3 第 6 条、17.8）。
  const runtimeFiles = listFiles(SRC, ['.ts', '.vue', '.js', '.json'])
  for (const f of runtimeFiles) {
    const text = stripComments(readText(f))
    // (1) 文本里出现那份快照的路径字面量（含 readFileSync / glob 之类非 import 的引用）。
    if (text.includes(GENERATED_IMPORT)) {
      fail(`${relative(WEB_DIR, f)} 引用了构建期生成物 ${GENERATED_IMPORT}（分包集合不得进客户端运行时）`)
    }
    // (2) 真实解析 import/require 说明符：只要解析后落在 web/generated 目录内即违规，
    //     堵掉 '../../../../generated/domains'（无扩展名）这类逃离子串匹配写法。
    for (const spec of moduleSpecifiers(text)) {
      const hit = resolvesIntoGenerated(f, spec)
      if (hit) {
        fail(
          `${relative(WEB_DIR, f)} 以 import/require 引用了构建期生成物：说明符「${spec}」解析到 ${relative(WEB_DIR, hit)}` +
            `（落在 ${relative(WEB_DIR, GENERATED_DIR)}/ 内）。面集合的客户端唯一来源是服务端 faces[]，运行时代码不得读这份快照（S1-F 纪律、§1.3 第 6 条）。`
        )
      }
    }
  }
})

// ---------------------------------------------------------------- 未出生域不得出现在任何前端资源
// 域取值来自 registryd live 的 meta.unborn（与 web/generated/domains.json 的 meta.unborn 逐值一致，
// 上方「生成物纪律」已断言两者相等，不在脚本里手抄 code 清单）。未出生域 = 出生期 > 当期
// （purchase/diet/trip/kin/growth 在 P1），它们的任何前端痕迹都属「提前创建」（PRD 11.7 第 1 条、
// 定版 E ★、§2.4 第 2 查）。扫三类资源：i18n 文件名/键、pages 目录、路由/接口串。
check('未出生域不得出现在任何前端资源（i18n 文件名/键、pages 目录、路由串；取自 domains.json 的 unborn）', () => {
  const unborn = meta.unborn
  if (unborn.length === 0) {
    markNotMeasured(`本期无未出生域（registryd unborn 为空），本项当期无可检物，不计入通过`)
    return
  }
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

  // (a) i18n 文件名与键：src/i18n/<locale>/<code>.json 的 <code> 即命名空间（§九 i18n 行）。
  //     未出生域要么有同名语言包，要么把它的 code 写成了别的语言包里的键 —— 两者都拦。
  for (const f of listFiles(join(SRC, 'i18n'), ['.json'])) {
    const raw = readText(f)
    const stem = basename(f).replace(/\.json$/, '')
    if (unborn.includes(stem)) {
      fail(`${relative(WEB_DIR, f)} 是未出生域「${stem}」的语言资源：出生期 > ${meta.currentPhase} 的面不得有前端资源（17.8、§2.4）`)
    }
    try {
      for (const key of jsonKeys(JSON.parse(raw))) {
        if (unborn.includes(key)) fail(`${relative(WEB_DIR, f)} 的 i18n 键「${key}」是未出生域 code（键名即命名空间，不得提前出现）`)
      }
    } catch (e) {
      fail(`${relative(WEB_DIR, f)} 不是合法 JSON：${e.message}`)
    }
  }

  // (b) pages 目录：src/pages/{unborn} 不得在磁盘上存在（与 registry_fs_test 的同一条判据，前端侧对齐）。
  for (const dir of listDirs(join(SRC, 'pages'))) {
    if (unborn.includes(dir)) {
      fail(`${relative(WEB_DIR, join(SRC, 'pages', dir))} 是未出生域「${dir}」的分包目录：不得提前建（17.7 第 1 条、§2.4 第 2 查）`)
    }
  }

  // (c) 路由/接口串：源码里以路径形态出现未出生域 —— pages/{code} 或 /api/{code} 之后紧跟分隔符或串尾。
  //     按路径段匹配而不是全文找单词，避免把 diet/trip/growth 这类英文词误判（只在 pages/ 或 /api/ 段命中）。
  for (const f of listFiles(SRC, ['.ts', '.vue', '.js', '.json'])) {
    const raw = readText(f)
    const text = stripComments(raw)
    for (const code of unborn) {
      for (const re of [new RegExp(`pages/${esc(code)}(?=[/'"\`]|$)`, 'g'), new RegExp(`/api/${esc(code)}(?=[/'"\`?]|$)`, 'g')]) {
        for (const m of matches(re, text)) {
          fail(`${relative(WEB_DIR, f)}:${lineOf(raw, m[0])} 路由/接口串里出现未出生域「${code}」的 ${m[0]}（未出生域不得在任何前端资源里出现，17.8、§2.4）`)
        }
      }
    }
  }
})

// ---------------------------------------------------------------- 七条同源检查
const subPkgFiles = subPkgs.flatMap((s) => listFiles(join(SRC, s.root), ['.vue', '.ts', '.js', '.json']).map((f) => ({ root: s.root, code: s.root.split('/').pop(), file: f })))

function scanEach(label, rule) {
  check(label, () => {
    // §2.4 scopes these seven checks to 「分包内」. When the bundle holds no source file at all,
    // scanning it proves nothing -- so this must not report PASS. Distinguish the two cases the
    // review asked for: registryd still requires a bundle (a born face) but it is empty → 「未测」
    // is a defect → FAIL; there is genuinely no bundle this phase (no born face beyond homeos) →
    // print 「本项当期无可检物，不计入通过」 and do not tick.
    if (subPkgFiles.length === 0) {
      if (expectedRoots.length > 0) {
        fail(
          `分包内检查无源文件可扫，但 registryd(${meta.currentPhase}) 要求分包 ${expectedRoots.join(', ')} 存在：` +
            `已出生分包被清空属「未测」，按不通过处理（PRD 22.4「一个 pages/finance 分包」、22.5 第 4 道、§2.4 分包内口径）。`
        )
      } else {
        markNotMeasured(`本项当期无可检物：${meta.currentPhase} 无已出生业务域分包，§2.4 的「分包内」口径无对象，不计入通过`)
      }
      return
    }
    for (const { code, file } of subPkgFiles) {
      rule(code, file, readText(file))
    }
  })
}

// 17.7 第 4 条：分包内的接口调用前缀只能是本面 code，唯一被文档点名的跨面读是 /api/homeos/search。
function apiPaths(raw) {
  const text = stripComments(raw)
  return matches(/\/api\/[a-z0-9_]+(?:\/[A-Za-z0-9_{}\-./]*)?/g, text)
}

scanEach('同源① 分包内无切换家庭调用（§1.2 顶栏·家庭切换器行、17.2）', (code, file, raw) => {
  const text = stripComments(raw)
  const re = /(switch[-_]family|switchFamily|current[-_]?family|currentFamily|setActiveFamily|families?\/(?:switch|current))/gi
  for (const m of matches(re, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内出现切换家庭的调用「${m[0]}」（家庭切换只归 shell，17.2）`)
  }
})

scanEach('同源② 分包内无硬编码面清单（§1.2 首页 C 区行、17.8）', (code, file, raw) => {
  const text = stripComments(raw)
  const codeLiterals = (hay) =>
    [...new Set([...matches(/(['"`])([a-z][a-z0-9-]*)\1/g, hay).map((m) => m[2]).filter((v) => allCodes.includes(v))])]
  // (a) 数组字面量里枚举两个及以上 code —— 就是「分包自己存了一份面清单」的形状。
  for (const arr of matches(/\[[^\]]*\]/g, text)) {
    const inArray = codeLiterals(arr[0])
    if (inArray.length >= 2) {
      fail(`${relative(WEB_DIR, file)}:${lineOf(raw, arr[0])} 数组字面量里枚举了面 code [${inArray.join(', ')}]，即硬编码面清单（挂载集合只能来自 faces[]，17.8）`)
    }
  }
  // (b) 同一文件出现三个及以上 code 字面量：一个分包合法可见的 code 只有「本面」与
  //     文档点名的 /api/homeos/search 两处，枚举到第三个就不可能是这两者的并集。
  const distinct = codeLiterals(text)
  if (distinct.length >= 3) {
    fail(`${relative(WEB_DIR, file)} 出现 ${distinct.length} 个 registry code 字面量 [${distinct.join(', ')}]，即硬编码面清单（17.8：分包不得自己存一份面集合）`)
  }
})

scanEach('同源③ 搜索只调 /api/homeos/search（§1.2 顶栏·搜索行、17.6）', (code, file, raw) => {
  const reject = (path, snippet) => {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, snippet)} 分包 ${code} 的接口调用「${path}」越界：只允许 /api/${code}/* 与被点名的 /api/homeos/search（17.7 第 4 条、17.6）`)
  }
  // 形态一：源码里直接出现的 /api/{x}/... 字面量
  for (const m of apiPaths(raw)) {
    const path = m[0]
    const pathCode = path.split('/')[2]
    if (/search/i.test(path)) {
      if (!/^\/api\/homeos\/search\/?$/.test(path.replace(/\?.*$/, ''))) {
        fail(`${relative(WEB_DIR, file)}:${lineOf(raw, path)} 出现非 /api/homeos/search 的搜索调用「${path}」（17.6：索引与入口都在 HomeOS）`)
      }
      continue
    }
    if (pathCode !== code) reject(path, path)
  }
  // 形态二：走请求层 request(code, path) —— 前缀由 code 拼，所以判 code 实参与路径实参
  const text = stripComments(raw)
  for (const m of matches(/(?:request|apiUrl)\s*(?:<[^()]*>)?\(\s*(['"])([a-z0-9_-]+)\1\s*,\s*(['"])([^'"]*)\3/g, text)) {
    const calledCode = m[2]
    const path = m[4]
    if (/search/i.test(path)) {
      if (!(calledCode === 'homeos' && /^\/search\/?$/.test(path))) {
        fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 搜索调用 ${calledCode}${path} 不是 /api/homeos/search（17.6）`)
      }
      continue
    }
    if (calledCode !== code) reject(`${calledCode}${path}`, m[0])
  }
})

scanEach('同源④ 分包内无自造时间窗状态（§1.2 时间窗条行、§6.4、4.5.3）', (code, file, raw) => {
  const text = stripComments(raw)
  // (a) 归属：period 由主包 shell 级 store 持有，分包不得自存一份月份/季/年。
  for (const m of matches(/(?:(?:const|let|var)\s+period\b|period\s*[:=]\s*(?:ref|reactive|shallowRef|shallowReactive)\s*\(|(?:set|get)StorageSync\s*\(\s*['"`][^'"`]*period)/gi, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内自造 period 状态「${m[0].trim()}」（period 归主包 shell 级 store，§1.3 第 19 条）`)
  }
  // (b) 档位：分包赋给的 period 取值只能是三档编码 YYYY-MM / YYYY-Qn / YYYY（§6.4、定版 ⑬）。
  //     只判「作为 period 值出现」的字面量，避免把账单 due_at 之类的日期显示当成时间窗。
  for (const m of matches(/period\s*[:=]\s*(['"`])([^'"`]*)\1/gi, text)) {
    const v = m[2]
    if (!/^\d{4}(-\d{2}|-Q[1-4])?$/.test(v)) {
      fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} period 取值「${v}」不在三档之内（月/季/年，§1.3 第 19 条）`)
    }
  }
  for (const m of matches(/[?&]period=([^'"`\s&]+)/g, text)) {
    const v = m[1]
    if (!/^\d{4}(-\d{2}|-Q[1-4])?$/.test(v) && !/\$\{|\{\{|process\.env|import\.meta/.test(v) && v !== 'undefined') {
      fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 查询串里的 period「${v}」不在三档之内（§1.3 第 19 条）`)
    }
  }
})

scanEach('同源⑤ 分包内无硬编码色值（§九 主题令牌行、17.9、18.2#10）', (code, file, raw) => {
  const text = stripComments(raw)
  for (const m of matches(/#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})\b/g, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内写死色值「${m[0]}」（只能引用 shell 的一份主题令牌，17.9）`)
  }
  for (const m of matches(/\b(?:rgb|rgba|hsl|hsla|hwb)\s*\(/gi, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m.index)} 分包内写死色值函数「${m[0]}」（17.9）`)
  }
  for (const m of matches(/(?:^|[;{]\s*)(?:color|background(?:-color)?|border-color|fill|stroke)\s*:\s*(black|white|red|blue|green|gray|grey|orange|yellow|purple|pink|brown|silver|gold)\b/gim, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内写死色值「${m[0].trim()}」（17.9）`)
  }
})

scanEach('同源⑥ 分包内无自建未读计数或消息入口（§1.2 顶栏·消息入口行、17.1、17.7 第 1 条）', (code, file, raw) => {
  const text = stripComments(raw)
  for (const m of matches(/\b(?:unread|unreadCount|messageCount|noticeCount|notifications?|unreadBadge)\b/gi, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内出现未读/消息中心的形态「${m[0]}」（未读只归 shell 的一份 store，17.1）`)
  }
  for (const m of matches(/未读|消息中心|通知中心/g, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内出现「${m[0]}」文案位（各面无独立消息中心，17.5）`)
  }
})

scanEach('同源⑦ 分包内无第二处面入口或自建面切换器（§1.3 第 18 条、⑫、17.7 第 3 条）', (code, file, raw) => {
  const text = stripComments(raw)
  for (const m of matches(/(?:face|module)[-_]?(?:switch|entry)|switch(?:Face|Module)|faceSwitch|moduleSwitch|开通更多|更多面|去开通|面切换/g, text)) {
    fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包内出现第二处面入口/面切换器形态「${m[0]}」（面入口全 App 只有首页 C 区矩阵一处，⑫）`)
  }
  for (const m of matches(/(['"`])\/?pages\/([a-z0-9_-]+)\/[A-Za-z0-9_/-]*\1/g, text)) {
    const target = m[0].replace(/^['"`]|['"`]$/g, '').replace(/^\//, '')
    const targetCode = target.split('/')[1]
    if (targetCode !== code) {
      fail(`${relative(WEB_DIR, file)}:${lineOf(raw, m[0])} 分包 ${code} 引用他面路由「${target}」（分包之间不得互引页面，跨面只走深链，17.7 第 4 条）`)
    }
  }
  for (const m of matches(/from\s+['"]([^'"]+)['"]/g, text)) {
    const spec = m[1]
    const abs = spec.startsWith('@/') ? resolve(SRC, spec.slice(2)) : spec.startsWith('.') ? resolve(dirname(file), spec) : null
    if (!abs) continue
    const rel = relative(join(SRC, 'pages'), abs)
    if (!rel.startsWith('..')) {
      const targetCode = rel.split(sep)[0]
      if (targetCode && targetCode !== code) {
        fail(`${relative(WEB_DIR, file)} import 了主包/他面页面目录「${spec}」（分包不得互相 import，§1.3 第 9 条）`)
      }
    }
  }
})

// ---------------------------------------------------------------- 输出
console.log(`check:pages —— 判据源 docs/p1-page-structure-navigation.md §2.4（三查）+ §1.2 校验点列（七条同源）`)
console.log(`registryd live：currentPhase=${meta.currentPhase} implemented=[${meta.implemented.join(', ')}] unborn=[${meta.unborn.join(', ')}] bundleRoots=[${expectedRoots.join(', ')}]`)
console.log(`pages.json：主包路由 ${mainRoutes.length} 条 ${JSON.stringify(mainRoutes)}`)
console.log(`            分包 ${roots.length} 个 ${JSON.stringify(roots)}，分包路由 ${routesOfSub.length} 条 ${JSON.stringify(routesOfSub)}`)
console.log(`分包源码被扫文件：${subPkgFiles.length} 个 ${subPkgFiles.map((s) => relative(WEB_DIR, s.file)).join(', ')}`)
for (const p of passes) console.log(`  PASS  ${p}`)
// 「未测」项按文档口径当期确无可检物，既不阻断也不计入通过 —— 明确打「NOT-MEASURED」而不是打勾，
// 这就是审查要的「没有可检物就是未测，不得报通过」；它不触发非零退出（区别于零可检物的「失败」）。
for (const n of notMeasured) console.log(`  NOT-MEASURED  ${n}`)
if (failures.length) {
  console.error(`\n检查失败 ${failures.length} 项：`)
  for (const f of failures) console.error(`  FAIL  ${f}`)
  console.error(`\n结论：不通过（三查与同源检查任一命中即阻断合并，PRD 22.5 第 4 道）。`)
  process.exit(1)
}
const notMeasuredNote = notMeasured.length ? ` + 未测 ${notMeasured.length} 项（不计入通过）` : ''
console.log(
  `\n结论：三查 ${passes.filter((p) => p.startsWith('三查')).length}/3 通过 + 同源 ${passes.filter((p) => p.startsWith('同源')).length}/7 通过 + 结构 ${passes.filter((p) => !p.startsWith('三查') && !p.startsWith('同源')).length} 项通过，共 ${passes.length} 项检查通过${notMeasuredNote}。`
)

