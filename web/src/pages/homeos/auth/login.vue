<script setup lang="ts">
// pages/homeos/auth/login —— 手机号 + 验证码登录（§4.1、PRD 3.4.1、契约 homeos.yaml `/auth/login`）
//
// 本轮改齐的四条口径：
//   1. **请求走 `utils/request`**：本页此前直连 `uni.request`，于是拿不到 Bearer 头、
//      拿不到 401 刷新重试，还把 token 写成了第二套存储口径。现在只有 `request.post` 一条路，
//      token 落盘只有 `access_token` / `refresh_token` 两个键（与 request.ts、「我的」页同一套）。
//   2. **入参字段名按契约**：`homeos.yaml` `/auth/login` 的 requestBody 是
//      `required: [phone, code]`，旧页发的 `sms_code` 服务端从来读不到（binding 直接 400/401）。
//   3. **出参字段名按契约**：200 体是
//      `{access_token, refresh_token, expires_in, family_id, role, pver}`（yaml:143-156），
//      没有 `user`、也没有 `families[]`。旧页读 `res.data.user` / `res.data.families`
//      等于永远读到 `undefined` ⇒ 无论有没有家庭都被判成「没有」，且有家庭的人也被踢去建家页。
//      路由判定只看**契约里那一个字段** `family_id`（yaml:149）：缺失或为 `null` 即「本账号尚无家庭」。
//   4. **登录成功 = 重置栈（无栈替换）**（§2.3 表「登录成功」行）：两个分支都用 `uni.reLaunch`，
//      不用 `redirectTo` —— redirectTo 会把登录页留在栈底，返回手势能退回一张已作废的表单。

import { ref, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, RequestError } from '@/utils/request'
import { useHomeStore } from '@/stores/home'

/** 邀请深链与登录的接力键：family-join 在未登录时写入，本页登录后消费。 */
const PENDING_INVITE_KEY = 'pending_invite_code'

/** 只取契约 `/auth/login` 200 体里登记的那几个字段，不猜额外字段。 */
interface LoginResponse {
  access_token?: string
  refresh_token?: string
  expires_in?: number
  family_id?: string | null
  role?: string
  pver?: number
}

interface SmsCodeResponse {
  expires_in?: number
  channel?: string
  message?: string
}

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

const phone = ref('')
const code = ref('')
const countdown = ref(0)
const sending = ref(false)
const submitting = ref(false)
/** 页内错误条：校验失败与请求失败都落在这里，不只在 toast 里一闪（第八章：错误可读可重试）。 */
const errorText = ref('')

let timer: ReturnType<typeof setInterval> | null = null

/**
 * 开发期自动填充的测试码。`import.meta.env.DEV` 由 vite 在**构建期**静态替换：
 * `npm run build:h5` 产物里它是字面量 `false`，整个表达式连同 `VITE_SMS_TEST_CODE`
 * 一起被摇掉 ⇒ 生产 H5 里既没有这段逻辑也没有这个键。
 * 测试码本身在仓库内不留字面量，只从环境变量注入（未注入即整条 dev 通道不存在），
 * 因此「把固定测试验证码摆给终端用户」这类文案不可能出现在生产模板里。
 */
const devTestCode: string = import.meta.env.DEV
  ? String((import.meta.env as Record<string, any>).VITE_SMS_TEST_CODE || '')
  : ''

const PHONE_RE = /^1[3-9]\d{9}$/

function stopTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

function startCountdown() {
  countdown.value = 60
  stopTimer()
  timer = setInterval(() => {
    countdown.value -= 1
    if (countdown.value <= 0) stopTimer()
  }, 1000)
}

/** 请求失败文案：优先服务端 message，其次 401 的专属文案，最后网络层原因。 */
function describeError(err: unknown, fallbackKey: string): string {
  if (err instanceof RequestError) {
    if (err.status === 401) return t('homeos.login.code_invalid')
    const body = err.body as { message?: string; error?: string } | undefined
    return body?.message || body?.error || err.message || t(fallbackKey)
  }
  return (err as Error)?.message || t(fallbackKey)
}

