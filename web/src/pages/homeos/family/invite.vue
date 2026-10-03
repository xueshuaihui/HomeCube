<script setup lang="ts">
// pages/homeos/family/invite —— 邀请成员（§4.6 row ③「三态生成 + 有效期 + 撤销弹层」）
//
// 口径：
//   · 权限前提：管理员。15.1「邀请仅 owner 可发起」、15.3 的 invite 动作缺失 → **控件不渲染**，
//     非管理员进本页直接落第八章的 hc-state-denied，不给半成品表单。
//   · 角色**不在本页推导**：读 shell store 的会话快照（`homeStore.role`，唯一源 `ensureSession()`），
//     与「我的」页同一份。本页此前自己再调一次 `/families` 并以 `|| 'member'` 兜底 ——
//     「未知」被当成成人成员后，管理员门禁的判断依据就换了主人（15.1 要求未知即最小权限）。
//   · 生成：`POST /api/homeos/members/invite`（PRD 3.7）。三态（链接 / 二维码 / 邀请码）
//     **共用同一入参口径**：`homeos_invitations.code`，接受端 `POST /api/homeos/family/invite/accept`
//     收的就是 `{ invite_code }` 一个字段——链接与二维码只是这个 code 的两种载体。
//   · 有效期：`homeos_invitations.expires_at` NOT NULL，故发起时必须带时长档位（PRD 只给了
//     访客默认 30 天，15.1，未枚举档位；本卡的三档是实现选择，已作为定版空白上报）。
//   · 待处理邀请列表：读 `GET /api/homeos/family/invites`（与 3.7 登记的
//     `DELETE /api/homeos/family/invites/{id}` 同一条资源）。**该读接口未登记在 PRD 3.7**，
//     服务端也未出生 → 本页对它渲染失败态 + 重试，不造假数据（已上报）。
//   · 撤销：破坏性确认走弹层，不占路由（§2.2、17.7 第 7 条）。
//   · 二维码：`web/package.json` 没有 QR 依赖，且本卡不得引依赖 → 二维码格显式标注「待接入」，
//     同一条链接用「复制链接」兜底。这是依赖决策，不是页面缺陷。
//   · 状态位类名走第八章：hc-skeleton / hc-state-error / hc-state-empty / hc-state-denied。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, unwrapBody } from '@/utils/request'
import { useHomeStore } from '@/stores/home'
import { formatDate } from '@/utils/format'

type MemberRole = 'owner' | 'member' | 'ward' | 'guest'

interface InviteRow {
  id: string
  code: string
  link?: string
  role?: MemberRole
  status?: string
  invitee_phone?: string
  expires_at?: string
  created_at?: string
}

/** 有效期档位（天）：默认 30 天对齐 PRD 15.1 的访客默认有效期。 */
const EXPIRY_OPTIONS = [1, 7, 30]
const DEFAULT_EXPIRY_DAYS = 30

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

const booting = ref(true)
const denied = ref(false)
const errorText = ref('')
const errorCode = ref('')

const invites = ref<InviteRow[]>([])
const generating = ref(false)
const revokeTarget = ref<InviteRow | null>(null)
const revoking = ref(false)

// 表单（页面局部状态，随页面销毁，§2.3 页面内状态归属）
const inviteRole = ref<Exclude<MemberRole, 'owner'>>('member')
const expiryDays = ref<number>(DEFAULT_EXPIRY_DAYS)
const inviteePhone = ref('')
const created = ref<InviteRow | null>(null)

const ROLE_OPTIONS: Array<Exclude<MemberRole, 'owner'>> = ['member', 'ward', 'guest']

/**
 * 管理员判定只读 shell 会话快照（`homeStore.role`，唯一源 = `ensureSession()`）：
 * `role` 为空串（会话未取到）时 `isAdmin` 即 false，本页整页落 hc-state-denied ——
 * 未知即按最小权限，不按「member」猜（15.1，与「我的」页同一条口径）。
 */
const isAdmin = computed(() => homeStore.isAdmin)

function roleLabel(role?: MemberRole): string {
  if (!role) return ''
  const key = `homeos.family.role_${role}`
  const label = t(key)
  return label === key ? role : label
}

