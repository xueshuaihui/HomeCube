<script setup lang="ts">
// pages/homeos/home/index —— 主包首页路由位（§4.2「首页与动态」，内容 = 第三章四区规格）。
//
// 完整实现 A/B/C/D 四区 + 时间窗条 + 底部导航：
//   · A 区：问候语 + 家庭名 + 成员头像排（前 3 个 + 溢出计数）
//   · B 区：今日到期与待办时间轴（最多 3 张小卡）
//   · C 区：面矩阵宫格（变长宫格，顺序恒为 registry 登记顺序）
//   · D 区：动态流前 20 条 + 「查看全部」
//   · 时间窗条：月/季/年三档切换
//   · 底部导航：Home/Dynamics/+/Messages/My 五项

import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useHomeStore, type PeriodGrain, type FaceStatus, type MemberInfo } from '@/stores/home'
import { formatRelativeTime, formatDate } from '@/utils/format'

const PAGE_PATH = '/pages/homeos/home/index'
const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

/** 时间窗条恒三档（§1.3 第 19 条：没有「周」也没有「全部」）。 */
const PERIOD_GRAINS: PeriodGrain[] = ['month', 'quarter', 'year']

// D 行五项之间、首页矩阵 → 某面 list 页 = 无栈互切（栈深恒为 1，§2.3）
function switchEntry(path: string) {
  uni.reLaunch({ url: path })
}

// 首屏**只有一个请求**（§3.1）：四区与 unread 全部从 home/summary 那一份派生，
// 本页不并行打第二个服务、也不自算任何计数。
async function fetchHomeSummary() {
  try {
    await homeStore.fetchSummary()
  } catch {
    // 错误态由 store 的 summaryError 承载（第八章同一套状态组件）
  }
}

// 时间窗条：档位写进 shell store，`period` 的三档编码（YYYY-MM / YYYY-Qn / YYYY）
// 也由 store 按家庭时区算 —— 首页不自己拼一个 'month' 塞进查询串（§1.3 第 19 条、§6.4）。
async function handlePeriodChange(grain: PeriodGrain) {
  homeStore.setPeriodGrain(grain)
  await fetchHomeSummary()
}

// A 区头像排整行 → 成员管理列表（§2.3 一级页 → 二级页 = 压栈；§4.6 路由 homeos/family/index）
// 溢出「+N」圆圈不是邀请入口（spec ⑮），它是整行点击的一部分，故不加独立 handler。
function goToMembers() {
  uni.navigateTo({ url: '/pages/homeos/family/index' })
}

// B 区「日历 ›」= 到期中心入口（§1.3 第 13 条：入口 = 首页 B 区 + 我的 → 时间与提醒）。
// 到期中心的 9 页（§4.4 homeos/calendar|todo|reminder/*）随 S6 出生，P1 当期未注册任何一条路由，
// 所以这里不压栈、也不为未出生的域注册路由位（CI 三查②与未出生域零痕迹检查）：
// 与 goToFace 对未出生面做的是同一种处置 —— 显式回话，不给静默 no-op。
function goToCalendar() {
  uni.showToast({ title: t('homeos.home.calendar_coming_soon'), icon: 'none' })
}

// Navigate to face detail
function goToFace(faceCode: string) {
  if (!faceCode) return
  // 只跳 pages.json 里已注册的路由：P1 只有 finance 出生，其余面随各自期次注册
  // （未出生域零痕迹，CI 三查②），故这里未命中时同样走「显式回话」。
  const routeMap: Record<string, string> = {
    finance: '/pages/finance/flow/index',
  }
  const path = routeMap[faceCode]
  if (path) {
    switchEntry(path)
  } else {
    uni.showToast({ title: t('homeos.home.face_coming_soon'), icon: 'none' })
  }
}

// Navigate to dynamics page
function goToDynamics() {
  switchEntry('/pages/homeos/dynamics/index')
}

/** C 区格子可用性：契约的 `availability` 是二元位，不是旧的 `available` 布尔。 */
function isFaceAvailable(face: FaceStatus): boolean {
  return face.availability === 'available'
}