async function sendCode() {
  if (!PHONE_RE.test(phone.value)) {
    errorText.value = t('homeos.login.phone_invalid')
    return
  }
  if (sending.value || countdown.value > 0) return

  errorText.value = ''
  sending.value = true
  try {
    await request.post<SmsCodeResponse>('/api/homeos/auth/sms-code', { phone: phone.value })
    startCountdown()
    uni.showToast({ title: t('homeos.login.code_sent'), icon: 'none' })
    if (devTestCode) {
      code.value = devTestCode
      uni.showToast({ title: t('homeos.login.dev_code_filled'), icon: 'none' })
    }
  } catch (err: any) {
    errorText.value = describeError(err, 'homeos.login.code_failed')
  } finally {
    sending.value = false
  }
}

/**
 * 「有家庭」的唯一判据（契约 `homeos.yaml:149` 的 `family_id`）：
 * 该 schema 块没有 `required`、也没有 `nullable`，所以「未建家」在服务端有两种等价表达 ——
 * 键**缺席** 或 键为 `null`（队友的 onboarding token 用的是 `family_id: null`）。
 * 两种都算「无家庭」，其余任何非空字符串都算「有会话家庭」。
 */
function sessionFamilyOf(body: LoginResponse | null | undefined): string {
  const value = body?.family_id
  return typeof value === 'string' && value.trim() ? value.trim() : ''
}

function pendingInviteCode(): string {
  const stored = uni.getStorageSync(PENDING_INVITE_KEY)
  return typeof stored === 'string' && stored.trim() ? stored.trim() : ''
}

async function submit() {
  if (!phone.value.trim() || !code.value.trim()) {
    errorText.value = t('homeos.login.incomplete')
    return
  }
  if (submitting.value) return

  errorText.value = ''
  submitting.value = true
  try {
    // 字段名逐一对齐契约的 `{phone, code}`（yaml:125 的 required 列表）
    const body = await request.post<LoginResponse>('/api/homeos/auth/login', {
      phone: phone.value.trim(),
      code: code.value.trim(),
    })

    const accessToken = body?.access_token
    const refreshToken = body?.refresh_token
    if (!accessToken) {
      // 200 但没有 token：契约外响应，不猜、不静默跳首页（第八章：不假装成功）
      errorText.value = t('homeos.login.bad_response')
      return
    }

    // 单点存储口径：键名与 utils/request.ts 读写的完全一致
    uni.setStorageSync('access_token', accessToken)
    if (refreshToken) uni.setStorageSync('refresh_token', refreshToken)
    uni.setStorageSync('phone', phone.value.trim())

    // 上一个账号的 shell 快照（家庭、角色、period、面集合）不能留到新会话里
    homeStore.clearData()

    const sessionFamilyId = sessionFamilyOf(body)
    if (sessionFamilyId) homeStore.sessionFamilyId = sessionFamilyId
    if (body?.role) homeStore.role = body.role as any

    // 邀请在登录后续接（定版流程「登录后接受邀请」）：先回到那条待处理的邀请，
    // 由 family-join 决定是直接加入还是提示切换家庭。
    const invite = pendingInviteCode()
    if (invite) {
      uni.reLaunch({
        url: `/pages/homeos/auth/family-join?invite_code=${encodeURIComponent(invite)}`,
      })
      return
    }

    if (!sessionFamilyId) {
      // 无家庭：登录本身已成功（onboarding 作用域 token），引导去建家（§4.1 引导链）
      uni.reLaunch({ url: '/pages/homeos/family/create' })
      return
    }

    // 有家庭：进 shell 一级页（§2.3「登录成功 = 重置栈」）
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  } catch (err: any) {
    // 服务端尚未改齐 onboarding 口径时（svc-homeos 现在仍回 403 `no_family`）也照样引导：
    // token 已在这一分支前不落盘，故只把用户送到建家页，不谎报「已登录」。
    if (err instanceof RequestError && err.status === 403) {
      const body = err.body as { error?: string } | undefined
      if (body?.error === 'no_family') {
        errorText.value = t('homeos.login.need_family')
        uni.reLaunch({ url: '/pages/homeos/family/create' })
        return
      }
    }
    errorText.value = describeError(err, 'homeos.login.failed')
  } finally {
    submitting.value = false
  }
}