function classifyError(err: any): { text: string; code: string } {
  const msg: string = err?.message || t('homeos.invite.load_failed')
  const hit = msg.match(/HTTP\s+(\d{3})/)
  return { text: msg, code: hit ? hit[1] : 'NETWORK' }
}

/**
 * 三态合一的落地页是 §4.1 的 `homeos/auth/family-join`（邀请码/链接/二维码同一入参口径）。
 * 服务端下发 link 时优先用服务端值；否则按同一个 code 本地拼出同一形状。
 * 该路由位本轮已出生并登记进 `pages.json`（此前它既不在 pages.json 也不在磁盘上，
 * 复制出去的链接是死链）；本页只负责拼出链接，落地页读的就是 `invite_code` 这一个入参。
 */
function buildInviteLink(code: string): string {
  const path = '/#/pages/homeos/auth/family-join?invite_code='
  const origin = typeof window !== 'undefined' && window.location ? window.location.origin : ''
  return `${origin}${path}${encodeURIComponent(code)}`
}

function normalizeInvite(raw: any): InviteRow | null {
  if (!raw) return null
  const code: string = raw.code || raw.invite_code || ''
  const id: string = String(raw.id || raw.invite_id || '')
  if (!id && !code) return null
  return {
    id,
    code,
    link: raw.link || raw.url || raw.invite_url || (code ? buildInviteLink(code) : ''),
    role: raw.role,
    status: raw.status,
    invitee_phone: raw.invitee_phone,
    expires_at: raw.expires_at,
    created_at: raw.created_at,
  }
}

// ---------------------------------------------------------------- 取数
async function loadInvites(): Promise<void> {
  const res = await request.get('/api/homeos/family/invites', { params: { status: 'pending' } })
  const body = unwrapBody(res)
  const raw: any[] = Array.isArray(body?.items)
    ? body.items
    : Array.isArray(body?.invites)
      ? body.invites
      : Array.isArray(body)
        ? body
        : []
  invites.value = raw.map(normalizeInvite).filter((x): x is InviteRow => !!x)
}

async function load(): Promise<void> {
  booting.value = true
  errorText.value = ''
  errorCode.value = ''
  denied.value = false

  try {
    // 会话快照（当前家庭 + 本人角色）只由 shell store 取一次；本页不再另发 /families
    await homeStore.ensureSession()
    if (!isAdmin.value) {
      denied.value = true
      invites.value = []
      return
    }
    await loadInvites()
  } catch (err: any) {
    const { text, code } = classifyError(err)
    errorText.value = text
    errorCode.value = code
  } finally {
    booting.value = false
  }
}

// ---------------------------------------------------------------- 生成三态
async function generate(): Promise<void> {
  if (generating.value) return
  generating.value = true
  errorText.value = ''

  try {
    const payload: Record<string, any> = {
      role: inviteRole.value,
      expires_in_days: expiryDays.value,
    }
    if (inviteePhone.value.trim()) payload.invitee_phone = inviteePhone.value.trim()

    const res = await request.post('/api/homeos/members/invite', payload)
    const row = normalizeInvite(unwrapBody(res))

    if (!row || !row.code) {
      uni.showToast({ title: t('homeos.invite.bad_response'), icon: 'none' })
      return
    }

    created.value = row
    inviteePhone.value = ''
    uni.showToast({ title: t('homeos.invite.created'), icon: 'success' })

    // 名册刷新失败不打断生成结果：三态已经在页面上
    try {
      await loadInvites()
    } catch {
      /* 列表读接口未出生时保留刚生成的这一条可见性 */
      invites.value = [row, ...invites.value.filter((i) => i.code !== row.code)]
    }
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  } finally {
    generating.value = false
  }
}

function copy(text: string): void {
  if (!text) return
  uni.setClipboardData({
    data: text,
    success: () => uni.showToast({ title: t('homeos.invite.copied'), icon: 'none' }),
    fail: () => uni.showToast({ title: t('homeos.invite.copy_failed'), icon: 'none' }),
  })
}

// ---------------------------------------------------------------- 撤销弹层（§2.2）
function askRevoke(row: InviteRow): void {
  revokeTarget.value = row
}

function cancelRevoke(): void {
  revokeTarget.value = null
}