/** B 区条目的来系统标签（`source_system` 只用来标来源，不当颜色变量名用）。 */
function sourceLabel(source: string): string {
  const map: Record<string, string> = {
    finance: t('homeos.home.source_finance'),
    health: t('homeos.home.source_health'),
    schedule: t('homeos.home.source_schedule'),
    elder: t('homeos.home.source_elder'),
    child: t('homeos.home.source_child'),
    iot: t('homeos.home.source_iot'),
  }
  return map[source] || source
}

/** D 行前的面标记取面名首字，矩阵里没有这个 code 时退回 code 本身。 */
function faceInitial(code: string): string {
  const face = homeStore.faces.find((f) => f.code === code)
  return face ? face.name.charAt(0) : code
}

// Get avatar URL or fallback to first character
function getAvatarUrl(member: MemberInfo): string | null {
  return member.avatar || null
}

function getAvatarText(member: MemberInfo): string {
  if (member.avatar) return ''
  return member.name?.charAt(0) || '?'
}

// Extract time from ISO timestamp for due items
function extractTime(isoString: string): string {
  const date = new Date(isoString)
  const hours = date.getHours().toString().padStart(2, '0')
  const minutes = date.getMinutes().toString().padStart(2, '0')
  return `${hours}:${minutes}`
}

onMounted(() => {
  fetchHomeSummary()
})
</script>

