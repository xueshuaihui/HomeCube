// Utility functions for formatting and parsing

/**
 * Format amount in cents to human-readable currency string
 * @param cents Amount in cents (integer)
 * @returns Formatted string like "¥123.45"
 */
export function formatAmount(cents: number): string {
  const yuan = cents / 100
  return `¥${yuan.toFixed(2)}`
}

/**
 * 一条「金额」的字面量形状：可带千分位逗号，小数最多两位（人民币口径）。
 * 先长后短：带千分位的形状要整体命中，否则「1,234」会被拆成 1 与 234。
 */
const AMOUNT_PATTERN = /\d{1,3}(?:,\d{3})+(?:\.\d{1,2})?|\d+(?:\.\d{1,2})?/

export interface ParsedAmount {
  /** 整数分（PRD 17.7 第 5 条与 18.2：全链路以「分」计，无浮点） */
  cents: number
  /** 命中的那段原文，用于从备注里摘掉 */
  matched: string
  /** 命中片段之外的其余文字：即备注 */
  remainder: string
  /** 摘掉第一个金额之后**还能不能再解析出一个金额**（供 UI 显式提示，不自行改数） */
  ambiguous: boolean
}

/** 「58.9」→ 5890 分：整数与小数位分别按整数解析，不做浮点乘除。 */
function yuanTextToCents(text: string): number {
  const plain = text.replace(/,/g, '')
  const [intPart, fracPart = ''] = plain.split('.')
  const yuan = Number.parseInt(intPart, 10)
  const cents = Number.parseInt((fracPart + '00').slice(0, 2), 10)
  if (Number.isNaN(yuan) || Number.isNaN(cents)) return 0
  return yuan * 100 + cents
}

/**
 * 金额识别（P1 口径）：**取输入里的第一个金额，剩余文字作备注**。
 *
 * 为什么不是「把所有数字拼起来」：旧实现把非数字字符全部删掉后再 parseFloat，
 * 「午餐 58 晚餐 20」→ "5820" → 5820 元，是把两条不相干的数**造**成一个金额。
 * 为什么也不做「歧义即弹确认」：PRD 17.4 定「AI 归类在 P6 交付」，P1 必须是确定性解析；
 * 而 §6.2 的记账步数预算是「＋ → 输入金额 → 保存 = 3 步」，多一个确认弹层即 4 步、
 * 直接判失败。因此 P1 取第一个金额、余文入备注，并把第二个金额的存在**显式回显**
 * （`ambiguous`）让用户自己决定要不要改写 —— 呈现歧义，不制造歧义，也不为歧义加步骤。
 */
export function extractFirstAmount(input: string): ParsedAmount | null {
  if (!input) return null
  const hit = AMOUNT_PATTERN.exec(input)
  if (!hit) return null

  const matched = hit[0]
  const remainder = (input.slice(0, hit.index) + input.slice(hit.index + matched.length))
    .replace(/\s+/g, ' ')
    .trim()

  return {
    cents: yuanTextToCents(matched),
    matched,
    remainder,
    ambiguous: AMOUNT_PATTERN.test(remainder),
  }
}

/**
 * Parse user input amount string to cents
 * @param input User input like "123.45" or "123"
 * @returns 输入里**第一个**金额，单位分（整数）；解析不出即 0
 */
export function parseAmountToCents(input: string): number {
  return extractFirstAmount(input)?.cents ?? 0
}

/**
 * Format date to relative time string
 * @param isoString ISO timestamp
 * @returns Relative time like "5分钟前", "今天 14:30", "3天前"
 */
export function formatRelativeTime(isoString: string): string {
  const now = new Date()
  const target = new Date(isoString)
  const diffMs = now.getTime() - target.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  const diffMin = Math.floor(diffSec / 60)
  const diffHour = Math.floor(diffMin / 60)
  const diffDay = Math.floor(diffHour / 24)

  if (diffMin < 1) return '刚刚'
  if (diffMin < 60) return `${diffMin} 分钟前`
  if (diffHour < 24) return `${diffHour} 小时前`
  if (diffDay < 7) return `${diffDay} 天前`

  // Format as MM-DD HH:MM for older items
  const month = target.getMonth() + 1
  const day = target.getDate()
  const hours = target.getHours().toString().padStart(2, '0')
  const minutes = target.getMinutes().toString().padStart(2, '0')
  return `${month}月${day}日 ${hours}:${minutes}`
}

/**
 * Format date to display string
 * @param isoString ISO timestamp
 * @returns Formatted date like "10月3日 14:30"
 */
export function formatDate(isoString: string): string {
  const date = new Date(isoString)
  const month = date.getMonth() + 1
  const day = date.getDate()
  const hours = date.getHours().toString().padStart(2, '0')
  const minutes = date.getMinutes().toString().padStart(2, '0')
  return `${month}月${day}日 ${hours}:${minutes}`
}

/**
 * Truncate text with ellipsis
 * @param text Text to truncate
 * @param maxLength Maximum length
 * @returns Truncated text
 */
export function truncateText(text: string, maxLength: number): string {
  if (!text || text.length <= maxLength) return text
  return text.slice(0, maxLength) + '...'
}
