// 主包 shell 级 store（§1.2 区域所有权表、§1.3 第 16/19 条、PRD 17.7 第 8 条）
//
// 四份 shell 状态在这里、且只在这里持有一份：
//   · `period`     —— 时间窗条（三档编码 YYYY-MM / YYYY-Qn / YYYY，⑬），分包只读不造
//   · `faces`      —— 面集合（首页 C 区、「＋」目标、搜索分组、到期中心、动态流筛选五处同源）
//   · `unread`     —— 未读的**那一个数**（顶栏红点 + D 行红点），出口只有 home/summary 与
//                     notifications / dynamics 的已读回执，客户端不相减、不缓存第三份（17.1）
//   · 会话快照     —— 当前家庭与**本人角色**（治理页的权限判定与「我的」页共用这一份）
//
// 字段名一律按冻结契约 `server/contracts/openapi/homeos.yaml`：
// `home/summary` 的 `faces[]` 是 `{code,name,availability,headline,badge,as_of}`、
// `dynamics.items[]` 是 `{id,code,actor_name,action,summary,at}`、
// `due_today.items[]` 是 `{id,title,due_at,source_system}`、`unread` 是整数。
// 页面上出现的 face_code / member_name / headline(动态) / created_at / status_sentence /
// badge_count / available 都是旧客户端自造名，已在本轮整体改齐。

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { request, unwrapBody, RequestError } from '@/utils/request'

/** UI 侧的时间窗档位（§1.3 第 19 条：只有月/季/年三档，没有「周」与「全部」）。 */
export type PeriodGrain = 'month' | 'quarter' | 'year'
/** 契约口径的 period 编码：`YYYY-MM` / `YYYY-Qn` / `YYYY`。 */
export type PeriodCode = string

export type MemberRole = 'owner' | 'member' | 'ward' | 'guest'
export type NotificationType = 'budget_alert' | 'system' | 'reminder'
export type Availability = 'available' | 'unavailable'

/** home/summary 的 family.members[]（⑭：`relation` 是称谓唯一源）。 */
export interface MemberInfo {
  member_id: string
  name: string
  relation?: string
  avatar?: string | null
}

export interface FamilyInfo {
  id: string
  name: string
  members: MemberInfo[]
  member_count: number
}

/** B 区今日项：`count` 是当日全量、`items[]` 恒 ≤3（17.2），两者不等是设计结果。 */
export interface DueItem {
  id: string
  title: string
  due_at: string
  source_system: string
}

export interface DueToday {
  count: number
  items: DueItem[]
}

/** C 区格子。`badge` 是「待处理」，与顶栏/D 行的「未读」是两个语义（17.1 第 3 条）。 */
export interface FaceStatus {
  code: string
  name: string
  availability: Availability
  headline: string
  badge: number
  as_of?: string
}

/** D 区动态条目（同 GET /dynamics 的 items[]）。 */
export interface DynamicItem {
  id: string
  code: string
  actor_name: string
  action: string
  summary: string
  at: string
}

/** 消息中心条目（GET /notifications 的 items[]：只有 `read_at` 表达已读，没有 is_read）。 */
export interface NotificationItem {
  id: string
  type: NotificationType
  content: string
  read_at: string | null
  created_at: string
}

/** GET /family/modules 的一项（家庭口径 `enabled` + 角色口径 `visible`）。 */
export interface FamilyModule {
  code: string
  name: string
  icon?: string
  enabled: boolean
  visible: boolean
  born: boolean
  availability: Availability
}

export interface HomeSummaryResponse {
  family: FamilyInfo
  due_today: DueToday
  faces: FaceStatus[]
  dynamics: { items: DynamicItem[] }
  unread: number
}

export interface NotificationsResponse {
  items: NotificationItem[]
  unread: number
  unread_by_type?: Partial<Record<NotificationType, number>>
  next_cursor?: string | null
}

export interface MarkReadResponse {
  marked_count: number
  unread: number
}

/**
 * 契约里 `home/summary` 的 family 块**不带 timezone**（只有 id/name/members/member_count），
 * 而 period 缺省档与问候语都要按家庭时区取「当前」。这里先按 PRD 12.4 的默认时区取值，
 * 缺字段已作为定版冲突上报（GET /api/homeos/settings 落地后改读设置）。
 */
const FALLBACK_TIMEZONE = 'Asia/Shanghai'

/** localStorage key for persisting period value across sessions. */
const STORAGE_KEY_PERIOD = 'finance_period'