<template>
  <view class="hc-page">
    <!-- Loading state：loading/error 两个状态位归 shell store（首屏唯一请求的成败），
         本页不再自持一份，也不自算任何计数 -->
    <view v-if="homeStore.summaryLoading && !homeStore.family" class="hc-loading">
      <text>{{ t('homeos.home.loading') }}</text>
    </view>

    <!-- Error state -->
    <view v-else-if="homeStore.summaryError" class="hc-error">
      <text>{{ homeStore.summaryError }}</text>
      <button @click="fetchHomeSummary">{{ t('homeos.home.retry') }}</button>
    </view>

    <!-- Main content -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- Time window bar -->
      <view class="time-window-bar">
        <text class="twb-label">{{ t('homeos.home.period_label') }}</text>
        <view class="twb-segments">
          <view
            v-for="grain in PERIOD_GRAINS"
            :key="grain"
            class="twb-segment"
            :class="{ active: homeStore.periodGrain === grain }"
            @click="handlePeriodChange(grain)"
          >
            <text>{{ t(`homeos.home.period_${grain}`) }}</text>
          </view>
        </view>
      </view>

      <!-- Zone A: Family area -->
      <view class="zone-a">
        <text class="za-greeting">{{ homeStore.greeting }}</text>
        <text class="za-family-name">{{ homeStore.family?.name || '未命名家庭' }}</text>

        <!-- Member avatar row -->
        <view class="za-avatar-row" @click="goToMembers">
          <view
            v-for="member in (homeStore.family?.members || []).slice(0, 3)"
            :key="member.member_id"
            class="za-avatar"
          >
            <image
              v-if="getAvatarUrl(member)"
              :src="getAvatarUrl(member)!"
              mode="aspectFill"
              class="za-avatar-img"
            />
            <view v-else class="za-avatar-text">
              <text>{{ getAvatarText(member) }}</text>
            </view>
          </view>
          <view v-if="homeStore.overflowMemberCount > 0" class="za-avatar za-avatar-overflow">
            <text>+{{ homeStore.overflowMemberCount }}</text>
          </view>
        </view>
      </view>

      <!-- Zone B: Today's due & todo -->
      <view class="zone-b">
        <view class="zb-header">
          <text class="zb-title">{{ t('homeos.home.today_title', { count: homeStore.dueToday.count }) }}</text>
          <view class="zb-calendar-link" @click="goToCalendar">
            <text>{{ t('homeos.home.calendar_entry') }}</text>
            <text class="zb-soon">{{ t('homeos.common.not_open') }}</text>
          </view>
        </view>

        <view v-if="homeStore.dueToday.items.length > 0" class="zb-timeline">
          <view
            v-for="item in homeStore.dueToday.items.slice(0, 3)"
            :key="item.id"
            class="zb-card"
          >
            <text class="zb-time">{{ extractTime(item.due_at) }}</text>
            <text class="zb-headline">{{ item.title }}</text>
            <text class="zb-source">{{ sourceLabel(item.source_system) }}</text>
          </view>
        </view>
        <view v-else class="zb-empty">
          <text>{{ t('homeos.home.today_empty') }}</text>
        </view>
      </view>

      <!-- Zone C: Face matrix -->
      <view class="zone-c">
        <view class="zc-header">
          <text class="zc-title">{{ t('homeos.home.matrix_title', { count: homeStore.faces.length }) }}</text>
          <text class="zc-hint">{{ t('homeos.home.matrix_hint') }}</text>
        </view>

        <view
          class="zc-grid"
          :class="{
            'zc-grid-1': homeStore.faces.length === 1,
            'zc-grid-2': homeStore.faces.length === 2,
            'zc-grid-3to4': homeStore.faces.length >= 3 && homeStore.faces.length <= 4,
            'zc-grid-5to6': homeStore.faces.length >= 5 && homeStore.faces.length <= 6,
          }"
        >
          <view
            v-for="face in homeStore.faces"
            :key="face.code"
            class="zc-cell"
            :class="{ unavailable: !isFaceAvailable(face) }"
            @click="goToFace(face.code)"
          >
            <!-- 待处理角标（badge 语义 = 待处理，与顶栏/D 行的「未读」不是一个数，17.1 第 3 条） -->
            <view v-if="face.badge > 0" class="zc-badge">
              <text>{{ face.badge > 99 ? '99+' : face.badge }}</text>
            </view>

            <view class="zc-icon">
              <text>{{ face.name.charAt(0) }}</text>
            </view>

            <text class="zc-name">{{ face.name }}</text>
            <text class="zc-status">{{ face.headline }}</text>

            <!-- Unavailable overlay：契约只有 availability 二元位 + 数据时点 as_of，
                 没有 unavailable_reason 字段，故文案走 i18n、不编造原因 -->
            <view v-if="!isFaceAvailable(face)" class="zc-unavailable">
              <text class="zc-unavail-text">{{ t('homeos.home.face_unavailable') }}</text>
              <text v-if="face.as_of" class="zc-unavail-time">
                {{ t('homeos.home.face_as_of', { time: formatDate(face.as_of) }) }}
              </text>
              <button class="zc-retry-btn" @click.stop="fetchHomeSummary">{{ t('homeos.home.retry') }}</button>
            </view>
          </view>
        </view>
      </view>

      <!-- Zone D: Dynamics feed -->
      <view class="zone-d">
        <view class="zd-header">
          <text class="zd-title">{{ t('homeos.home.dynamics_title') }}</text>
          <view class="zd-view-all" @click="goToDynamics">
            <text>{{ t('homeos.home.dynamics_view_all') }}</text>
          </view>
        </view>

        <view v-if="homeStore.dynamics.length > 0" class="zd-list">
          <view
            v-for="item in homeStore.dynamics.slice(0, 20)"
            :key="item.id"
            class="zd-item"
          >
            <view class="zd-face-icon">
              <text>{{ faceInitial(item.code) }}</text>
            </view>
            <view class="zd-content">
              <text class="zd-member">{{ item.actor_name }}</text>
              <text class="zd-headline">{{ item.summary }}</text>
              <text class="zd-time">{{ formatRelativeTime(item.at) }}</text>
            </view>
          </view>
        </view>
        <view v-else class="zd-empty">
          <text>{{ t('homeos.home.dynamics_empty') }}</text>
        </view>
      </view>
    </scroll-view>

    <!-- Bottom navigation -->
    <view class="bottom-nav">
      <view class="bn-item" @click="switchEntry(PAGE_PATH)">
        <text class="bn-icon">🏠</text>
        <text class="bn-label bn-active">{{ t('homeos.nav.home') }}</text>
      </view>
      <view class="bn-item" @click="goToDynamics">
        <text class="bn-icon">💬</text>
        <text class="bn-label">{{ t('homeos.nav.dynamics') }}</text>
        <view v-if="homeStore.unread > 0" class="bn-badge">
          <text>{{ homeStore.unread > 99 ? '99+' : homeStore.unread }}</text>
        </view>
      </view>
      <view class="bn-item bn-plus" @click="switchEntry('/pages/homeos/quick-add/index')">
        <text class="bn-plus-icon">＋</text>
      </view>
      <view class="bn-item" @click="switchEntry('/pages/homeos/messages/index')">
        <text class="bn-icon">🔔</text>
        <text class="bn-label">{{ t('homeos.nav.messages') }}</text>
        <view v-if="homeStore.unread > 0" class="bn-dot" />
      </view>
      <view class="bn-item" @click="switchEntry('/pages/homeos/mine/index')">
        <text class="bn-icon">👤</text>
        <text class="bn-label">{{ t('homeos.nav.mine') }}</text>
      </view>
    </view>
  </view>