async function confirmRevoke(): Promise<void> {
  const target = revokeTarget.value
  if (!target || revoking.value) return
  revoking.value = true

  try {
    await request.delete(`/api/homeos/family/invites/${target.id || target.code}`)
    revokeTarget.value = null
    if (created.value && created.value.code === target.code) created.value = null
    uni.showToast({ title: t('homeos.invite.revoked'), icon: 'success' })
    try {
      await loadInvites()
    } catch {
      invites.value = invites.value.filter((i) => i.code !== target.code)
    }
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  } finally {
    revoking.value = false
  }
}

function isExpired(row: InviteRow): boolean {
  if (!row.expires_at) return false
  return new Date(row.expires_at).getTime() < Date.now()
}

function goBack(): void {
  if (getCurrentPages().length > 1) {
    uni.navigateBack()
  } else {
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  }
}

onMounted(() => {
  load()
})
</script>

<template>
  <view class="hc-page">
    <view class="page-head">
      <text class="head-back" @click="goBack">‹</text>
      <view class="head-titles">
        <text class="head-title">{{ t('homeos.invite.title') }}</text>
        <text class="head-sub">{{ t('homeos.invite.subtitle') }}</text>
      </view>
      <text class="head-placeholder" />
    </view>

    <view v-if="booting" class="hc-skeleton">
      <view class="sk-row" />
      <view class="sk-row" />
    </view>

    <!-- 无权限：不给半成品表单（第八章） -->
    <view v-else-if="denied" class="hc-state-denied">
      <text class="state-title">{{ t('homeos.family.denied_title') }}</text>
      <text class="state-hint">{{ t('homeos.invite.denied_hint') }}</text>
      <button class="state-btn" @click="goBack">{{ t('homeos.family.back') }}</button>
    </view>

    <!-- 失败：可重试 + 错误码可见 -->
    <view v-else-if="errorText" class="hc-state-error">
      <text class="state-title">{{ t('homeos.invite.load_failed') }}</text>
      <text class="state-hint">{{ errorText }}</text>
      <text class="state-code">{{ t('homeos.family.error_code') }}：{{ errorCode }}</text>
      <button class="state-btn" @click="load">{{ t('homeos.family.retry') }}</button>
    </view>

    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- 生成表单 -->
      <view class="card">
        <text class="card-title">{{ t('homeos.invite.generate') }}</text>

        <view class="field">
          <text class="field-label">{{ t('homeos.invite.role') }}</text>
          <picker
            mode="selector"
            :range="[t('homeos.family.role_member'), t('homeos.family.role_ward'), t('homeos.family.role_guest')]"
            :value="ROLE_OPTIONS.indexOf(inviteRole)"
            @change="(e: any) => inviteRole = ROLE_OPTIONS[e.detail.value]"
          >
            <view class="field-picker">
              <text>{{ roleLabel(inviteRole) }}</text>
              <text class="chevron">›</text>
            </view>
          </picker>
        </view>

        <view class="field">
          <text class="field-label">{{ t('homeos.invite.expiry') }}</text>
          <view class="segmented">
            <view
              v-for="d in EXPIRY_OPTIONS"
              :key="d"
              class="segment"
              :class="{ active: expiryDays === d }"
              @click="expiryDays = d"
            >
              <text>{{ t('homeos.invite.expiry_days', { days: d }) }}</text>
            </view>
          </view>
          <text class="field-note">{{ t('homeos.invite.expiry_note') }}</text>
        </view>

        <view class="field">
          <text class="field-label">{{ t('homeos.invite.phone') }}</text>
          <input
            v-model="inviteePhone"
            class="field-input"
            type="number"
            maxlength="20"
            :placeholder="t('homeos.invite.phone_hint')"
          />
        </view>

        <button class="primary-btn" :disabled="generating" @click="generate">
          {{ generating ? t('homeos.invite.generating') : t('homeos.invite.generate_action') }}
        </button>
      </view>

      <!-- 三态：链接 / 二维码 / 邀请码，共用同一个 code -->
      <view v-if="created" class="card">
        <text class="card-title">{{ t('homeos.invite.three_states') }}</text>
        <text class="field-note">{{ t('homeos.invite.three_states_note') }}</text>

        <view class="tri-grid">
          <view class="tri-tile">
            <text class="tri-label">{{ t('homeos.invite.state_link') }}</text>
            <text class="tri-link">{{ created.link || created.code }}</text>
            <button class="tile-btn" @click="copy(created.link || created.code)">
              {{ t('homeos.invite.copy_link') }}
            </button>
          </view>

          <view class="tri-tile">
            <text class="tri-label">{{ t('homeos.invite.state_qr') }}</text>
            <!-- 无 QR 依赖（package.json 未声明，本卡不得引依赖）：显式标注待接入，不留空图位 -->
            <view class="qr-placeholder">
              <text class="qr-soon">{{ t('homeos.invite.qr_pending') }}</text>
              <text class="qr-note">{{ t('homeos.invite.qr_pending_note') }}</text>
            </view>
            <button class="tile-btn" @click="copy(created.link || created.code)">
              {{ t('homeos.invite.copy_link_instead') }}
            </button>
          </view>

          <view class="tri-tile">
            <text class="tri-label">{{ t('homeos.invite.state_code') }}</text>
            <text class="tri-code">{{ created.code }}</text>
            <button class="tile-btn" @click="copy(created.code)">
              {{ t('homeos.invite.copy_code') }}
            </button>
          </view>
        </view>

        <view v-if="created.expires_at" class="kv">
          <text class="kv-k">{{ t('homeos.invite.expires_at') }}</text>
          <text class="kv-v">{{ formatDate(created.expires_at) }}</text>
        </view>
      </view>

      <!-- 待处理邀请 + 撤销 -->
      <view class="card">
        <text class="card-title">{{ t('homeos.invite.outstanding') }}</text>

        <view v-if="invites.length === 0" class="hc-state-empty inline">
          <text class="state-hint">{{ t('homeos.invite.empty_hint') }}</text>
        </view>

        <view v-for="row in invites" :key="row.id || row.code" class="invite-row">
          <view class="row-main">
            <view class="row-line1">
              <text class="row-code">{{ row.code }}</text>
              <text class="row-status" :class="{ expired: isExpired(row) }">
                {{ isExpired(row) ? t('homeos.invite.status_expired') : t('homeos.invite.status_pending') }}
              </text>
            </view>
            <text class="row-meta">
              {{ roleLabel(row.role) }}
              <text v-if="row.invitee_phone"> · {{ row.invitee_phone }}</text>
              <text v-if="row.expires_at"> · {{ t('homeos.invite.expires_at') }} {{ formatDate(row.expires_at) }}</text>
            </text>
          </view>
          <view class="row-actions">
            <text class="row-action" @click="copy(row.link || row.code)">{{ t('homeos.invite.copy') }}</text>
            <text class="row-action danger" @click="askRevoke(row)">{{ t('homeos.invite.revoke') }}</text>
          </view>
        </view>
      </view>
    </scroll-view>

    <!-- 撤销确认弹层（§2.2：破坏性动作走弹层，不占路由） -->
    <view v-if="revokeTarget" class="sheet-mask" @click="cancelRevoke">
      <view class="sheet" @click.stop>
        <text class="sheet-title">{{ t('homeos.invite.revoke_confirm_title') }}</text>
        <text class="state-hint">{{ t('homeos.invite.revoke_confirm_body', { code: revokeTarget.code }) }}</text>
        <text class="state-hint">{{ t('homeos.invite.revoke_confirm_note') }}</text>
        <view class="dialog-actions">
          <button class="dialog-btn" @click="cancelRevoke">{{ t('homeos.invite.cancel') }}</button>
          <button class="dialog-btn danger-btn" :disabled="revoking" @click="confirmRevoke">
            {{ revoking ? t('homeos.invite.revoking') : t('homeos.invite.confirm_revoke') }}
          </button>
        </view>
      </view>
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

