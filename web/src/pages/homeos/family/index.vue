<script setup lang="ts">
// pages/homeos/family/index —— 成员管理列表（§4.6 row ①，PRD 3.4.1 / 15.1 / 15.3 / 15.5）
//
// 口径：
//   · 数据源：`GET /api/homeos/members`（名册，PRD 3.7）。**当前会话家庭与本人角色不在本页另取一份**：
//     读的是主包 shell store 的会话快照（`homeStore.role`，唯一源 = `ensureSession()`），
//     与「我的」页同一份（15.1、17.5 五处同源）。
//   · **角色未知即按最小权限**：`homeStore.role` 为空串、`ward`、`guest` 时一律落
//     `hc-state-denied`，不按「member」猜（本页此前 `|| 'member'` 的兜底会让未知角色读到名册，
//     是一个权限缺陷；「我的」页第 66 行写的是同一个口径）。
//   · 「有没有账号」是治理属性，**只在本页显式标注**（17.2 定版：首页不得把成员分成两级）。
//   · 写入口（改角色 / 移除）按 15.3 的 HomeOS 行渲染：管理员可写，成人成员只读，
//     儿童与访客整页「无权限」态（§七：非管理员在本页只剩读取，儿童/访客为 —）。
//   · 唯一 owner 不可直接移除，必须先转让（3.4.1）；本页不给静默 no-op，只给理由。
//   · 移除与角色变更是破坏性/一次性确认，按 §2.2 走弹层，不占路由。
//   · 行点击 = 一级页 → 二级页压栈语义（§2.3）。§4.6 把成员治理页定在
//     `homeos/family/detail`、公开页定在 `homeos/member/detail`，两页 P1 尚未出生也未注册，
//     因此本页先把治理动作收在**页内弹层**里（同一组件、同一权限判定），
//     那两条路由出生时把 openMember() 换成 uni.navigateTo 即可。
//   · 分页：成员硬上限 12（21.1），单页即可承载；仍按响应里的 next_cursor 续读，
//     服务端不分页时该分支不出现（不是自造分页）。
//   · 请求层的响应出口只有一个（`utils/request` 的 `unwrapBody`），本页不再自带信封判定。
//   · 状态位用第八章的组件类名：hc-skeleton / hc-state-error / hc-state-empty / hc-state-denied。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, unwrapBody } from '@/utils/request'
import { useHomeStore } from '@/stores/home'
import { formatDate } from '@/utils/format'

type MemberRole = 'owner' | 'member' | 'ward' | 'guest'

interface MemberRow {
  id: string
  user_id?: string | null
  name?: string
  relation?: string
  role: MemberRole
  avatar?: string
  guardian_id?: string | null
  status?: string
  has_account?: boolean
  created_at?: string
}

/** PRD 21.1：家庭人数 2-6 人，≤12 硬上限。 */
const MEMBER_HARD_CAP = 12

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

// ---------------------------------------------------------------- 上下文与名册
const booting = ref(true)
const errorCode = ref('')
const errorText = ref('')
const denied = ref(false)
const members = ref<MemberRow[]>([])
const nextCursor = ref<string | null>(null)
const loadingMore = ref(false)
/**
 * 本人的 user_id：只读单一存储键 `user_id`（与「我的」页、它的 logout 清理列表同一套口径）。
 * 旧实现读的是登录页写下的 `user`，而登录响应按契约根本不带 user 对象（见 D1③报告），
 * 所以那条判定此前一直是空的 —— 换成 `user_id` 后，会话一旦带上该字段本页即刻生效。
 */
const myUserId = ref<string>('')

/**
 * 角色 = shell 会话快照的那一份（`stores/home.ts` 的 `role`，唯一源 `ensureSession()`）。
 * **不在本页再调一次 `/families` 自己推导**：本页此前 `currentFamily?.role || 'member'`
 * 的兜底会把「未知」当成成人成员，从而渲染出只该管理员与成人成员看到的名册。
 */
