<script setup lang="ts">
// pages/homeos/mine/index —— 「我的」（§4.6）
//
// 实现 PRD 17.5 规格：
//   · 账号：个人信息展示
//   · 家庭管理（成员/邀请/角色/权限，见十五章）
//   · 开通更多面：本页只放一个**入口行**，跳 `homeos/family/modules`（17.8）
//   · 设置（主题/时区/货币/日期格式/语言，主题三态见 17.9）
//   · 数据（导出/备份/迁移，见 3.4.4）
//   · 隐私与紧急卡开关（见 15.4）
//   · 帮助与关于
//
// 三条同源约束（本轮改齐的重点）：
//   1. **角色不硬编码**：`role` 与 `isAdmin` 都取自主包 shell store 的会话快照
//      （唯一源 `GET /api/homeos/families` 的 `{families:[{id,name,role}]}`，经 `ensureSession()`），
//      本页与 `family/index`、`family/invite`、`family/modules` 共用同一份判定（15.1），
//      不在本页二次推导、也不把未知角色猜成「member」——未知即最小权限，管理入口不渲染。
//   2. **本页不写面配置**：面挂载配置的**唯一入口**是 `homeos/family/modules`
//      （§4.6 row ① + 17.8；§6.11「管理员开通：me/index → family/modules」）。
//      本页此前自己渲染了一份开关列表，那是同一 App 里的第二个面入口 ——
//      22.5 第 4 道同源⑦拦的正是这个形态，但同源⑦只扫分包、「我的」在主包，所以它不会被自动发现。
//      改完之后本页不再请求面目录、也不再发出任何面配置写入：
//      面清单、`enabled`、乐观锁 `version` 三样都不进本页（17.8）。
//   3. **定版路由未出生的行不留死点**：`homeos/settings/index`、`homeos/data/index`、
//      `homeos/about/index` 是 P1 定版但未出生，对应行一律标「未开放」并显式回话。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request } from '@/utils/request'
import { useHomeStore, type MemberRole } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

type ThemeMode = 'light' | 'dark' | 'system'

interface UserInfo {
  id: string
  phone: string
  name?: string
  avatar?: string
}

const userInfo = ref<UserInfo | null>(null)
const themeMode = ref<ThemeMode>('system')
const sessionError = ref('')

const role = computed<MemberRole | ''>(() => homeStore.role)
const isAdmin = computed(() => homeStore.isAdmin)

function roleLabel(value: MemberRole | ''): string {
  if (!value) return t('homeos.mine.role_unknown')
  return t(`homeos.family.role_${value}`)
}

// 账号信息来自本地登录态（token 由请求层持有，App 侧只有 id/手机号两份展示字段）
async function loadUserInfo() {
  const userId = uni.getStorageSync('user_id')
  const phone = uni.getStorageSync('phone')
  userInfo.value = {
    id: userId || '',
    phone: phone || t('homeos.mine.not_logged_in'),
    name: t('homeos.mine.default_nickname'),
  }
}

/**
 * 会话角色：`ensureSession()` 打的就是 `GET /api/homeos/families`。
 * 失败时 `role` 仍是空串、`isAdmin` 仍是 false —— 角色未知即按最小权限渲染，
 * 不把未知角色猜成「member」，也不给一个点不动的管理入口（15.1、§七「不渲染不是置灰」）。
 */
async function loadSession() {
  sessionError.value = ''
  try {
    await homeStore.ensureSession()
  } catch (err: any) {
    sessionError.value = err?.message || t('homeos.mine.session_failed')
  }
}

/**
 * 主题三态（17.9）：把 `data-theme` 写到文档根节点，theme.css 的两套变量值随之切换。
 * H5 运行期全站共用同一个 document，因此本页写入后对全站生效、切页不回落；
 * 冷启动时的初值在「我的」页 onMounted 读回并重新写入（未进过本页即为跟随系统档）。
 */
function applyTheme(mode: ThemeMode) {
  const doc = (globalThis as any).document
  if (!doc?.documentElement) return
  if (mode === 'system') {
    doc.documentElement.removeAttribute('data-theme')
    return
  }
  doc.documentElement.setAttribute('data-theme', mode)
}

function changeTheme(mode: ThemeMode) {
  themeMode.value = mode
  if (mode === 'system') {
    uni.removeStorageSync('theme')
  } else {
    uni.setStorageSync('theme', mode)
  }
  applyTheme(mode)
}

function restoreTheme() {
  const stored = uni.getStorageSync('theme')
  const mode: ThemeMode = stored === 'light' || stored === 'dark' ? stored : 'system'
  themeMode.value = mode
  applyTheme(mode)
}

// Logout
function handleLogout() {
  uni.showModal({
    title: t('homeos.mine.logout'),
    content: t('homeos.mine.logout_confirm'),
    confirmText: t('homeos.common.confirm'),
    cancelText: t('homeos.common.cancel'),
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.post('/api/homeos/auth/logout')
      } catch {
        // 退出接口的失败不阻断清本地态：令牌留在服务端也只是下一次 401 时被清掉
      }

      uni.removeStorageSync('access_token')
      uni.removeStorageSync('refresh_token')
      uni.removeStorageSync('user_id')
      uni.removeStorageSync('phone')
      homeStore.clearData()

      uni.reLaunch({ url: '/pages/homeos/auth/login' })
    },
  })
}

