// 构建期生成物：web/generated/domains.json
//
// 内容 = registryd 的 live 导出（--format=codes + --format=meta），逐值不改写、不加时间戳，
// 因此可以字节比对。
//
// 生成物纪律（本卡选定的是「提交 + 断言」这一条，不是「gitignore + 每次现生成」）：
//   · 文件进版本库，构建与检查前由 npm run gen:domains 现取现覆写；
//   · scripts/check-pages.mjs 断言它与 registryd 的当前输出**逐值一致**，不一致即失败。
//   代价：registry 变更后必须重跑 gen:domains 并提交，否则三查在构建前就红；好处是
//   CI 里能看到「这份快照改了哪一行」，且离线构建不需要 Go 也能读到快照（判定仍要 live）。
//
// 用途限定：**只供构建期与检查脚本使用**。运行时代码不得 import 它 —— 面集合在客户端的
// 唯一来源是服务端的 faces[] 与 family/modules（§1.3 第 6 条、17.8），构建期快照进运行时
// 就等于把分包集合硬编码进客户端。check-pages.mjs 的第 2 查会拦这条。
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { registrySnapshot, WEB_DIR } from './registryd.mjs'

const OUT = join(WEB_DIR, 'generated', 'domains.json')
const snapshot = registrySnapshot()

mkdirSync(join(WEB_DIR, 'generated'), { recursive: true })
writeFileSync(OUT, `${JSON.stringify(snapshot, null, 2)}\n`, 'utf8')

console.log(
  `gen:domains -> generated/domains.json（currentPhase=${snapshot.meta.currentPhase}, ` +
    `codes=[${snapshot.codes.join(', ')}], bundleRoots=[${snapshot.meta.bundleRoots.join(', ')}]）`
)