/* 页头 */
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
  padding: 0 8rpx;
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
  font-size: 24rpx;
  color: var(--text-secondary);
}

.head-placeholder {
  width: 1rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

/* 骨架屏 */
.hc-skeleton {
  flex: 1;
  padding: 24rpx;
  display: flex;
  flex-direction: column;
  gap: 16rpx;
}

.sk-row {
  height: 240rpx;
  border-radius: var(--radius-md);
  background-color: var(--bg-tertiary);
}

/* 态位（第八章同一组件库） */
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

.hc-state-empty.inline {
  flex: none;
  padding: 32rpx 0;
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
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

/* 卡片与表单 */
.card {
  padding: 28rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.card-title {
  display: block;
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 20rpx;
}

.field {
  margin-bottom: 24rpx;
}

.field-label {
  display: block;
  font-size: 26rpx;
  color: var(--text-secondary);
  margin-bottom: 8rpx;
}

.field-note {
  display: block;
  font-size: 22rpx;
  color: var(--text-tertiary);
  margin-top: 8rpx;
  line-height: var(--line-height-normal);
}

.field-picker {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.field-input {
  width: 100%;
  padding: 20rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.chevron {
  font-size: 32rpx;
  color: var(--text-tertiary);
}

.segmented {
  display: flex;
  gap: 12rpx;
}

.segment {
  flex: 1;
  padding: 20rpx 0;
  text-align: center;
  font-size: 26rpx;
  color: var(--text-secondary);
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
}

.segment.active {
  background-color: var(--color-primary);
  color: var(--color-white);
}

.primary-btn {
  width: 100%;
  padding: 24rpx 0;
  background-color: var(--color-primary);
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 30rpx;
  font-weight: 600;
}

.primary-btn:disabled {
  opacity: 0.6;
}

/* 三态 */
.tri-grid {
  display: flex;
  flex-direction: column;
  gap: 20rpx;
  margin-top: 16rpx;
}

.tri-tile {
  padding: 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-md);
  display: flex;
  flex-direction: column;
  gap: 12rpx;
}

.tri-label {
  font-size: 26rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.tri-link {
  font-size: 24rpx;
  color: var(--color-primary);
  word-break: break-all;
  line-height: var(--line-height-normal);
}

.tri-code {
  font-size: 44rpx;
  font-weight: 600;
  letter-spacing: 4rpx;
  color: var(--text-primary);
}

.qr-placeholder {
  padding: 32rpx 24rpx;
  border: 2rpx dashed var(--border-color);
  border-radius: var(--radius-sm);
  background-color: var(--bg-primary);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
}

.qr-soon {
  font-size: 26rpx;
  color: var(--text-secondary);
}

.qr-note {
  font-size: 22rpx;
  color: var(--text-tertiary);
  text-align: center;
  line-height: var(--line-height-normal);
}

.tile-btn {
  padding: 16rpx 0;
  background-color: var(--color-primary);
  color: var(--color-white);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
}

.kv {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 24rpx;
  margin-top: 20rpx;
  padding-top: 16rpx;
  border-top: 1rpx solid var(--divider-color);
}

.kv-k {
  font-size: 26rpx;
  color: var(--text-secondary);
}

.kv-v {
  font-size: 26rpx;
  color: var(--text-primary);
  text-align: right;
}

/* 邀请行 */
.invite-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 20rpx;
  padding: 20rpx 0;
  border-top: 1rpx solid var(--divider-color);
}

.row-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6rpx;
  overflow: hidden;
}

.row-line1 {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.row-code {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.row-status {
  font-size: 20rpx;
  padding: 2rpx 12rpx;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
  color: var(--text-secondary);
}

.row-status.expired {
  color: var(--color-error);
}

.row-meta {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.row-actions {
  display: flex;
  gap: 20rpx;
  flex-shrink: 0;
}

.row-action {
  font-size: 26rpx;
  color: var(--color-primary);
}

.row-action.danger {
  color: var(--color-error);
}

/* 弹层 */
.sheet-mask {
  position: fixed;
  inset: 0;
  background-color: var(--overlay-dark);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.sheet {
  width: 600rpx;
  max-height: 80vh;
  overflow-y: auto;
  padding: 32rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  gap: 20rpx;
}

.sheet-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
  text-align: center;
}

.dialog-actions {
  display: flex;
  gap: 16rpx;
}

.dialog-btn {
  flex: 1;
  padding: 24rpx 0;
  border-radius: var(--radius-md);
  font-size: 28rpx;
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
}

.danger-btn {
  background-color: var(--color-error);
  color: var(--color-white);
}
</style>
