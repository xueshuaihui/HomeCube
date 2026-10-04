<script setup lang="ts">
// pages/homeos/quick-add/index —— 全局「＋」快速添加页（§4.2、PRD 17.4）
//
// 实现 PRD 17.4 规格：
//   · 四种输入形态：文字 / 语音 / 拍照 / 模板
//   · P1-P5 期间目标面由用户手选（仅本家庭已启用且服务已出生的面）
//   · 手选路径 ≤2 步；记账步数预算 = ＋ → 输入金额 → 保存 = 3 步（§6.2）
//   · 取消即返回进入前的页面；未提交的草稿不入任何业务对象
//
// 三条本轮改齐的口径：
//   1. **目标面集合不来自本页自造的名册**：读 shell store 的 `modules`（GET /family/modules）
//      后取 `mountedModules` = 已启用 ∧ 服务已出生 ∧ 该角色可见（17.8、五处同源）。
//      旧的 catch 分支里写死六个面并「enabled: true」是造数，失败时宁可不渲染目标位。
//   2. **金额解析取第一个金额，余文入备注**（utils/format 的 extractFirstAmount）：
//      旧实现把所有非数字字符删净再 parseFloat，「午餐 58 晚餐 20」会变成 5820 元。
//      歧义不弹确认层（会把记账从 3 步变 4 步），而是把第二个金额显式回显。
//   3. **流水载荷按 svc-finance 的 CreateTransactionRequest 组**：
//      `family_id`（store 的会话家庭）+ `type` + `amount_cents`（整数分）+ `account_id`
//      （GET /api/finance/accounts?family_id= 取的可见选择器）+ `occurred_at`（本页日期选择）
//      + `description` + `client_request_id`。缺 `family_id`/`account_id` 就是 400，
//      所以两者任一取不到时**不提交**，在本页说明原因。

import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, unwrapBody } from '@/utils/request'
import { extractFirstAmount, formatAmount, newIdempotencyUUID } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

const { t, te } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

type InputMode = 'text' | 'voice' | 'photo' | 'template'
type TemplateKey = 'breakfast' | 'lunch' | 'dinner' | 'transport' | 'shopping' | 'entertainment' | 'medical' | 'education'

/** 记账面的 code：只有这一个目标要求「必须有金额」，所以它单独参与模板档的可用性判定。 */
const FINANCE_CODE = 'finance'

/** 账户条目只取建流水要用的三个字段（GET /api/finance/accounts 的 items[] 子集）。 */
interface AccountOption {
  id: string
  name: string
  type: string
}

const inputMode = ref<InputMode>('text')
const textInput = ref('')
const selectedTemplate = ref('')
const selectedFace = ref('')
const submitting = ref(false)
const amountError = ref('')

// 语音与拍照：识别链路（录音上传 + ASR / 小票 OCR）未接入本页，
// 因此不假装在录、也不假装拍到了 —— 明示未开放，引导到文字或模板档。
const unsupportedModes: InputMode[] = ['voice', 'photo']

/**
 * 目标面集合 = shell 的挂载集合（已启用 ∧ 已出生 ∧ 该角色可见）。
 * 服务端已按角色裁过一层，客户端不再自己按角色过滤（17.7 第 4 条）。
 */
const targetFaces = computed(() =>
  homeStore.mountedModules.map((m) => ({ code: m.code, name: m.name }))
)

/** 常用模板：本地快捷词，标签走文案库，内容进的就是文字档。 */
const templateKeys: TemplateKey[] = [
  'breakfast',
  'lunch',
  'dinner',
  'transport',
  'shopping',
  'entertainment',
  'medical',
  'education',
]
const templates = computed(() =>
  templateKeys.map((key) => ({ key, label: t(`homeos.quickadd.template_${key}`) }))
)

const isFinanceTarget = computed(() => selectedFace.value === FINANCE_CODE)

