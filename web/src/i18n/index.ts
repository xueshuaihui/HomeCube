import { createI18n } from 'vue-i18n'

// i18n 结构：语言资源**按 code 分文件**（docs/p1-tech-plan.md §九 i18n 行、PRD 12.4）
//
//   src/i18n/<locale>/<code>.json        一个域一个文件，文件名 = 该域的 code
//
// 文件名同时是 key 的命名空间，所以 §2.1「面内二级 Tab key = `{code}.tab.{n}`」在这里直接成立：
//   t('finance.tab.flow') → src/i18n/zh-CN/finance.json 的 tab.flow
//
// 两条纪律：
//   1. 本文件不出现 code 清单。待合并的语言包由 import.meta.glob 从目录里真实存在的文件推导
//      （路径即 locale + code），不在 web/ 里手抄一份面 code 清单 —— 一个 code 的唯一真源是
//      server/packages/registry（§1.3「一个 code，七处生效」）。
//   2. 英文本地化属 P6 交付（§九 i18n 行、PRD 12.4），因此本卡**不建 en 目录**、
//      也不建「有键无值」的半成品语言包；文件里只有页面真正渲染出来的那条文案。
type MessageTree = Record<string, unknown>

const files = import.meta.glob<{ default: MessageTree }>('./*/*.json', { eager: true })

function buildMessages() {
  const messages: Record<string, Record<string, MessageTree>> = {}
  for (const [path, mod] of Object.entries(files)) {
    // './<locale>/<code>.json'
    const parts = path.replace(/^\.\//, '').split('/')
    const [locale, file] = [parts[0], parts[parts.length - 1]]
    const code = file.replace(/\.json$/, '')
    ;(messages[locale] ??= {})[code] = mod.default
  }
  return messages
}

const messages = buildMessages()

// 未建语言包的 locale 一律回落 zh-CN（P6 补齐英文后本行不改形状）。
const DEFAULT_LOCALE = 'zh-CN'
const current = typeof uni !== 'undefined' && uni.getLocale ? uni.getLocale() : DEFAULT_LOCALE

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: messages[current] ? current : DEFAULT_LOCALE,
  fallbackLocale: DEFAULT_LOCALE,
  messages
})