const myRole = computed<MemberRole | ''>(() => homeStore.role)
/** 15.1/15.3：管理员 = owner，成员与邀请只有 owner 可写。未知即 false（最小权限）。 */
const isAdmin = computed(() => homeStore.isAdmin)
/**
 * 15.3「HomeOS（成员/权限/设置）」行：管理员 A、成人成员 R、儿童与访客 —。
 * 这里**不写默认分支**：`role` 为空串（会话未取到）与 `ward`/`guest` 同样落 false，
 * 于是整页走 hc-state-denied —— 未知即按最小权限，不按「member」猜（15.1）。
 */
const canReadRoster = computed(() => myRole.value === 'owner' || myRole.value === 'member')

const memberCount = computed(() => members.value.length)
/**
 * 页头副标题的家庭名只读 shell 快照（`homeStore.family`，唯一源 = `home/summary`）。
 * 本页不再自己调 `/families` 拿名字，所以快照里没有名字时**只数人**，
 * 不把「有会话家庭」写成「未选择家庭」（store 的 `loadSession` 目前只落 id+role，
 * 不带家庭名 —— 已作为 store 缺口上报）。
 */
const headSub = computed(() => {
  const count = `${memberCount.value}/${MEMBER_HARD_CAP}`
  const name = homeStore.family?.name || ''
  if (name) return `${name} · ${count}`
  if (homeStore.sessionFamilyId) return count
  return `${t('homeos.family.no_family')} · ${count}`
})
const capReached = computed(() => memberCount.value >= MEMBER_HARD_CAP)
const ownerCount = computed(() => members.value.filter((m) => m.role === 'owner').length)

function roleLabel(role: MemberRole): string {
  const key = `homeos.family.role_${role}`
  const label = t(key)
  return label === key ? role : label
}

function displayName(m: MemberRow): string {
  return m.name || m.relation || t('homeos.family.unnamed')
}

/** 「有没有账号」：user_id 为空即无账号被记录成员（3.4.1、15.5）。 */
function hasAccount(m: MemberRow): boolean {
  if (m.user_id) return true
  if (typeof m.has_account === 'boolean') return m.has_account
  return false
}

function guardianName(m: MemberRow): string {
  if (!m.guardian_id) return ''
  const g = members.value.find((x) => x.id === m.guardian_id)
  return g ? displayName(g) : ''
}

// ---------------------------------------------------------------- 取数
function classifyError(err: any): { text: string; code: string } {
  const msg: string = err?.message || t('homeos.family.load_failed')
  const hit = msg.match(/HTTP\s+(\d{3})/)
  return { text: msg, code: hit ? hit[1] : 'NETWORK' }
}

async function loadRoster(cursor?: string | null): Promise<void> {
  const params: Record<string, any> = { limit: MEMBER_HARD_CAP }
  if (cursor) params.cursor = cursor

  const res = await request.get('/api/homeos/members', { params })
  const body = unwrapBody(res)
  const items: MemberRow[] = Array.isArray(body?.items)
    ? body.items
    : Array.isArray(body?.members)
      ? body.members
      : Array.isArray(body)
        ? body
        : []

  members.value = cursor ? members.value.concat(items) : items
  nextCursor.value = body?.next_cursor || body?.cursor || null
}

async function load(): Promise<void> {
  booting.value = true
  denied.value = false
  errorText.value = ''
  errorCode.value = ''

  try {
    // 会话快照（当前家庭 + 本人角色）只由 shell store 取一次；本页不再另发 /families
    await homeStore.ensureSession()

    // 无权限判定在读名册之前：第八章「不显示任何数据片段」，也不发这个 doomed 请求
    if (!canReadRoster.value) {
      members.value = []
      denied.value = true
      return
    }

    await loadRoster()
  } catch (err: any) {
    const { text, code } = classifyError(err)
    errorText.value = text
    errorCode.value = code
    members.value = []
  } finally {
    booting.value = false
  }
}