/** 从 localStorage 读取保存的 period 档位，非法值回落 'month'。 */
function loadStoredGrain(): PeriodGrain {
  try {
    const stored = uni.getStorageSync(STORAGE_KEY_PERIOD)
    if (stored === 'month' || stored === 'quarter' || stored === 'year') {
      return stored
    }
  } catch {
    // 读取失败：静默回落默认档
  }
  return 'month'
}

/** 将 period 档位持久化到 localStorage。 */
function saveStoredGrain(grain: PeriodGrain) {
  try {
    uni.setStorageSync(STORAGE_KEY_PERIOD, grain)
  } catch {
    // 写入失败：不阻断主流程
  }
}

/** 取家庭时区下的「现在」分量，避免用设备时区造出跨日的档（§6.4）。 */
function nowInTimezone(tz: string): { year: number; month: number } {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      year: 'numeric',
      month: 'numeric',
    }).formatToParts(new Date())
    const read = (k: string) => Number(parts.find((p) => p.type === k)?.value)
    if (Number.isFinite(read('year')) && Number.isFinite(read('month'))) {
      return { year: read('year'), month: read('month') }
    }
  } catch {
    // 未知时区名：回落设备时间，不因此阻断首屏。
  }
  const now = new Date()
  return { year: now.getFullYear(), month: now.getMonth() + 1 }
}

/** 三档编码（⑬）：月 `YYYY-MM`、季 `YYYY-Qn`、年 `YYYY`。 */
export function encodePeriod(grain: PeriodGrain, year: number, month: number): PeriodCode {
  if (grain === 'year') return `${year}`
  if (grain === 'quarter') return `${year}-Q${Math.ceil(month / 3)}`
  return `${year}-${String(month).padStart(2, '0')}`
}

