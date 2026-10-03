<script setup lang="ts">
// pages/homeos/auth/family-join —— 加入家庭（邀请码 / 链接 / 二维码三态合一，§4.1 row ③）
//
// 口径：
//   · 本页是 §2.1 具名动作段封闭表里的 `family-join`，也是 `family/invite.vue` 生成的那条
//     链接（`/#/pages/homeos/auth/family-join?invite_code=…`）与深链
//     `homecube://homeos/family/{code}` 的落点 —— 此前两端都指向一个不存在的路由位（死链）。
//   · 三态共用同一个入参：`homeos_invitations.code`。链接与二维码都只是它的载体，
//     所以本页只认一个字段 `invite_code`，手动输码走的是同一个接口。
//   · 定版流程「登录后接受邀请」：未登录时把 code 暂存 `pending_invite_code` 再压栈进登录页，
//     登录成功后由登录页消费同一个 code 回到本页（§2.3「一级页 → 二级页 = 压栈」）。
//   · 已有家庭的登录态**不静默加入即切换**：加入前说明清楚，加入后由用户选「切过去」还是
//     「先不切」（家庭切换是顶栏面板语义，§4.1；本页只是它的一个入口场景）。
//   · 加入成功 = §2.3「家庭切换/深链冷启动 = 重置栈」→ `uni.reLaunch` 进 shell 一级页。
//   · 状态位类名走第八章：hc-skeleton / hc-state-error / hc-state-empty / hc-state-denied。
//   · 服务端 `POST /api/homeos/family/invite/accept` 已实现（svc-homeos family.go），
//     但该路径未登记进 `server/contracts/openapi/homeos.yaml` —— 已作为契约缺口上报。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, unwrapBody, RequestError } from '@/utils/request'
import { useHomeStore } from '@/stores/home'

const PENDING_INVITE_KEY = 'pending_invite_code'

interface AcceptInviteResponse {
  family_id?: string
  family_name?: string
  role?: string
  message?: string
}

/** `POST /api/homeos/family/switch`：加入后把会话切到新家庭（token 的 family_id 会一起换）。 */
interface SwitchFamilyResponse {
  access_token?: string
  refresh_token?: string
  family_id?: string
  role?: string
}

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

const booting = ref(true)
const joining = ref(false)
const switching = ref(false)
/** 会话读取失败（不是加入失败）→ 整页错误态 + 重试，与 family/invite 同一口径。 */
const errorText = ref('')
const errorCode = ref('')
/** 加入动作本身的失败，留在表单上方回显，不吞。 */
const joinError = ref('')

const inviteCode = ref('')
const loggedIn = ref(false)
const hasFamily = ref(false)
const currentFamilyName = ref('')
const joinedFamily = ref<AcceptInviteResponse | null>(null)

const codeReady = computed(() => inviteCode.value.trim().length > 0)
const busy = computed(() => joining.value || switching.value)

/** 受邀角色标签：文案库里有的就译，没有的原样显示服务端枚举（不猜、不填假名）。 */
function roleLabel(role?: string): string {
  if (!role) return ''
  const key = `homeos.family.role_${role}`
  const label = t(key)
  return label === key ? role : label
}

/** 加入回执：家庭名与角色都来自服务端，缺角色时不硬凑一句带「身份：」的话。 */
const joinedSummary = computed(() => {
  const accepted = joinedFamily.value
  if (!accepted) return ''
  const name = accepted.family_name || t('homeos.family.no_family')
  const role = roleLabel(accepted.role)
  return role
    ? t('homeos.join.joined_named', { name, role })
    : t('homeos.join.joined_unnamed', { name })
})

// ---------------------------------------------------------------- 入参：三态与深链
/** uni-app 的查询参数口径与 `legal/detail.vue` 一致：读当前页实例的 `options`。 */
function pageOptions(): Record<string, any> {
  const pages = getCurrentPages()
  const current = pages[pages.length - 1] as any
  return (current?.options || {}) as Record<string, any>
}