</template>

<style scoped>
/* Page container */
.hc-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: var(--bg-secondary);
}

.hc-loading,
.hc-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48rpx;
  gap: 24rpx;
}

.hc-scroll {
  flex: 1;
  padding-bottom: 120rpx; /* Space for bottom nav */
}

/* Time window bar */
.time-window-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.twb-label {
  font-size: 28rpx;
  color: var(--text-secondary);
}

.twb-segments {
  display: flex;
  gap: 8rpx;
}

.twb-segment {
  padding: 8rpx 24rpx;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
  font-size: 26rpx;
  color: var(--text-secondary);
}

.twb-segment.active {
  background-color: var(--color-primary);
  color: var(--color-on-primary);
}

/* Zone A: Family area */
.zone-a {
  padding: 32rpx;
  background-color: var(--bg-primary);
  margin-bottom: 16rpx;
}

.za-greeting {
  font-size: 36rpx;
  font-weight: 600;
  color: var(--text-primary);
  display: block;
  margin-bottom: 8rpx;
}

.za-family-name {
  font-size: 28rpx;
  color: var(--text-secondary);
  display: block;
  margin-bottom: 24rpx;
}

.za-avatar-row {
  display: flex;
  gap: 16rpx;
  align-items: center;
}

.za-avatar {
  width: 64rpx;
  height: 64rpx;
  border-radius: 50%;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: var(--color-primary-light);
  color: var(--color-on-primary);
  font-size: 28rpx;
  font-weight: 600;
}

.za-avatar-img {
  width: 100%;
  height: 100%;
}

.za-avatar-text {
  line-height: 1;
}

.za-avatar-overflow {
  background-color: transparent;
  border: 2rpx dashed var(--border-color);
  color: var(--text-secondary);
  font-size: 24rpx;
}

/* Zone B: Today's due */
.zone-b {
  padding: 32rpx;
  background-color: var(--bg-primary);
  margin-bottom: 16rpx;
}

.zb-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24rpx;
}

.zb-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.zb-calendar-link {
  display: flex;
  align-items: center;
  gap: 8rpx;
  font-size: 28rpx;
  color: var(--text-secondary);
}

/* 到期中心未出生时的显式标注：不假装可跳转（不带「›」箭头） */
.zb-soon {
  font-size: 20rpx;
  color: var(--text-tertiary);
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  padding: 2rpx 10rpx;
}

.zb-timeline {
  display: flex;
  gap: 16rpx;
  overflow-x: auto;
}