/**
 * 模板档 × 记账目标 = 一条永远保存不了的点击，所以这一组合**不给点**（17.4）：
 * 模板行的内容就是一个词（「午餐」），P1 只做确定性解析、AI 归类在 P6，
 * 词里没有数字 ⇒ `extractFirstAmount` 恒为空 ⇒ 记账必缺 `amount_cents`。
 * 处置不是「点了再报错」，而是：目标位显式标「未开放」并说明原因（同源 17.8 的
 * 「不可用 ≠ 不渲染」：这一格仍渲染，因为记账面对用户是可用的，只是在模板档下不可用），
 * 且切进模板档时把已选的记账目标显式改到其它面 —— 没有任何一步会落在死路上。
 */
function isFaceBlocked(code: string): boolean {
  return code === FINANCE_CODE && inputMode.value === 'template'
}

/** 模板档可去的其它目标（非记账面走 `POST /api/homeos/todos`，词就是标题）。 */
const templateTargetFaces = computed(() =>
  targetFaces.value.filter((face) => face.code !== FINANCE_CODE)
)

const amountSource = computed(() => {
  if (inputMode.value === 'text') return textInput.value.trim()
  if (inputMode.value === 'template') return selectedTemplate.value.trim()
  return ''
})

/** 第一个金额 + 余文备注 + 是否还有第二个金额（P1 确定性解析，不做 AI 归类，17.4）。 */
const parsed = computed(() => extractFirstAmount(amountSource.value))
const parsedAmountCents = computed(() => parsed.value?.cents ?? 0)
const remark = computed(() => parsed.value?.remainder ?? '')
/** 第二个金额的字面量，用于把它显式回显而不是静默丢掉。 */
const secondAmountText = computed(() => {
  if (!parsed.value?.ambiguous) return ''
  return extractFirstAmount(parsed.value.remainder)?.matched ?? ''
})

/** 解析不出金额时的文案分两种：内容里没数字 vs 这个输入形态根本没有可读内容。 */
function unavailableAmountMessage(): string {
  return amountSource.value
    ? t('homeos.quickadd.amount_unrecognized')
    : t('homeos.quickadd.amount_unavailable')
}

// 改输入即清掉上一条失败提示，避免旧错误挂在已修正的表单上
watch([amountSource, selectedFace, inputMode], () => {
  amountError.value = ''
})

// ---------------------------------------------------------------- 账户与日期
const accounts = ref<AccountOption[]>([])
const accountId = ref('')
const accountsLoading = ref(false)
const accountsError = ref('')