async function loadMore(): Promise<void> {
  if (!nextCursor.value || loadingMore.value) return
  loadingMore.value = true
  try {
    await loadRoster(nextCursor.value)
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  } finally {
    loadingMore.value = false
  }
}

// ---------------------------------------------------------------- 行内弹层（成员治理）
const sheetMember = ref<MemberRow | null>(null)
const pendingRole = ref<MemberRole>('member')

function openMember(m: MemberRow): void {
  sheetMember.value = m
  pendingRole.value = m.role
}

function closeSheet(): void {
  sheetMember.value = null
}

/** 唯一 owner 不可直接改角色或移除，必须先转让（3.4.1）—— 给理由，不给 no-op。 */
function roleLockReason(m: MemberRow): string {
  if (m.role === 'owner' && ownerCount.value <= 1) return t('homeos.family.only_owner_reason')
  return ''
}

function removeBlockReason(m: MemberRow): string {
  // 「是不是本人」比的是**账号 id**（`user_id`），不是成员行 id：`DELETE /members/{id}` 用的是后者，
  // 旧写法 `m.id === myUserId` 拿成员行 id 去比账号 id，两个命名空间永远不相等，自检形同不存在。
  if (myUserId.value && m.user_id && m.user_id === myUserId.value) {
    return t('homeos.family.remove_self_reason')
  }
  const locked = roleLockReason(m)
  if (locked) return locked
  return ''
}

const canChangeRole = computed(() => isAdmin.value && !!sheetMember.value && !roleLockReason(sheetMember.value))
const canRemoveMember = computed(
  () => isAdmin.value && !!sheetMember.value && !removeBlockReason(sheetMember.value)
)

async function saveRole(): Promise<void> {
  const target = sheetMember.value
  if (!target || !canChangeRole.value) return

  try {
    // PRD 3.7：PUT /api/homeos/members/{id}/role（pver+1 + homeos.permission.updated）
    await request.put(`/api/homeos/members/${target.id}/role`, { role: pendingRole.value })
    uni.showToast({ title: t('homeos.family.role_updated'), icon: 'success' })
    closeSheet()
    await loadRoster()
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  }
}

// ---------------------------------------------------------------- 移除确认弹层（§2.2：破坏性动作不占路由）
const removeTarget = ref<MemberRow | null>(null)
const removing = ref(false)

function askRemove(m: MemberRow): void {
  if (!isAdmin.value || removeBlockReason(m)) return
  removeTarget.value = m
}

function cancelRemove(): void {
  removeTarget.value = null
}

async function confirmRemove(): Promise<void> {
  const target = removeTarget.value
  if (!target || removing.value) return
  removing.value = true

  try {
    await request.delete(`/api/homeos/members/${target.id}`)
    removeTarget.value = null
    closeSheet()
    uni.showToast({ title: t('homeos.family.member_removed'), icon: 'success' })
    await loadRoster()
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  } finally {
    removing.value = false
  }
}

// ---------------------------------------------------------------- 导航
/** 邀请是独立条目（§4.6 两页分工），一级页 → 二级页 = 压栈（§2.3）。 */
function goInvite(): void {
  // 3.4.1 成员硬上限：到顶时不把管理员带到邀请页去发一条注定失败的邀请，
  // 但也不给静默 no-op —— 回话说清原因。
  if (capReached.value) {
    uni.showToast({ title: t('homeos.family.cap_reached'), icon: 'none' })
    return
  }
  uni.navigateTo({ url: '/pages/homeos/family/invite' })
}

function goBack(): void {
  if (getCurrentPages().length > 1) {
    uni.navigateBack()
  } else {
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  }
}