.zb-card {
  min-width: 240rpx;
  padding: 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-md);
  border-left: 4rpx solid var(--color-primary);
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.zb-time {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.zb-headline {
  font-size: 26rpx;
  color: var(--text-secondary);
}

/* B 区条目的来系统标记：契约的 source_system 只作文字标注，
   不用它拼出 `var(--color-${source_system})` 这类动态变量名（未定义即静默失效）。 */
.zb-source {
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.zb-empty {
  padding: 48rpx 0;
  text-align: center;
  color: var(--text-tertiary);
}

/* Zone C: Face matrix */
.zone-c {
  padding: 32rpx;
  background-color: var(--bg-primary);
  margin-bottom: 16rpx;
}

.zc-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24rpx;
}

.zc-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.zc-hint {
  font-size: 26rpx;
  color: var(--text-tertiary);
}

.zc-grid {
  display: grid;
  gap: 24rpx;
}

.zc-grid-1 {
  grid-template-columns: repeat(2, 1fr);
}

.zc-grid-2 {
  grid-template-columns: repeat(2, 1fr);
}

.zc-grid-3to4 {
  grid-template-columns: repeat(2, 1fr);
}

.zc-grid-5to6 {
  grid-template-columns: repeat(3, 1fr);
}

.zc-cell {
  position: relative;
  padding: 32rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12rpx;
  min-height: 200rpx;
}

.zc-cell.unavailable {
  opacity: 0.6;
}

.zc-badge {
  position: absolute;
  top: 12rpx;
  right: 12rpx;
  min-width: 32rpx;
  height: 32rpx;
  border-radius: 16rpx;
  background-color: var(--badge-unread);
  color: var(--color-on-badge);
  font-size: 20rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 8rpx;
}

.zc-icon {
  width: 80rpx;
  height: 80rpx;
  border-radius: var(--radius-md);
  background-color: var(--color-primary-light);
  color: var(--color-on-primary);
  font-size: 40rpx;
  display: flex;
  align-items: center;
  justify-content: center;
}

.zc-name {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
  text-align: center;
}

.zc-status {
  font-size: 24rpx;
  color: var(--text-secondary);
  text-align: center;
  line-height: 1.4;
}

.zc-unavailable {
  position: absolute;
  inset: 0;
  background-color: var(--overlay-dark);
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8rpx;
  padding: 24rpx;
}

.zc-unavail-text {
  font-size: 28rpx;
  color: var(--color-on-overlay);
  font-weight: 600;
}

.zc-unavail-time {
  font-size: 24rpx;
  color: var(--color-on-overlay-muted);
}

.zc-retry-btn {
  margin-top: 12rpx;
  padding: 12rpx 32rpx;
  background-color: var(--bg-primary);
  color: var(--text-primary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
}

/* Zone D: Dynamics */
.zone-d {
  padding: 32rpx;
  background-color: var(--bg-primary);
}

.zd-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24rpx;
}

.zd-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.zd-view-all {
  font-size: 28rpx;
  color: var(--color-primary);
}

.zd-list {
  display: flex;
  flex-direction: column;
  gap: 24rpx;
}

.zd-item {
  display: flex;
  gap: 16rpx;
  padding: 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-md);
}

.zd-face-icon {
  width: 48rpx;
  height: 48rpx;
  border-radius: var(--radius-sm);
  background-color: var(--color-primary-light);
  color: var(--color-on-primary);
  font-size: 20rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.zd-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.zd-member {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.zd-headline {
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
}

.zd-time {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.zd-empty {
  padding: 48rpx 0;
  text-align: center;
  color: var(--text-tertiary);
}

/* Bottom navigation */
.bottom-nav {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  height: 100rpx;
  background-color: var(--bg-primary);
  border-top: 1rpx solid var(--divider-color);
  display: flex;
  align-items: center;
  justify-content: space-around;
  padding-bottom: env(safe-area-inset-bottom);
  z-index: 100;
}

.bn-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4rpx;
  position: relative;
}

.bn-icon {
  font-size: 40rpx;
}

.bn-label {
  font-size: 22rpx;
  color: var(--text-secondary);
}

.bn-label.bn-active {
  color: var(--color-primary);
}

.bn-plus {
  flex: 1.5;
}

.bn-plus-icon {
  width: 80rpx;
  height: 80rpx;
  border-radius: 50%;
  background-color: var(--color-primary);
  color: var(--color-on-primary);
  font-size: 56rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: -30rpx;
  box-shadow: var(--shadow-md);
}

.bn-badge {
  position: absolute;
  top: 8rpx;
  right: 50%;
  transform: translateX(24rpx);
  min-width: 32rpx;
  height: 32rpx;
  border-radius: 16rpx;
  background-color: var(--badge-unread);
  color: var(--color-on-badge);
  font-size: 20rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 8rpx;
}

.bn-dot {
  position: absolute;
  top: 12rpx;
  right: 50%;
  transform: translateX(20rpx);
  width: 16rpx;
  height: 16rpx;
  border-radius: 50%;
  background-color: var(--badge-unread);
}
</style>