onUnmounted(() => stopTimer())
</script>

<template>
  <view class="login-page">
    <view class="login-header">
      <text class="app-name">{{ t('homeos.login.app_name') }}</text>
      <text class="app-slogan">{{ t('homeos.login.slogan') }}</text>
    </view>

    <view class="login-form">
      <view class="input-group">
        <text class="label">{{ t('homeos.login.phone_label') }}</text>
        <input
          v-model="phone"
          type="number"
          :placeholder="t('homeos.login.phone_placeholder')"
          maxlength="11"
          class="input-field"
        />
      </view>

      <view class="input-group">
        <text class="label">{{ t('homeos.login.code_label') }}</text>
        <view class="code-input-wrapper">
          <input
            v-model="code"
            type="number"
            :placeholder="t('homeos.login.code_placeholder')"
            maxlength="6"
            class="input-field code-input"
          />
          <button
            class="send-code-btn"
            :disabled="countdown > 0 || sending"
            @click="sendCode"
          >
            {{ countdown > 0 ? t('homeos.login.resend_in', { seconds: countdown }) : t('homeos.login.send_code') }}
          </button>
        </view>
      </view>

      <text v-if="errorText" class="form-error">{{ errorText }}</text>

      <button class="login-btn" :disabled="submitting" @click="submit">
        {{ submitting ? t('homeos.login.submitting') : t('homeos.login.submit') }}
      </button>

      <!-- 仅 DEV 构建存在的提示：生产 H5 里 `devTestCode` 恒为空串，整个节点不进模板 -->
      <view v-if="devTestCode" class="tips">
        <text class="tip-text">{{ t('homeos.login.dev_hint') }}</text>
      </view>
    </view>
  </view>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-dark) 100%);
  padding: 60rpx 40rpx;
}

.login-header {
  text-align: center;
  margin-bottom: 80rpx;
}

.app-name {
  font-size: 48rpx;
  font-weight: bold;
  color: var(--color-on-primary);
  display: block;
  margin-bottom: 20rpx;
}

.app-slogan {
  font-size: 28rpx;
  color: var(--color-on-primary-muted);
}

.login-form {
  background: var(--bg-primary);
  border-radius: 20rpx;
  padding: 40rpx;
  box-shadow: var(--shadow-lg);
}

.input-group {
  margin-bottom: 30rpx;
}

.label {
  font-size: 28rpx;
  color: var(--text-primary);
  margin-bottom: 10rpx;
  display: block;
}

.input-field {
  width: 100%;
  height: 80rpx;
  border: 2rpx solid var(--border-color);
  border-radius: 10rpx;
  padding: 0 20rpx;
  font-size: 28rpx;
  box-sizing: border-box;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.code-input-wrapper {
  display: flex;
  gap: 20rpx;
}

.code-input {
  flex: 1;
}

.send-code-btn {
  width: 220rpx;
  height: 80rpx;
  line-height: 80rpx;
  background: var(--color-primary);
  color: var(--color-on-primary);
  border: none;
  border-radius: 10rpx;
  font-size: 26rpx;
}

.send-code-btn[disabled] {
  background: var(--bg-tertiary);
  color: var(--text-tertiary);
}

.login-btn {
  width: 100%;
  height: 90rpx;
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-dark) 100%);
  color: var(--color-on-primary);
  border: none;
  border-radius: 10rpx;
  font-size: 32rpx;
  font-weight: bold;
  margin-top: 40rpx;
}

.form-error {
  display: block;
  font-size: 26rpx;
  color: var(--color-error);
  margin-bottom: 20rpx;
  line-height: var(--line-height-normal);
}

.tips {
  margin-top: 30rpx;
  text-align: center;
}

.tip-text {
  font-size: 24rpx;
  color: var(--text-tertiary);
}
</style>