// 定版路由尚未出生的行（§4.6 的 homeos/settings/index、homeos/data/index、homeos/about/index）：
// 显式回话 + 行上标「未开放」，不留 `() => {}` 这种点了没反应的死行（同首页对未出生面的处置）。
function notifyUnavailable(feature: string) {
  uni.showToast({ title: t('homeos.mine.pending', { feature }), icon: 'none' })
}

interface MenuRow {
  label: string
  action: () => void
  /** true = 目标页未出生，行上显式标「未开放」 */
  pending?: boolean
}

const familySection = computed<MenuRow[]>(() => [
  { label: t('homeos.mine.row_members'), action: () => uni.navigateTo({ url: '/pages/homeos/family/index' }) },
  { label: t('homeos.mine.row_invite'), action: () => uni.navigateTo({ url: '/pages/homeos/family/invite' }) },
])

/**
 * 「开通更多面」= 面挂载配置的**唯一入口行**（§4.6 row ①、17.8、§6.11）：
 * 与上面两行同构，只有跳转、没有任何写操作，开通/停用与二次确认都在 `family/modules` 里发生。
 *
 * 只给管理员看（`isAdmin` 就是 shell 会话快照那一份，本页不自己判角色）：
 * 非管理员进了开通页也只有只读态，这一行对他是个不能成立的动作（15.2、§七「按钮不渲染，不是置灰」）。
 * 跳转方式按 §2.3：一级页 → 具名页 = 压栈 `uni.navigateTo`，返回即出栈回本页。
 */
const mountSection = computed<MenuRow[]>(() =>
  isAdmin.value
    ? [{ label: t('homeos.mine.row_modules'), action: () => uni.navigateTo({ url: '/pages/homeos/family/modules' }) }]
    : []
)

