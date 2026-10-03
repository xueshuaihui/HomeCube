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
 * Parse user input amount string to cents
 * @param input User input like "123.45" or "123"
 * @returns Amount in cents (integer)
 */
export function parseAmountToCents(input: string): number {
  if (!input || !input.trim()) return 0

  // Remove any non-digit characters except dot and minus
  const cleaned = input.replace(/[^0-9.-]/g, '')

  if (!cleaned) return 0

  const num = parseFloat(cleaned)

  if (isNaN(num)) return 0

  // Convert to cents and round to avoid floating point errors
  return Math.round(num * 100)
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
