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

import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useHomeStore, type PeriodType } from '@/stores/home'
import { request } from '@/utils/request'

const PAGE_PATH = 'pages/homeos/home/index'
const { t } = useI18n({ useScope: 'global' })
const router = useRouter()
const homeStore = useHomeStore()

const loading = ref(false)
const error = ref<string | null>(null)

// Fetch home summary data
async function fetchHomeSummary() {
  loading.value = true
  error.value = null

  try {
    const response = await request.get('/api/homeos/home/summary', {
      params: { period: homeStore.period },
    })
    homeStore.updateSummary(response.data)
  } catch (err: any) {
    console.error('Failed to fetch home summary:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Handle period change
async function handlePeriodChange(p: PeriodType) {
  homeStore.setPeriod(p)
  await fetchHomeSummary()
}

// Navigate to member management page
function goToMembers() {
  // TODO: implement member management page
  console.log('Navigate to member management')
}

// Navigate to calendar/due center
function goToCalendar() {
  // TODO: implement calendar page
  console.log('Navigate to calendar')
}

// Navigate to face detail
function goToFace(faceCode: string) {
  if (!faceCode) return
  // Only navigate to faces that are registered in pages.json
  // P1: only finance is born, others will be added in future phases
  const routeMap: Record<string, string> = {
    finance: '/pages/finance/flow/index',
  }
  const path = routeMap[faceCode]
  if (path) {
    router.push(path)
  } else {
    // For faces not yet implemented, show a toast
    uni.showToast({ title: '该面功能开发中', icon: 'none' })
  }
}

// Navigate to dynamics page
function goToDynamics() {
  router.push('/pages/homeos/dynamics/index')
}

// Format relative time for dynamic items
function formatRelativeTime(isoString: string): string {
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

// Get avatar URL or fallback to first character
function getAvatarUrl(member: any): string | null {
  return member.avatar || null
}

function getAvatarText(member: any): string {
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
    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchHomeSummary">重试</button>
    </view>

    <!-- Main content -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- Time window bar -->
      <view class="time-window-bar">
        <text class="twb-label">{{ t('homeos.home.period_label') }}</text>
        <view class="twb-segments">
          <view
            v-for="p in ['month', 'quarter', 'year']"
            :key="p"
            class="twb-segment"
            :class="{ active: homeStore.period === p }"
            @click="handlePeriodChange(p as PeriodType)"
          >
            <text>{{ t(`homeos.home.period_${p}`) }}</text>
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
            v-for="(member, idx) in (homeStore.family?.members || []).slice(0, 3)"
            :key="member.user_id"
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
          <text class="zb-title">今日 {{ homeStore.dueToday.count }} 项 · 到期与待办</text>
          <view class="zb-calendar-link" @click="goToCalendar">
            <text>日历 ›</text>
          </view>
        </view>

        <view v-if="homeStore.dueToday.items.length > 0" class="zb-timeline">
          <view
            v-for="item in homeStore.dueToday.items.slice(0, 3)"
            :key="item.id"
            class="zb-card"
            :style="{ borderLeftColor: `var(--color-${item.source_code}, var(--color-primary))` }"
          >
            <text class="zb-time">{{ extractTime(item.due_at) }}</text>
            <text class="zb-headline">{{ item.headline }}</text>
          </view>
        </view>
        <view v-else class="zb-empty">
          <text>今日暂无事项</text>
        </view>
      </view>

      <!-- Zone C: Face matrix -->
      <view class="zone-c">
        <view class="zc-header">
          <text class="zc-title">已启用 {{ homeStore.faces.length }} 面</text>
          <text class="zc-hint">开通在「我的」</text>
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
            :class="{ unavailable: !face.available }"
            @click="goToFace(face.code)"
          >
            <!-- Badge for pending count -->
            <view v-if="face.badge_count && face.badge_count > 0" class="zc-badge">
              <text>{{ face.badge_count > 99 ? '99+' : face.badge_count }}</text>
            </view>

            <!-- Face icon placeholder (using text for now, should be icon font) -->
            <view class="zc-icon">
              <text>{{ face.name.charAt(0) }}</text>
            </view>

            <text class="zc-name">{{ face.name }}</text>
            <text class="zc-status">{{ face.status_sentence }}</text>

            <!-- Unavailable overlay -->
            <view v-if="!face.available" class="zc-unavailable">
              <text class="zc-unavail-text">{{ face.unavailable_reason || '暂不可用' }}</text>
              <text v-if="face.last_updated_at" class="zc-unavail-time">截至 {{ face.last_updated_at }}</text>
              <button class="zc-retry-btn" @click.stop="fetchHomeSummary">重试</button>
            </view>
          </view>
        </view>
      </view>

      <!-- Zone D: Dynamics feed -->
      <view class="zone-d">
        <view class="zd-header">
          <text class="zd-title">家庭动态</text>
          <view class="zd-view-all" @click="goToDynamics">
            <text>查看全部 ›</text>
          </view>
        </view>

        <view v-if="homeStore.dynamics.length > 0" class="zd-list">
          <view
            v-for="item in homeStore.dynamics.slice(0, 20)"
            :key="item.id"
            class="zd-item"
          >
            <view class="zd-face-icon">
              <text>{{ item.face_code }}</text>
            </view>
            <view class="zd-content">
              <text class="zd-member">{{ item.member_name }}</text>
              <text class="zd-headline">{{ item.headline }}</text>
              <text class="zd-time">{{ formatRelativeTime(item.created_at) }}</text>
            </view>
          </view>
        </view>
        <view v-else class="zd-empty">
          <text>暂无动态</text>
        </view>
      </view>
    </scroll-view>

    <!-- Bottom navigation -->
    <view class="bottom-nav">
      <view class="bn-item" @click="router.push('/pages/homeos/home/index')">
        <text class="bn-icon">🏠</text>
        <text class="bn-label bn-active">{{ t('homeos.nav.home') }}</text>
      </view>
      <view class="bn-item" @click="goToDynamics">
        <text class="bn-icon">💬</text>
        <text class="bn-label">{{ t('homeos.nav.dynamics') }}</text>
        <view v-if="homeStore.unreadCount > 0" class="bn-badge">
          <text>{{ homeStore.unreadCount > 99 ? '99+' : homeStore.unreadCount }}</text>
        </view>
      </view>
      <view class="bn-item bn-plus" @click="router.push('/pages/homeos/quick-add/index')">
        <text class="bn-plus-icon">＋</text>
      </view>
      <view class="bn-item" @click="router.push('/pages/homeos/messages/index')">
        <text class="bn-icon">🔔</text>
        <text class="bn-label">{{ t('homeos.nav.messages') }}</text>
        <view v-if="homeStore.unreadCount > 0" class="bn-dot" />
      </view>
      <view class="bn-item" @click="router.push('/pages/homeos/mine/index')">
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
  color: #ffffff;
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
  color: #ffffff;
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
  font-size: 28rpx;
  color: var(--color-primary);
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
  color: #ffffff;
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
  color: #ffffff;
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
  background-color: rgba(0, 0, 0, 0.5);
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
  color: #ffffff;
  font-weight: 600;
}

.zc-unavail-time {
  font-size: 24rpx;
  color: rgba(255, 255, 255, 0.8);
}

.zc-retry-btn {
  margin-top: 12rpx;
  padding: 12rpx 32rpx;
  background-color: #ffffff;
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
  color: #ffffff;
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
  color: #ffffff;
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
  color: #ffffff;
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
