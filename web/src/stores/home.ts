import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export type PeriodType = 'month' | 'quarter' | 'year'

export interface MemberInfo {
  user_id: string
  name: string
  avatar?: string
  relation: string
  has_account: boolean
}

export interface FamilyInfo {
  id: string
  name: string
  timezone: string
  currency: string
  members: MemberInfo[]
}

export interface DueItem {
  id: string
  due_at: string // ISO timestamp
  headline: string
  source_code: string // face code like 'finance'
}

export interface DueToday {
  count: number
  items: DueItem[]
}

export interface FaceStatus {
  code: string
  name: string
  status_sentence: string // 今日状态句
  badge_count?: number // 待处理数
  available: boolean
  unavailable_reason?: string
  last_value?: string // 最后一次值
  last_updated_at?: string // 时效标注
}

export interface DynamicItem {
  id: string
  face_code: string
  member_name: string
  headline: string // 服务端拼好的整句
  relative_time: string // 相对时间文案
  created_at: string
}

export interface HomeSummaryResponse {
  family: FamilyInfo
  due_today: DueToday
  faces: FaceStatus[]
  dynamics: {
    items: DynamicItem[]
  }
  unread: {
    messages: number
  }
}

export const useHomeStore = defineStore('home', () => {
  // Period state (shell-level)
  const period = ref<PeriodType>('month')

  // Home summary data
  const family = ref<FamilyInfo | null>(null)
  const dueToday = ref<DueToday>({ count: 0, items: [] })
  const faces = ref<FaceStatus[]>([])
  const dynamics = ref<DynamicItem[]>([])
  const unreadCount = ref(0)

  // Computed: greeting based on family timezone and current time
  const greeting = computed(() => {
    if (!family.value) return ''

    const tz = family.value.timezone || 'Asia/Shanghai'
    const now = new Date()

    // Get hour in family's timezone
    const options: Intl.DateTimeFormatOptions = {
      timeZone: tz,
      hour: 'numeric',
      hour12: false,
    }
    const hourStr = new Intl.DateTimeFormat('zh-CN', options).format(now)
    const hour = parseInt(hourStr, 10)

    let timeGreeting = '你好'
    if (hour >= 5 && hour < 9) {
      timeGreeting = '早上好'
    } else if (hour >= 9 && hour < 12) {
      timeGreeting = '上午好'
    } else if (hour >= 12 && hour < 14) {
      timeGreeting = '中午好'
    } else if (hour >= 14 && hour < 18) {
      timeGreeting = '下午好'
    } else if (hour >= 18 && hour < 22) {
      timeGreeting = '晚上好'
    } else {
      timeGreeting = '夜深了'
    }

    // Get first member's relation as salutation
    const firstMember = family.value.members?.[0]
    const salutation = firstMember?.relation || firstMember?.name || ''

    return salutation ? `${timeGreeting}，${salutation}` : timeGreeting
  })

  // Computed: overflow member count for avatar row
  const overflowMemberCount = computed(() => {
    if (!family.value || !family.value.members) return 0
    const total = family.value.members.length
    return Math.max(0, total - 3)
  })

  // Set period and trigger reload
  function setPeriod(p: PeriodType) {
    period.value = p
  }

  // Update home summary data
  function updateSummary(data: HomeSummaryResponse) {
    family.value = data.family
    dueToday.value = data.due_today
    faces.value = data.faces
    dynamics.value = data.dynamics.items
    unreadCount.value = data.unread.messages
  }

  // Clear data (e.g., on logout)
  function clearData() {
    family.value = null
    dueToday.value = { count: 0, items: [] }
    faces.value = []
    dynamics.value = []
    unreadCount.value = 0
  }

  return {
    period,
    family,
    dueToday,
    faces,
    dynamics,
    unreadCount,
    greeting,
    overflowMemberCount,
    setPeriod,
    updateSummary,
    clearData,
  }
})