// 设置组：主题在本页内联渲染，其余四项属 homeos/settings/index（未出生）
const otherSettingRows = computed<MenuRow[]>(() => [
  { label: t('homeos.mine.row_timezone'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_timezone')) },
  { label: t('homeos.mine.row_currency'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_currency')) },
  { label: t('homeos.mine.row_date_format'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_date_format')) },
  { label: t('homeos.mine.row_language'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_language')) },
])

const dataRows = computed<MenuRow[]>(() => [
  { label: t('homeos.mine.row_export'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_export')) },
  { label: t('homeos.mine.row_backup'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_backup')) },
])

const otherRows = computed<MenuRow[]>(() => [
  { label: t('homeos.mine.row_privacy'), action: () => uni.navigateTo({ url: '/pages/homeos/legal/detail?type=privacy' }) },
  { label: t('homeos.mine.row_agreement'), action: () => uni.navigateTo({ url: '/pages/homeos/legal/detail?type=agreement' }) },
  { label: t('homeos.mine.row_about'), pending: true, action: () => notifyUnavailable(t('homeos.mine.row_about')) },
])

const themeOptions: Array<{ mode: ThemeMode; label: string }> = [
  { mode: 'light', label: t('homeos.mine.theme_light') },
  { mode: 'dark', label: t('homeos.mine.theme_dark') },
  { mode: 'system', label: t('homeos.mine.theme_system') },
]

onMounted(() => {
  restoreTheme()
  loadUserInfo()
  loadSession()
})
</script>

<template>
  <view class="hc-page">
    <scroll-view class="hc-scroll" scroll-y>
      <!-- User profile card -->
      <view class="profile-card">
        <view class="profile-avatar">
          <text>{{ (userInfo?.name || t('homeos.mine.default_nickname')).charAt(0) }}</text>
        </view>
        <view class="profile-info">
          <text class="profile-name">{{ userInfo?.name || t('homeos.mine.default_nickname') }}</text>
          <text class="profile-phone">{{ userInfo?.phone }}</text>
        </view>
      </view>

      <!-- Current family + 会话角色（唯一的角色源：GET /api/homeos/families） -->
      <view v-if="homeStore.family" class="family-card">
        <text class="family-label">{{ t('homeos.mine.current_family') }}</text>
        <text class="family-name">{{ homeStore.family.name }}</text>
        <text class="family-role">{{ roleLabel(role) }}</text>
      </view>
      <view v-else-if="sessionError" class="family-card">
        <text class="family-label">{{ sessionError }}</text>
      </view>

      <!-- 家庭 -->
      <view class="menu-section">
        <text class="section-title">{{ t('homeos.mine.section_family') }}</text>
        <view class="menu-list">
          <view v-for="item in familySection" :key="item.label" class="menu-item" @click="item.action">
            <text class="menu-label">{{ item.label }}</text>
            <text class="menu-arrow">›</text>
          </view>
        </view>
      </view>

      <!-- 开通更多面（17.8）：一个入口行，跳治理页。本页不渲染开关、不读写面配置 -->
      <view v-if="mountSection.length > 0" class="menu-section">
        <view class="menu-list">
          <view v-for="item in mountSection" :key="item.label" class="menu-item" @click="item.action">
            <text class="menu-label">{{ item.label }}</text>
            <text class="menu-arrow">›</text>
          </view>
        </view>
      </view>

      <!-- 设置：主题三态内联，其余四项标注未开放 -->
      <view class="menu-section">
        <text class="section-title">{{ t('homeos.mine.section_settings') }}</text>
        <view class="theme-selector">
          <view
            v-for="option in themeOptions"
            :key="option.mode"
            class="theme-item"
            :class="{ active: themeMode === option.mode }"
            @click="changeTheme(option.mode)"
          >
            <text>{{ option.label }}</text>
          </view>
        </view>
        <view class="menu-list settings-list">
          <view v-for="item in otherSettingRows" :key="item.label" class="menu-item" @click="item.action">
            <text class="menu-label">{{ item.label }}</text>
            <text v-if="item.pending" class="menu-soon">{{ t('homeos.common.not_open') }}</text>
            <text class="menu-arrow">›</text>
          </view>
        </view>
      </view>

      <!-- 数据 -->
      <view class="menu-section">
        <text class="section-title">{{ t('homeos.mine.section_data') }}</text>
        <view class="menu-list">
          <view v-for="item in dataRows" :key="item.label" class="menu-item" @click="item.action">
            <text class="menu-label">{{ item.label }}</text>
            <text v-if="item.pending" class="menu-soon">{{ t('homeos.common.not_open') }}</text>
            <text class="menu-arrow">›</text>
          </view>
        </view>
      </view>

      <!-- 其他 -->
      <view class="menu-section">
        <text class="section-title">{{ t('homeos.mine.section_other') }}</text>
        <view class="menu-list">
          <view v-for="item in otherRows" :key="item.label" class="menu-item" @click="item.action">
            <text class="menu-label">{{ item.label }}</text>
            <text v-if="item.pending" class="menu-soon">{{ t('homeos.common.not_open') }}</text>
            <text class="menu-arrow">›</text>
          </view>
        </view>
      </view>

      <view class="logout-section">
        <button class="logout-btn" @click="handleLogout">{{ t('homeos.mine.logout') }}</button>
      </view>
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

.hc-scroll {
  flex: 1;
  padding: 24rpx 24rpx 0;
}

/* Profile card */
.profile-card {
  display: flex;
  align-items: center;
  gap: 24rpx;
  padding: 32rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
}

.profile-avatar {
  width: 96rpx;
  height: 96rpx;
  border-radius: 50%;
  background-color: var(--color-primary-light);
  color: var(--color-on-primary);
  font-size: 40rpx;
  display: flex;
  align-items: center;
  justify-content: center;
}

.profile-info {
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.profile-name {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.profile-phone {
  font-size: 26rpx;
  color: var(--text-secondary);
}

/* Family card */
.family-card {
  padding: 24rpx 32rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  gap: 16rpx;
}

.family-label {
  font-size: 26rpx;
  color: var(--text-secondary);
}

.family-name {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.family-role {
  font-size: 24rpx;
  padding: 4rpx 12rpx;
  background-color: var(--color-primary-light);
  color: var(--color-on-primary);
  border-radius: var(--radius-sm);
}

/* Menu sections */
.menu-section {
  margin-bottom: 32rpx;
}

.section-title {
  display: block;
  font-size: 26rpx;
  color: var(--text-secondary);
  margin-bottom: 12rpx;
  padding-left: 8rpx;
}

/* Theme selector */
.theme-selector {
  display: flex;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.theme-item {
  flex: 1;
  padding: 24rpx 0;
  text-align: center;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-secondary);
}

.theme-item.active {
  background-color: var(--color-primary);
  color: var(--color-on-primary);
}

/* Menu list */
.menu-list {
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.menu-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx 32rpx;
  border-bottom: 1rpx solid var(--divider-color);
}

.menu-item:last-child {
  border-bottom: none;
}

.menu-label {
  font-size: 28rpx;
  color: var(--text-primary);
}

.menu-arrow {
  font-size: 32rpx;
  color: var(--text-tertiary);
}

/* 目标页未出生的行：显式标注，不假装可跳转 */
.menu-soon {
  flex-shrink: 0;
  margin-right: 12rpx;
  padding: 2rpx 12rpx;
  font-size: 20rpx;
  color: var(--text-tertiary);
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
}

.settings-list {
  margin-top: 16rpx;
}

/* Logout section */
.logout-section {
  padding: 32rpx 0;
}

.logout-btn {
  width: 100%;
  padding: 28rpx 0;
  background-color: var(--color-error);
  color: var(--color-on-primary);
  border-radius: var(--radius-md);
  font-size: 32rpx;
  font-weight: 600;
}
</style>