onMounted(() => {
  // 「我的」页与登录页写的都是 `user_id`；`GET /auth/login` 的 200 体不带用户身份字段
  // （homeos.yaml:142-156 只有 token 与 family_id/role/pver），所以这个键在 P1 当前无写入方，
  // 本行的自检因此取不到值 —— 已作为契约缺口上报（需要 login 回 user_id 或 members[] 标出 self）。
  const stored = uni.getStorageSync('user_id')
  myUserId.value = stored ? String(stored) : ''
  load()
})
</script>

<template>
  <view class="hc-page">
    <!-- 页头：家庭名 + 成员计数（上限 12 是本页的治理事实，写在列表头上） -->
    <view class="page-head">
      <text class="head-back" @click="goBack">‹</text>
      <view class="head-titles">
        <text class="head-title">{{ t('homeos.family.title') }}</text>
        <text class="head-sub">{{ headSub }}</text>
      </view>
      <!-- 15.3：invite 动作缺失即按钮不渲染（不是置灰） -->
      <text v-if="isAdmin" class="head-action" :class="{ muted: capReached }" @click="goInvite">
        {{ t('homeos.family.invite') }}
      </text>
      <text v-else class="head-placeholder" />
    </view>

    <!-- 加载中：骨架屏，不用 spinner 遮内容（第八章） -->
    <view v-if="booting" class="hc-skeleton">
      <view class="sk-row" />
      <view class="sk-row" />
      <view class="sk-row" />
    </view>

    <!-- 无权限：不显示任何数据片段（第八章） -->
    <view v-else-if="denied" class="hc-state-denied">
      <text class="state-title">{{ t('homeos.family.denied_title') }}</text>
      <text class="state-hint">{{ t('homeos.family.denied_hint') }}</text>
      <button class="state-btn" @click="goBack">{{ t('homeos.family.back') }}</button>
    </view>

    <!-- 失败：可重试 + 错误码可见，文案不带堆栈（第八章） -->
    <view v-else-if="errorText" class="hc-state-error">
      <text class="state-title">{{ t('homeos.family.load_failed') }}</text>
      <text class="state-hint">{{ errorText }}</text>
      <text class="state-code">{{ t('homeos.family.error_code') }}：{{ errorCode }}</text>
      <button class="state-btn" @click="load">{{ t('homeos.family.retry') }}</button>
    </view>

    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- 空态：引导动作，不写「暂无数据」了事（第八章） -->
      <view v-if="members.length === 0" class="hc-state-empty">
        <text class="state-title">{{ t('homeos.family.empty_title') }}</text>
        <template v-if="isAdmin">
          <text class="state-hint">{{ t('homeos.family.empty_hint_admin') }}</text>
          <button class="state-btn" @click="goInvite">{{ t('homeos.family.invite') }}</button>
        </template>
        <text v-else class="state-hint">{{ t('homeos.family.empty_hint_member') }}</text>
      </view>

      <view v-else class="member-list">
        <view v-for="m in members" :key="m.id" class="member-row" @click="openMember(m)">
          <view class="avatar">
            <image v-if="m.avatar" :src="m.avatar" mode="aspectFill" class="avatar-img" />
            <text v-else class="avatar-text">{{ displayName(m).charAt(0) }}</text>
          </view>

          <view class="row-main">
            <view class="row-line1">
              <text class="row-name">{{ displayName(m) }}</text>
              <text class="row-role" :class="`role-${m.role}`">{{ roleLabel(m.role) }}</text>
            </view>
            <view class="row-line2">
              <text v-if="m.relation" class="row-meta">{{ m.relation }}</text>
              <!-- 「有没有账号」只在这一页标注（17.2 定版） -->
              <text class="row-meta account-tag" :class="hasAccount(m) ? 'is-account' : 'no-account'">
                {{ hasAccount(m) ? t('homeos.family.has_account') : t('homeos.family.no_account') }}
              </text>
              <text v-if="m.role === 'guest'" class="row-meta">{{ t('homeos.family.guest_note') }}</text>
            </view>
          </view>

          <text class="row-chevron">›</text>
        </view>

        <!-- 服务端分页时才出现的续读位；成员上限 12，正常不会走到这里 -->
        <view v-if="nextCursor" class="more-row" @click="loadMore">
          <text>{{ loadingMore ? t('homeos.family.loading_more') : t('homeos.family.load_more') }}</text>
        </view>

        <view v-if="isAdmin && capReached" class="cap-note">
          <text>{{ t('homeos.family.cap_reached') }}</text>
        </view>
      </view>
    </scroll-view>

    <!-- 成员治理弹层（§2.2：破坏性/一次性动作走弹层，不占路由） -->
    <view v-if="sheetMember" class="sheet-mask" @click="closeSheet">
      <view class="sheet" @click.stop>
        <text class="sheet-title">{{ t('homeos.family.member_detail') }}</text>

        <view class="sheet-head">
          <view class="avatar">
            <image v-if="sheetMember.avatar" :src="sheetMember.avatar" mode="aspectFill" class="avatar-img" />
            <text v-else class="avatar-text">{{ displayName(sheetMember).charAt(0) }}</text>
          </view>
          <view class="row-main">
            <text class="row-name">{{ displayName(sheetMember) }}</text>
            <text class="row-meta">
              {{ roleLabel(sheetMember.role) }}
              <text v-if="sheetMember.relation"> · {{ sheetMember.relation }}</text>
            </text>
          </view>
        </view>

        <view class="kv">
          <text class="kv-k">{{ t('homeos.family.account_state') }}</text>
          <text class="kv-v">{{ hasAccount(sheetMember) ? t('homeos.family.has_account') : t('homeos.family.no_account') }}</text>
        </view>
        <view v-if="guardianName(sheetMember)" class="kv">
          <text class="kv-k">{{ t('homeos.family.guardian') }}</text>
          <text class="kv-v">{{ guardianName(sheetMember) }}</text>
        </view>
        <view v-if="sheetMember.created_at" class="kv">
          <text class="kv-k">{{ t('homeos.family.joined_at') }}</text>
          <text class="kv-v">{{ formatDate(sheetMember.created_at) }}</text>
        </view>

        <!-- 写入口：非管理员整块不渲染（15.3「按钮不渲染」规则） -->
        <template v-if="isAdmin">
          <view v-if="roleLockReason(sheetMember)" class="locked-note">
            <text>{{ roleLockReason(sheetMember) }}</text>
          </view>

          <view v-else class="role-edit">
            <text class="kv-k">{{ t('homeos.family.change_role') }}</text>
            <picker
              mode="selector"
              :range="[t('homeos.family.role_owner'), t('homeos.family.role_member'), t('homeos.family.role_ward'), t('homeos.family.role_guest')]"
              :value="['owner', 'member', 'ward', 'guest'].indexOf(pendingRole)"
              @change="(e: any) => pendingRole = (['owner', 'member', 'ward', 'guest'] as MemberRole[])[e.detail.value]"
            >
              <view class="role-picker">
                <text>{{ roleLabel(pendingRole) }}</text>
                <text class="row-chevron">›</text>
              </view>
            </picker>
            <button v-if="canChangeRole" class="state-btn" @click="saveRole">
              {{ t('homeos.family.save_role') }}
            </button>
          </view>

          <view v-if="removeBlockReason(sheetMember)" class="locked-note">
            <text>{{ removeBlockReason(sheetMember) }}</text>
          </view>
          <button v-else-if="canRemoveMember" class="danger-btn" @click="askRemove(sheetMember)">
            {{ t('homeos.family.remove') }}
          </button>
        </template>

        <button class="sheet-close" @click="closeSheet">{{ t('homeos.family.close') }}</button>
      </view>
    </view>

    <!-- 移除确认弹层 -->
    <view v-if="removeTarget" class="sheet-mask" @click="cancelRemove">
      <view class="sheet sheet-narrow" @click.stop>
        <text class="sheet-title">{{ t('homeos.family.remove_confirm_title') }}</text>
        <text class="state-hint">{{ t('homeos.family.remove_confirm_body', { name: displayName(removeTarget) }) }}</text>
        <text class="state-hint">{{ t('homeos.family.remove_confirm_note') }}</text>
        <view class="dialog-actions">
          <button class="dialog-btn" @click="cancelRemove">{{ t('homeos.family.cancel') }}</button>
          <button class="dialog-btn danger-btn" :disabled="removing" @click="confirmRemove">
            {{ removing ? t('homeos.family.removing') : t('homeos.family.confirm_remove') }}
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