export const useHomeStore = defineStore('home', () => {
  // ---------------------------------------------------------------- 时间窗条
  // 从 localStorage 恢复档位，跨会话记住最后一窗（PRD 4.5.3）。
  const storedGrain = loadStoredGrain()
  const periodGrain = ref<PeriodGrain>(storedGrain)
  // 缺省档 = 家庭时区的当前月（§6.4「缺省为家庭时区的当前月」），不是设备当月。
  const initial = nowInTimezone(FALLBACK_TIMEZONE)
  const period = ref<PeriodCode>(encodePeriod(storedGrain, initial.year, initial.month))

  // ---------------------------------------------------------------- 会话快照
  const role = ref<MemberRole | ''>('')
  const sessionFamilyId = ref<string>('')
  const sessionLoaded = ref(false)

  // ---------------------------------------------------------------- home/summary 四区
  const family = ref<FamilyInfo | null>(null)
  const dueToday = ref<DueToday>({ count: 0, items: [] })
  const faces = ref<FaceStatus[]>([])
  const dynamics = ref<DynamicItem[]>([])
  const unread = ref(0)
  /** 消息中心三个分型的未读：**只来自服务端**（notifications 的 unread_by_type）。 */
  const unreadByType = ref<Record<NotificationType, number>>({
    budget_alert: 0,
    system: 0,
    reminder: 0,
  })

  const summaryLoading = ref(false)
  const summaryError = ref('')

  // ---------------------------------------------------------------- 面配置（开通页）
  const modules = ref<FamilyModule[]>([])
  /** GET /family/modules 回列表时带的那个 version：PUT 的乐观锁凭据（17.8）。 */
  const modulesVersion = ref(0)
  const modulesLoading = ref(false)

  // ---------------------------------------------------------------- 派生值
  const greeting = computed(() => {
    if (!family.value) return ''

    const options: Intl.DateTimeFormatOptions = {
      timeZone: FALLBACK_TIMEZONE,
      hour: 'numeric',
      hour12: false,
    }
    const hourStr = new Intl.DateTimeFormat('zh-CN', options).format(new Date())
    const hour = parseInt(hourStr, 10)

    let timeGreeting = '你好'
    if (hour >= 5 && hour < 9) timeGreeting = '早上好'
    else if (hour >= 9 && hour < 12) timeGreeting = '上午好'
    else if (hour >= 12 && hour < 14) timeGreeting = '中午好'
    else if (hour >= 14 && hour < 18) timeGreeting = '下午好'
    else if (hour >= 18 && hour < 22) timeGreeting = '晚上好'
    else timeGreeting = '夜深了'

    // ⑭：称谓取 `members[].relation`、缺值回落成员名，**不为问候语另调 kin 的接口**。
    // 契约的 members[] 里没有「哪一条是本人」的标记（yaml 的 family 块连 role 都没有，
    // §3.4 的样例里有），因此 P1 取名册首条 —— 与定版效果图「早上好，爸爸」同形，
    // 已作为定版冲突上报（缺 self member 标记）。
    const self = currentMember.value
    const salutation = self?.relation || self?.name || ''
    return salutation ? `${timeGreeting}，${salutation}` : timeGreeting
  })

  /** 名册首条（见上：P1 无 self 标记）。D 区与 A 区渲染都读它，不另数一次。 */
  const currentMember = computed(() => family.value?.members?.[0] || null)

  /** A 区「+N」= max(member_count - 3, 0)：数的是服务端给的全量计数，不数列表长度（⑮）。 */
  const overflowMemberCount = computed(() => {
    const total = family.value?.member_count ?? 0
    return Math.max(0, total - 3)
  })

  const isAdmin = computed(() => role.value === 'owner')

  /** 「＋」目标列表与开通页共用的挂载集合：已启用 ∧ 已出生 ∧ 该角色可见（17.8，第三层在服务端裁）。 */
  const mountedModules = computed(() => modules.value.filter((m) => m.enabled && m.born && m.visible))

  // ---------------------------------------------------------------- 时间窗
  /** 切换时间窗档位并持久化到 localStorage（PRD 4.5.3：会话内一致 + 跨会话记住）。 */
  function setPeriodGrain(grain: PeriodGrain) {
    periodGrain.value = grain
    saveStoredGrain(grain)
    const { year, month } = nowInTimezone(FALLBACK_TIMEZONE)
    period.value = encodePeriod(grain, year, month)
  }

  /** 显式设定编码档（深链或家庭切换后按当期重建时用），会反推档位。 */
  function setPeriodCode(code: PeriodCode) {
    if (/^\d{4}$/.test(code)) periodGrain.value = 'year'
    else if (/^\d{4}-Q[1-4]$/.test(code)) periodGrain.value = 'quarter'
    else if (/^\d{4}-\d{2}$/.test(code)) periodGrain.value = 'month'
    else return // 不在三档之内：不静默改参（§6.4「非法值 400 且不回落」的客户端侧对齐）
    period.value = code
    saveStoredGrain(periodGrain.value)
  }

  // ---------------------------------------------------------------- home/summary
  /** 首屏唯一请求（§3.1）：四区与 unread 都从这一份派生，页面不再各自打接口。 */
  async function fetchSummary(): Promise<HomeSummaryResponse | null> {
    summaryLoading.value = true
    summaryError.value = ''
    try {
      const res = await request.get('/api/homeos/home/summary', { params: { period: period.value } })
      const data = unwrapBody<HomeSummaryResponse>(res)
      updateSummary(data)
      return data
    } catch (err: any) {
      summaryError.value = err?.message || '加载失败'
      throw err
    } finally {
      summaryLoading.value = false
    }
  }

  function updateSummary(data: HomeSummaryResponse) {
    family.value = data.family
    dueToday.value = data.due_today ?? { count: 0, items: [] }
    faces.value = data.faces ?? []
    dynamics.value = data.dynamics?.items ?? []
    // 契约的 unread 就是一个整数（17.1 的「未读只有一个数」）。
    unread.value = typeof data.unread === 'number' ? data.unread : 0
    if (data.family?.id) sessionFamilyId.value = data.family.id
  }

  // ---------------------------------------------------------------- 会话角色（15.1、17.5）
  /**
   * 角色快照的唯一来源：`GET /api/homeos/families` → `{families:[{id,name,role}]}`，
   * `role` 即**调用方在该家庭里的角色**（svc-homeos handler `ListFamilies` 的 m.role）。
   * 首页不调它（§3.1 首屏只有一个请求），治理页与「我的」页进页面时 ensure 一次。
   */
  async function ensureSession(): Promise<void> {
    if (sessionLoaded.value) return
    await loadSession()
  }

  async function loadSession(): Promise<void> {
    const res = await request.get('/api/homeos/families')
    const body = unwrapBody<{ families?: Array<{ id: string; name: string; role: MemberRole }> }>(res)
    const list = Array.isArray(body?.families) ? body.families : []
    const mine = list.find((f) => f.id === sessionFamilyId.value) || list[0]
    if (mine) {
      sessionFamilyId.value = mine.id
      role.value = mine.role
    }
    sessionLoaded.value = true
  }

  // ---------------------------------------------------------------- 未读（只在这一处写）
  /** 消息中心列表读到的未读口径（顶栏与 D 行红点读的就是这两个数）。 */
  function setNotificationsUnread(next: number, byType?: Partial<Record<NotificationType, number>>) {
    if (typeof next === 'number') unread.value = next
    if (byType) {
      unreadByType.value = {
        budget_alert: byType.budget_alert ?? 0,
        system: byType.system ?? 0,
        reminder: byType.reminder ?? 0,
      }
    }
  }

  /** POST /notifications/read：单个（notification_ids）或全部（all:true）。 */
  async function markNotificationsRead(
    opts: { ids?: string[]; all?: boolean }
  ): Promise<MarkReadResponse> {
    const payload = opts.all
      ? { notification_ids: [], all: true }
      : { notification_ids: opts.ids ?? [], all: false }
    const res = await request.post('/api/homeos/notifications/read', payload)
    const body = unwrapBody<MarkReadResponse>(res)
    if (body && typeof body.unread === 'number') unread.value = body.unread
    if (body && typeof body.marked_count === 'number' && body.marked_count > 0) {
      // 全部已读后分型未读同步归零；单条已读服务端不回分型计数，留给下一次列表读。
      if (opts.all) {
        unreadByType.value = { budget_alert: 0, system: 0, reminder: 0 }
      }
    }
    return body
  }

  /** POST /dynamics/read：D 行与动态流的「全部已读」，回执里的 unread 就是同一个数。 */
  async function markDynamicsRead(opts: { ids?: string[]; all?: boolean } = {}): Promise<MarkReadResponse> {
    const payload = { dynamic_ids: opts.all ? [] : opts.ids ?? [], all: !!opts.all }
    const res = await request.post('/api/homeos/dynamics/read', payload)
    const body = unwrapBody<MarkReadResponse>(res)
    if (body && typeof body.unread === 'number') unread.value = body.unread
    return body
  }

  // ---------------------------------------------------------------- 面配置（17.8）
  async function loadModules(): Promise<FamilyModule[]> {
    modulesLoading.value = true
    try {
      const res = await request.get('/api/homeos/family/modules')
      const body = unwrapBody<{ modules?: FamilyModule[]; version?: number }>(res)
      modules.value = Array.isArray(body?.modules) ? body.modules : []
      modulesVersion.value = typeof body?.version === 'number' ? body.version : 0
      return modules.value
    } finally {
      modulesLoading.value = false
    }
  }

  /**
   * 开通 / 停用一面：**只有一条 `PUT /api/homeos/family/modules`**，面 code 在请求体里
   * （`{code, enabled, version}`），不存在 `/family/modules/{code}` 这条路径（PRD 3.7、契约 277 行）。
   *
   * 乐观锁冲突（409）不盲重试：按 §6.5 的「用最新」分支处置 —— 重拉列表拿到新 version、
   * 开关回位，由用户看着新状态再决定要不要再拨一次。金额型与配置型都不接受静默覆盖。
   */
  async function setModuleEnabled(
    code: string,
    enabled: boolean
  ): Promise<{ ok: boolean; conflict: boolean; message: string }> {
    if (!modulesVersion.value) {
      await loadModules()
    }

    try {
      const res = await request.put('/api/homeos/family/modules', {
        code,
        enabled,
        version: modulesVersion.value,
      })
      const body = unwrapBody<{ version?: number; pver?: number }>(res)
      if (typeof body?.version === 'number') modulesVersion.value = body.version

      const target = modules.value.find((m) => m.code === code)
      if (target) target.enabled = enabled

      // 导航集合四处同源（17.8）：矩阵与「＋」目标都要在 ≤2s 内跟上。
      if (family.value) await fetchSummary()
      return { ok: true, conflict: false, message: '' }
    } catch (err: any) {
      const conflict = err instanceof RequestError && err.status === 409
      if (conflict) await loadModules() // 重拉而不是重发
      return {
        ok: false,
        conflict,
        message: err?.message || '操作失败',
      }
    }
  }

  function clearData() {
    family.value = null
    dueToday.value = { count: 0, items: [] }
    faces.value = []
    dynamics.value = []
    unread.value = 0
    unreadByType.value = { budget_alert: 0, system: 0, reminder: 0 }
    role.value = ''
    sessionFamilyId.value = ''
    sessionLoaded.value = false
    modules.value = []
    modulesVersion.value = 0
    setPeriodGrain('month')
  }

  return {
    // state
    period,
    periodGrain,
    role,
    sessionFamilyId,
    family,
    dueToday,
    faces,
    dynamics,
    unread,
    unreadByType,
    modules,
    modulesVersion,
    summaryLoading,
    summaryError,
    modulesLoading,
    // getters
    greeting,
    overflowMemberCount,
    isAdmin,
    mountedModules,
    currentMember,
    // actions
    setPeriodGrain,
    setPeriodCode,
    fetchSummary,
    updateSummary,
    ensureSession,
    loadSession,
    setNotificationsUnread,
    markNotificationsRead,
    markDynamicsRead,
    loadModules,
    setModuleEnabled,
    clearData,
  }
})