/** 发生日期：默认今天（本地日历日），日期选择器只给到日，时刻取当前挂钟。 */
function todayLocal(): string {
  const now = new Date()
  const m = String(now.getMonth() + 1).padStart(2, '0')
  const d = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${m}-${d}`
}
const occurredDate = ref(todayLocal())

/**
 * `occurred_at` 是 svc-finance 侧的 `time.Time`（binding:required）：
 * 日期取自本页选择器，时刻取当前挂钟，序列化成 RFC3339（UTC Z 形式）。
 */
function buildOccurredAt(): string {
  const now = new Date()
  const hh = String(now.getHours()).padStart(2, '0')
  const mm = String(now.getMinutes()).padStart(2, '0')
  const ss = String(now.getSeconds()).padStart(2, '0')
  const iso = new Date(`${occurredDate.value}T${hh}:${mm}:${ss}`).toISOString()
  return Number.isNaN(new Date(iso).getTime()) ? now.toISOString() : iso
}

function handleDateChange(e: any) {
  const value = e?.detail?.value
  if (typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value)) {
    occurredDate.value = value
  }
}

async function loadAccounts() {
  accountsLoading.value = true
  accountsError.value = ''
  try {
    const familyId = await resolveFamilyId()
    if (!familyId) {
      accounts.value = []
      accountsError.value = t('homeos.quickadd.no_family')
      return
    }
    const res = await request.get('/api/finance/accounts', { params: { family_id: familyId } })
    const body = unwrapBody<{ items?: AccountOption[] }>(res)
    // 归档账户不参与记账（4.5.9 归档语义），因此不进选择器
    const list = Array.isArray(body?.items) ? body.items.filter((a) => a && a.id) : []
    accounts.value = list
    if (!list.some((a) => a.id === accountId.value)) {
      accountId.value = list[0]?.id ?? ''
    }
    if (list.length === 0) accountsError.value = t('homeos.quickadd.account_empty')
  } catch (err: any) {
    accounts.value = []
    accountId.value = ''
    accountsError.value = err?.message || t('homeos.quickadd.account_failed')
  } finally {
    accountsLoading.value = false
  }
}

/** 会话家庭 id：首屏 `home/summary` 带出的那一份；没带过就 ensureSession 取一次。 */
async function resolveFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

function selectAccount(account: AccountOption) {
  accountId.value = account.id
  accountsError.value = ''
}

/** 账户类型标签：文案库里有的就译，没有的原样显示服务端枚举（不猜、不填假名）。 */
function accountTypeLabel(type: string): string {
  const key = `homeos.quickadd.account_type_${type}`
  return te(key) ? t(key) : type
}

// ---------------------------------------------------------------- 目标与模式
async function loadTargetFaces() {
  try {
    if (homeStore.modules.length === 0) await homeStore.loadModules()
  } catch (err: any) {
    uni.showToast({ title: err?.message || t('homeos.quickadd.targets_failed'), icon: 'none' })
  }
  // 唯一目标即视为已选（手选路径 ≤2 步：0 步即可开始，17.4）
  if (targetFaces.value.length === 1) {
    selectedFace.value = targetFaces.value[0].code
  }
}

function handleModeChange(mode: InputMode) {
  if (unsupportedModes.includes(mode)) {
    uni.showToast({ title: t('homeos.quickadd.mode_not_open'), icon: 'none' })
    return
  }
  if (mode === 'template' && isFinanceTarget.value) {
    // 当前目标是记账：先把它显式挪到别的面，挪不动（本家庭只挂了记账）就不进模板档
    const next = templateTargetFaces.value[0]
    if (!next) {
      uni.showToast({ title: t('homeos.quickadd.template_unavailable'), icon: 'none' })
      return
    }
    selectedFace.value = next.code
  }
  inputMode.value = mode
}

/** 目标位点击：不可用的那一格给原因，不静默接受一个保存不了的选中态。 */
function chooseFace(code: string) {
  if (isFaceBlocked(code)) {
    uni.showToast({ title: t('homeos.quickadd.template_face_blocked'), icon: 'none' })
    return
  }
  selectedFace.value = code
}

function selectTemplate(label: string) {
  selectedTemplate.value = label
}

/** 有可读内容且目标已选，才能保存；模板档下的记账目标从一开始就选不上。 */
const canSubmit = computed(() => {
  if (!selectedFace.value) return false
  if (unsupportedModes.includes(inputMode.value)) return false
  if (isFaceBlocked(selectedFace.value)) return false
  return amountSource.value.trim().length > 0
})

// ＋ 由 reLaunch 进入时栈深为 1，无栈可退；§17.4 要求取消即回到进入前的页面
function goBack() {
  if (getCurrentPages().length > 1) {
    uni.navigateBack()
  } else {
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  }
}

/** 幂等键：同一份输入在弱网重发时不产生第二条流水（3.4.6、18.2）。 */
/**
 * 幂等键走共享实现 `newIdempotencyUUID()`（utils/format.ts）——
 * 必须是合法 UUID：`finance_transaction.client_request_id` 列类型是 uuid，
 * 旧的 `${Date.now()}-${random}` 串会被 PostgreSQL 拒（SQLSTATE 22P02），
 * 表现为快速添加**每次都 500**。
 */
function buildClientRequestId(): string {
  return newIdempotencyUUID()
}

async function handleSubmit() {
  if (!canSubmit.value) {
    uni.showToast({ title: t('homeos.quickadd.incomplete'), icon: 'none' })
    return
  }

  const cents = parsedAmountCents.value
  const content = amountSource.value.trim()

  // 财务目标：金额是必填业务字段，解析不出来就留在本页，绝不提交 0 分的空流水
  if (isFinanceTarget.value && cents <= 0) {
    amountError.value = unavailableAmountMessage()
    uni.showToast({ title: amountError.value, icon: 'none' })
    return
  }

  submitting.value = true
  try {
    let apiUrl = ''
    let payload: Record<string, unknown> = {}

    if (isFinanceTarget.value) {
      const familyId = await resolveFamilyId()
      if (!familyId) {
        amountError.value = t('homeos.quickadd.no_family')
        uni.showToast({ title: amountError.value, icon: 'none' })
        return
      }
      if (!accountId.value) {
        await loadAccounts()
        if (!accountId.value) {
          amountError.value = accountsError.value || t('homeos.quickadd.account_empty')
          uni.showToast({ title: amountError.value, icon: 'none' })
          return
        }
      }

      // 字段与 svc-finance 的 CreateTransactionRequest 逐一对应（金额单位：整数分）
      apiUrl = '/api/finance/transactions'
      payload = {
        family_id: familyId,
        type: 'expense',
        amount_cents: cents,
        account_id: accountId.value,
        occurred_at: buildOccurredAt(),
        description: remark.value || content,
        client_request_id: buildClientRequestId(),
      }
    } else {
      // 非财务目标 P1 落 homeos 待办（POST /api/homeos/todos，PRD 3.7 已登记）
      apiUrl = '/api/homeos/todos'
      payload = {
        title: remark.value || content,
        due_at: buildOccurredAt(),
        client_request_id: buildClientRequestId(),
      }
    }

    await request.post(apiUrl, payload)

    uni.showToast({ title: t('homeos.quickadd.saved'), icon: 'success' })

    // §2.3 第 3 行「＋ → create → 保存成功」= redirectTo 落地页，不用 navigateBack：
    // 退栈会回到这条已提交的空白表单本身。finance 的落点是已注册的流水列表
    // （pages.json 未注册 finance/transaction/detail，不臆造 URL）；
    // 待办在 P1 无独立详情页，保持进入前上下文。
    setTimeout(() => {
      if (isFinanceTarget.value) {
        uni.redirectTo({ url: '/pages/finance/flow/index' })
      } else {
        goBack()
      }
    }, 1500)
  } catch (err: any) {
    uni.showToast({ title: err?.message || t('homeos.quickadd.save_failed'), icon: 'none' })
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  loadTargetFaces()
  loadAccounts()
})
</script>

<template>
  <view class="hc-page">
    <!-- Header -->
    <view class="page-header">
      <text class="header-title">{{ t('homeos.quickadd.title') }}</text>
      <text class="header-cancel" @click="goBack">{{ t('homeos.quickadd.cancel') }}</text>
    </view>

    <scroll-view class="hc-scroll" scroll-y>
      <!-- Face selector（目标面 = shell 的挂载集合，未启用与未出生的面不出现也不占位，17.8） -->
      <view class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.target_label') }}</text>
        <view v-if="targetFaces.length === 0" class="section-empty">
          <text>{{ homeStore.modulesLoading ? t('homeos.common.loading') : t('homeos.quickadd.target_empty') }}</text>
        </view>
        <view v-else class="face-selector">
          <view
            v-for="face in targetFaces"
            :key="face.code"
            class="face-item"
            :class="{ selected: selectedFace === face.code, disabled: isFaceBlocked(face.code) }"
            @click="chooseFace(face.code)"
          >
            <text>{{ face.name }}</text>
            <!-- 未开放 ≠ 不渲染：这一格在别的输入档下是可用的，此处只是模板档去不了 -->
            <text v-if="isFaceBlocked(face.code)" class="face-badge">{{ t('homeos.common.not_open') }}</text>
          </view>
        </view>
      </view>

      <!-- Input mode selector -->
      <view class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.mode_label') }}</text>
        <view class="mode-selector">
          <view
            v-for="mode in (['text', 'voice', 'photo', 'template'] as InputMode[])"
            :key="mode"
            class="mode-item"
            :class="{ active: inputMode === mode, disabled: unsupportedModes.includes(mode) }"
            @click="handleModeChange(mode)"
          >
            <text class="mode-label">{{ t(`homeos.quickadd.mode.${mode}`) }}</text>
            <text v-if="unsupportedModes.includes(mode)" class="mode-badge">{{ t('homeos.common.not_open') }}</text>
          </view>
        </view>
      </view>

      <!-- Text input -->
      <view v-if="inputMode === 'text'" class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.content_label') }}</text>
        <textarea
          v-model="textInput"
          class="text-input"
          :placeholder="t('homeos.quickadd.content_placeholder')"
          maxlength="500"
        />
      </view>

      <!-- Voice / photo：识别链路未接入，明示未开放并给出可用替代路径 -->
      <view v-if="unsupportedModes.includes(inputMode)" class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.fallback_label') }}</text>
        <view class="section-empty">
          <text>{{ t('homeos.quickadd.mode_not_open') }}</text>
        </view>
      </view>

      <!-- Template input -->
      <view v-if="inputMode === 'template'" class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.template_label') }}</text>
        <text class="section-note">{{ t('homeos.quickadd.template_note') }}</text>
        <view class="template-grid">
          <view
            v-for="tpl in templates"
            :key="tpl.key"
            class="template-item"
            :class="{ selected: selectedTemplate === tpl.label }"
            @click="selectTemplate(tpl.label)"
          >
            <text>{{ tpl.label }}</text>
          </view>
        </view>
      </view>

      <!-- 金额识别预览：目标为财务时常驻，余文与歧义都落在这块 UI 上 -->
      <view v-if="isFinanceTarget" class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.amount_label') }}</text>
        <text v-if="parsedAmountCents > 0" class="amount-ok">
          {{ t('homeos.quickadd.amount_parsed') }} {{ formatAmount(parsedAmountCents) }}
        </text>
        <text v-if="parsedAmountCents > 0 && remark" class="amount-remark">
          {{ t('homeos.quickadd.remark_parsed', { text: remark }) }}
        </text>
        <text v-if="secondAmountText" class="amount-ambiguous">
          {{ t('homeos.quickadd.amount_ambiguous', { amount: secondAmountText, first: formatAmount(parsedAmountCents) }) }}
        </text>
        <text v-else-if="parsedAmountCents <= 0" class="amount-hint">{{ unavailableAmountMessage() }}</text>
        <text v-if="amountError" class="amount-error">{{ amountError }}</text>
      </view>

      <!-- 支出账户：GET /api/finance/accounts?family_id= 的可见选择位 -->
      <view v-if="isFinanceTarget" class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.account_label') }}</text>
        <view v-if="accountsLoading" class="section-empty">
          <text>{{ t('homeos.common.loading') }}</text>
        </view>
        <view v-else-if="accounts.length === 0" class="section-empty">
          <text>{{ accountsError || t('homeos.quickadd.account_empty') }}</text>
        </view>
        <view v-else class="account-selector">
          <view
            v-for="account in accounts"
            :key="account.id"
            class="account-item"
            :class="{ selected: accountId === account.id }"
            @click="selectAccount(account)"
          >
            <text class="account-name">{{ account.name }}</text>
            <text class="account-type">{{ accountTypeLabel(account.type) }}</text>
          </view>
        </view>
      </view>

      <!-- 发生日期：occurred_at 的日期分量 -->
      <view class="form-section">
        <text class="section-label">{{ t('homeos.quickadd.date_label') }}</text>
        <picker mode="date" :value="occurredDate" :start="'2000-01-01'" @change="handleDateChange">
          <view class="date-field">
            <text>{{ occurredDate }}</text>
            <text class="date-arrow">›</text>
          </view>
        </picker>
        <text class="section-note">{{ t('homeos.quickadd.date_note') }}</text>
      </view>
    </scroll-view>

    <!-- Submit button -->
    <view class="submit-bar">
      <button class="submit-btn" :disabled="!canSubmit || submitting" @click="handleSubmit">
        {{ submitting ? t('homeos.quickadd.saving') : t('homeos.common.save') }}
      </button>
    </view>
  </view>
</template>

<style scoped>
.hc-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: var(--bg-secondary);
}

/* Header */
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.header-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.header-cancel {
  font-size: 28rpx;
  color: var(--text-secondary);
}

.hc-scroll {
  flex: 1;
  padding: 24rpx 24rpx 140rpx;
}

/* Form sections */
.form-section {
  margin-bottom: 32rpx;
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.section-label {
  display: block;
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 16rpx;
}

.section-note {
  display: block;
  font-size: 22rpx;
  color: var(--text-tertiary);
  margin-top: 12rpx;
  line-height: var(--line-height-normal);
}

.section-empty {
  padding: 24rpx 0;
  font-size: 26rpx;
  color: var(--text-tertiary);
}

/* Face selector */
.face-selector {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16rpx;
}

.face-item {
  padding: 24rpx 16rpx;
  text-align: center;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-primary);
}

.face-item.selected {
  background-color: var(--color-primary);
  color: var(--color-on-primary);
}

/* 模板档下的记账目标：显式标注不可去，点击给原因（不是置灰后 no-op） */
.face-item.disabled {
  opacity: 0.55;
}

.face-badge {
  display: block;
  font-size: 20rpx;
  color: var(--text-tertiary);
}

.face-item.selected .face-badge {
  color: var(--color-on-primary-muted);
}

/* Mode selector */
.mode-selector {
  display: flex;
  gap: 16rpx;
}

.mode-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
  padding: 24rpx 8rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
}

.mode-item.active {
  background-color: var(--color-primary-light);
  color: var(--color-on-primary);
}

.mode-item.disabled {
  opacity: 0.5;
}

.mode-label {
  font-size: 24rpx;
}

.mode-badge {
  font-size: 20rpx;
  color: var(--text-tertiary);
}

.mode-item.active .mode-badge {
  color: var(--color-on-primary-muted);
}

/* Text input */
.text-input {
  width: 100%;
  min-height: 200rpx;
  padding: 16rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

/* 金额识别预览 */
.amount-ok {
  display: block;
  font-size: 32rpx;
  font-weight: 600;
  color: var(--color-success);
}

.amount-remark {
  display: block;
  font-size: 24rpx;
  color: var(--text-secondary);
  margin-top: 8rpx;
}

/* 歧义呈现：不弹层、不改数，把第二个金额说出来（17.4 的确定性口径） */
.amount-ambiguous {
  display: block;
  font-size: 24rpx;
  color: var(--color-warning);
  margin-top: 8rpx;
  line-height: var(--line-height-normal);
}

.amount-hint {
  display: block;
  font-size: 24rpx;
  color: var(--text-secondary);
  line-height: var(--line-height-normal);
}

.amount-error {
  display: block;
  font-size: 26rpx;
  color: var(--color-error);
  margin-top: 8rpx;
  line-height: var(--line-height-normal);
}

/* Account selector */
.account-selector {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
}

.account-item {
  min-width: 200rpx;
  padding: 20rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  border: 2rpx solid transparent;
}

.account-item.selected {
  background-color: var(--color-primary-light);
  border-color: var(--color-primary);
  color: var(--color-on-primary);
}

.account-name {
  display: block;
  font-size: 28rpx;
}

.account-type {
  display: block;
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.account-item.selected .account-type {
  color: var(--color-on-primary-muted);
}

/* Date picker field */
.date-field {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.date-arrow {
  font-size: 32rpx;
  color: var(--text-tertiary);
}

/* Template grid */
.template-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16rpx;
  margin-top: 12rpx;
}

.template-item {
  padding: 24rpx 8rpx;
  text-align: center;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-primary);
}

.template-item.selected {
  background-color: var(--color-primary);
  color: var(--color-on-primary);
}

/* Submit bar */
.submit-bar {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  padding: 24rpx;
  padding-bottom: calc(24rpx + env(safe-area-inset-bottom));
  background-color: var(--bg-primary);
  border-top: 1rpx solid var(--divider-color);
}

.submit-btn {
  width: 100%;
  padding: 28rpx 0;
  background-color: var(--color-primary);
  color: var(--color-on-primary);
  border-radius: var(--radius-md);
  font-size: 32rpx;
  font-weight: 600;
}

.submit-btn:disabled {
  opacity: 0.6;
}
</style>