.head-action {
  font-size: 28rpx;
  color: var(--color-primary);
  padding: 8rpx 20rpx;
  border: 1rpx solid var(--color-primary);
  border-radius: var(--radius-sm);
}

.head-action.muted {
  color: var(--text-tertiary);
  border-color: var(--border-color);
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
  height: 128rpx;
  border-radius: var(--radius-md);
  background-color: var(--bg-tertiary);
}

/* 四个态位（第八章同一组件库） */
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

/* 成员行 */
.member-list {
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.member-row {
  display: flex;
  align-items: center;
  gap: 20rpx;
  padding: 24rpx 28rpx;
  border-bottom: 1rpx solid var(--divider-color);
}

.member-row:last-child {
  border-bottom: none;
}

.avatar {
  width: 80rpx;
  height: 80rpx;
  border-radius: 50%;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  flex-shrink: 0;
}

.avatar-img {
  width: 100%;
  height: 100%;
}

.avatar-text {
  font-size: 32rpx;
  font-weight: 600;
}

.row-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8rpx;
  overflow: hidden;
}

.row-line1 {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.row-name {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.row-role {
  font-size: 20rpx;
  padding: 2rpx 12rpx;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
  color: var(--text-secondary);
}

.row-role.role-owner {
  background-color: var(--color-primary);
  color: var(--color-white);
}

.row-line2 {
  display: flex;
  align-items: center;
  gap: 12rpx;
  flex-wrap: wrap;
}

.row-meta {
  font-size: 24rpx;
  color: var(--text-secondary);
}

/* 有无账号的标注：文字标记，不用描边/灰度把成员分成两级（17.2 只约束首页） */
.account-tag {
  padding: 2rpx 12rpx;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
}

.row-chevron {
  font-size: 34rpx;
  color: var(--text-tertiary);
}

.more-row {
  padding: 24rpx;
  text-align: center;
  font-size: 26rpx;
  color: var(--color-primary);
}

.cap-note {
  padding: 20rpx 28rpx;
  font-size: 24rpx;
  color: var(--text-tertiary);
  background-color: var(--bg-tertiary);
}

/* 弹层 */
.sheet-mask {
  position: fixed;
  inset: 0;
  background-color: var(--overlay-dark);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  z-index: 100;
}

.sheet {
  width: 100%;
  max-height: 82vh;
  overflow-y: auto;
  padding: 32rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  display: flex;
  flex-direction: column;
  gap: 20rpx;
}

.sheet-narrow {
  max-height: 60vh;
}

.sheet-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.sheet-head {
  display: flex;
  align-items: center;
  gap: 20rpx;
}

.kv {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 24rpx;
  padding: 16rpx 0;
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

.role-edit {
  display: flex;
  flex-direction: column;
  gap: 16rpx;
  padding-top: 16rpx;
  border-top: 1rpx solid var(--divider-color);
}

.role-picker {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.locked-note {
  padding: 20rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 24rpx;
  color: var(--text-secondary);
  line-height: var(--line-height-normal);
}

.danger-btn {
  background-color: var(--color-error);
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 28rpx;
  padding: 20rpx 0;
}

.sheet-close {
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
  border-radius: var(--radius-md);
  font-size: 28rpx;
  padding: 20rpx 0;
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
</style>