/**
 * 深链 `homecube://{code}/{entity}/{id}`（§2.1、§2.5）。邀请落点是
 * `homecube://homeos/family/{code}`；服务端二维码载荷当前是 `homecube://invite/{code}`
 * （svc-homeos `repo/identity.go`），两种都认 —— 差异已作为定版冲突上报。
 */
function codeFromDeepLink(raw: string): string {
  const hit = raw.match(/^homecube:\/\/(?:homeos\/family|invite)\/([^/?#]+)/i)
  return hit ? safeDecode(hit[1]) : ''
}

function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function readInviteCode(): string {
  const opts = pageOptions()
  const direct = opts.invite_code || opts.code || opts.invite
  if (direct) return String(direct)

  // 深链整串：H5 冷启动可能挂在 q / url / source 任一参数上
  for (const key of ['q', 'url', 'source', 'link']) {
    const raw = typeof opts[key] === 'string' ? safeDecode(opts[key] as string) : ''
    const hit = codeFromDeepLink(raw)
    if (hit) return hit
  }
  for (const value of Object.values(opts)) {
    if (typeof value === 'string') {
      const hit = codeFromDeepLink(safeDecode(value))
      if (hit) return hit
    }
  }

  // 登录后续接：登录页消费前留在存储里
  const pending = uni.getStorageSync(PENDING_INVITE_KEY)
  return typeof pending === 'string' ? pending.trim() : ''
}

function clearPendingInvite() {
  uni.removeStorageSync(PENDING_INVITE_KEY)
}

// ---------------------------------------------------------------- 会话态
function classifyError(err: any): { text: string; code: string } {
  const msg: string = err?.message || t('homeos.join.load_failed')
  const hit = String(msg).match(/HTTP\s+(\d{3})/)
  return { text: msg, code: hit ? hit[1] : 'NETWORK' }
}

async function boot(): Promise<void> {
  booting.value = true
  errorText.value = ''
  errorCode.value = ''
  joinError.value = ''

  try {
    inviteCode.value = readInviteCode()
    loggedIn.value = !!uni.getStorageSync('access_token')

    if (loggedIn.value) {
      await homeStore.ensureSession()
      hasFamily.value = !!homeStore.sessionFamilyId
      currentFamilyName.value = homeStore.family?.name || ''
    }
  } catch (err: any) {
    const { text, code } = classifyError(err)
    errorText.value = text
    errorCode.value = code
  } finally {
    booting.value = false
  }
}

/** 未登录：把 code 留在存储里交给登录页续接，再压栈进登录页（定版流程「登录后接受邀请」）。 */
function goLogin(): void {
  if (inviteCode.value.trim()) {
    uni.setStorageSync(PENDING_INVITE_KEY, inviteCode.value.trim())
  }
  uni.navigateTo({ url: '/pages/homeos/auth/login' })
}

async function switchTo(familyId: string): Promise<boolean> {
  try {
    const res = await request.post<unknown>('/api/homeos/family/switch', { family_id: familyId })
    const body = unwrapBody<SwitchFamilyResponse>(res)
    if (body?.access_token) {
      uni.setStorageSync('access_token', body.access_token)
      if (body.refresh_token) uni.setStorageSync('refresh_token', body.refresh_token)
    }
    homeStore.clearData()
    homeStore.sessionFamilyId = body?.family_id || familyId
    if (body?.role) homeStore.role = body.role as any
    return true
  } catch (err: any) {
    const { text } = classifyError(err)
    errorText.value = text
    errorCode.value = String(err instanceof RequestError ? err.status : 'NETWORK')
    return false
  }
}

/** §2.3 加入成功 = 重置栈（无栈替换）：栈里不能留下登录页与本页。 */
function enterShell() {
  clearPendingInvite()
  uni.reLaunch({ url: '/pages/homeos/home/index' })
}

async function accept(switchAfterJoin: boolean): Promise<void> {
  const code = inviteCode.value.trim()
  if (!code) {
    joinError.value = t('homeos.join.code_missing')
    return
  }
  if (!loggedIn.value) {
    goLogin()
    return
  }
  if (busy.value) return

  joinError.value = ''
  joining.value = true
  try {
    const res = await request.post<unknown>('/api/homeos/family/invite/accept', {
      invite_code: code,
    })
    const body = unwrapBody<AcceptInviteResponse>(res)
    const familyId = typeof body?.family_id === 'string' ? body.family_id : ''
    if (!familyId) {
      // 契约外响应：不猜加入成功（第八章「不假装成功」）
      joinError.value = t('homeos.join.bad_response')
      return
    }

    joinedFamily.value = { ...body, family_id: familyId }
    // 家庭名来自服务端回执；缺名时不写「已加入「」」这种半截话
    uni.showToast({
      title: body.family_name
        ? t('homeos.join.joined', { name: body.family_name })
        : t('homeos.join.joined_noname'),
      icon: 'none',
    })

    // 无家庭时必须先切过去才有家庭作用域的 token；有家庭时按用户显式选择
    if (!hasFamily.value || switchAfterJoin) {
      switching.value = true
      const ok = await switchTo(familyId)
      switching.value = false
      if (!ok) {
        joinError.value = t('homeos.join.switch_failed')
        return
      }
      enterShell()
      return
    }

    // 只加入不切换：新家庭进的是顶栏家庭面板（§4.1），本页把用户送回 shell
    enterShell()
  } catch (err: any) {
    if (err instanceof RequestError && err.status === 404) {
      joinError.value = t('homeos.join.not_found')
    } else if (err instanceof RequestError && err.status === 400) {
      joinError.value = t('homeos.join.code_invalid')
    } else if (err instanceof RequestError && err.status === 401) {
      joinError.value = t('homeos.join.token_missing')
    } else {
      const { text } = classifyError(err)
      joinError.value = text
    }
  } finally {
    joining.value = false
    switching.value = false
  }
}

function goBack(): void {
  if (getCurrentPages().length > 1) {
    uni.navigateBack()
  } else {
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  }
}

/** 查询参数在页面实例入栈后才可读（与 `legal/detail.vue` 同一口径），故挂载后再取。 */
onMounted(() => {
  boot()
})
</script>

<template>
  <view class="hc-page">
    <view class="page-head">
      <text class="head-back" @click="goBack">‹</text>
      <view class="head-titles">
        <text class="head-title">{{ t('homeos.join.title') }}</text>
        <text class="head-sub">{{ t('homeos.join.subtitle') }}</text>
      </view>
      <text class="head-placeholder" />
    </view>

    <view v-if="booting" class="hc-skeleton">
      <view class="sk-row" />
      <view class="sk-row" />
    </view>

    <view v-else-if="errorText && !joinError" class="hc-state-error">
      <text class="state-title">{{ t('homeos.join.load_failed') }}</text>
      <text class="state-hint">{{ errorText }}</text>
      <text class="state-code">{{ t('homeos.family.error_code') }}：{{ errorCode }}</text>
      <button class="state-btn" @click="boot">{{ t('homeos.common.retry') }}</button>
    </view>

    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- 三态合一：链接 / 二维码解码 / 手动输码都落到同一个字段 -->
      <view class="card">
        <text class="card-title">{{ t('homeos.join.code_label') }}</text>
        <input
          v-model="inviteCode"
          class="code-input"
          :placeholder="t('homeos.join.code_placeholder')"
          maxlength="32"
        />
        <text class="card-note">
          {{ inviteCode ? t('homeos.join.code_from_link') : t('homeos.join.code_manual_hint') }}
        </text>
      </view>

      <!-- 未登录：先登录，登录后回到本页续接同一条邀请 -->
      <view v-if="!loggedIn" class="card">
        <text class="card-title">{{ t('homeos.join.need_login_title') }}</text>
        <text class="card-note">{{ t('homeos.join.need_login_hint') }}</text>
        <button class="primary-btn" @click="goLogin">{{ t('homeos.join.need_login_action') }}</button>
      </view>

      <!-- 已登录且已有家庭：不静默切换，加入后由用户选 -->
      <view v-else-if="hasFamily" class="card">
        <text class="card-title">{{ t('homeos.join.has_family_title') }}</text>
        <text class="card-note">{{ t('homeos.join.has_family_hint') }}</text>
        <button class="primary-btn" :disabled="busy || !codeReady" @click="accept(true)">
          {{ switching ? t('homeos.join.switching') : t('homeos.join.accept_and_switch') }}
        </button>
        <button class="ghost-btn" :disabled="busy || !codeReady" @click="accept(false)">
          {{ joining ? t('homeos.join.joining') : t('homeos.join.accept_only') }}
        </button>
      </view>

      <!-- 已登录且无家庭（onboarding 作用域）：加入即进入 -->
      <view v-else class="card">
        <text class="card-title">{{ t('homeos.join.no_family_title') }}</text>
        <text class="card-note">{{ t('homeos.join.no_family_hint') }}</text>
        <button
          class="primary-btn"
          :disabled="busy || !codeReady"
          @click="accept(true)"
        >
          {{ joining || switching ? t('homeos.join.joining') : t('homeos.join.accept') }}
        </button>
      </view>

      <view v-if="joinError" class="hc-state-error inline">
        <text class="state-title">{{ t('homeos.join.join_failed_title') }}</text>
        <text class="state-hint">{{ joinError }}</text>
      </view>

      <view v-if="joinedFamily" class="hc-state-empty inline">
        <text class="state-hint">{{ joinedSummary }}</text>
      </view>

      <text v-if="!loggedIn || hasFamily || joinedFamily" class="page-footnote">
        {{ t('homeos.join.footnote') }}
      </text>
    </scroll-view>
  </view>
</template>

<style scoped>
.hc-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: var(--bg-secondary);
}

.page-head {
  display: flex;
  align-items: center;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.head-back {
  font-size: 44rpx;
  line-height: 1;
  color: var(--text-secondary);
  padding: 0 16rpx;
}

.head-titles {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4rpx;
}

.head-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.head-sub {
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.head-placeholder {
  width: 76rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

.hc-skeleton {
  flex: 1;
  padding: 24rpx;
  display: flex;
  flex-direction: column;
  gap: 16rpx;
}

.sk-row {
  height: 200rpx;
  border-radius: var(--radius-md);
  background-color: var(--bg-tertiary);
}

.hc-state-empty,
.hc-state-error,
.hc-state-denied {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16rpx;
  padding: 96rpx 48rpx;
}

.hc-state-empty.inline,
.hc-state-error.inline {
  flex: none;
  padding: 32rpx 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  margin-top: 24rpx;
}

.state-title {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.state-hint {
  font-size: 26rpx;
  color: var(--text-secondary);
  text-align: center;
  line-height: var(--line-height-normal);
}

.state-code {
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.state-btn {
  margin-top: 16rpx;
  padding: 16rpx 48rpx;
  background-color: var(--color-primary);
  color: var(--color-on-primary);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.card {
  padding: 28rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  display: flex;
  flex-direction: column;
  gap: 16rpx;
}

.card-title {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.card-note {
  font-size: 24rpx;
  color: var(--text-secondary);
  line-height: var(--line-height-normal);
}

.code-input {
  height: 80rpx;
  padding: 0 20rpx;
  border: 2rpx solid var(--border-color);
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
  font-size: 28rpx;
}

.primary-btn {
  background-color: var(--color-primary);
  color: var(--color-on-primary);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.primary-btn[disabled] {
  opacity: 0.6;
}

.ghost-btn {
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
  border: 2rpx solid var(--border-color);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.page-footnote {
  display: block;
  padding: 8rpx 8rpx 40rpx;
  font-size: 22rpx;
  color: var(--text-tertiary);
  line-height: var(--line-height-normal);
}
</style>
