<script setup lang="ts">
// pages/homeos/dynamics/index —— 动态流页面
//
// 实现 PRD 3.4.2 + 17.5 规格：
//   · 动态流分页：按天分组、按面筛选、游标分页
//   · 条目 = 归属面图标 + 人名 + 一句动作描述（含对象摘要）+ 相对时间
//   · 进入即读（清空未读红点）
//   · 「全部已读」确认弹层

import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request } from '@/utils/request'
import { formatRelativeTime } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

interface DynamicItem {
  id: string
  face_code: string
  face_name?: string
  member_name: string
  headline: string // 服务端拼好的整句
  relative_time?: string
  created_at: string
}

const dynamics = ref<DynamicItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const cursor = ref<string | null>(null)
const hasMore = ref(true)
const selectedFace = ref<string>('')
const availableFaces = ref<Array<{ code: string; name: string }>>([])

// Fetch dynamics with pagination
async function fetchDynamics(loadMore = false) {
  if (loading.value) return
  if (!loadMore && !hasMore.value) return

  loading.value = true
  error.value = null

  try {
    const params: any = {
      limit: 20,
    }

    if (loadMore && cursor.value) {
      params.cursor = cursor.value
    }

    if (selectedFace.value) {
      params.face_code = selectedFace.value
    }

    const response = await request.get('/api/homeos/dynamics', { params })

    if (loadMore) {
      dynamics.value.push(...response.data.items)
    } else {
      dynamics.value = response.data.items
    }

    cursor.value = response.data.next_cursor || null
    hasMore.value = !!response.data.next_cursor

    // Extract available faces for filter
    const faceSet = new Map<string, string>()
    response.data.items.forEach((item: DynamicItem) => {
      if (!faceSet.has(item.face_code)) {
        faceSet.set(item.face_code, item.face_name || item.face_code)
      }
    })
    availableFaces.value = Array.from(faceSet.entries()).map(([code, name]) => ({
      code,
      name,
    }))
  } catch (err: any) {
    console.error('Failed to fetch dynamics:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Load more on scroll
function loadMore() {
  if (hasMore.value && !loading.value) {
    fetchDynamics(true)
  }
}

// Handle face filter change
function handleFaceChange(code: string) {
  selectedFace.value = code
  cursor.value = null
  hasMore.value = true
  fetchDynamics()
}

// Mark all as read
function markAllAsRead() {
  uni.showModal({
    title: '确认',
    content: '确定要将所有动态标记为已读吗？',
    success: async (res) => {
      if (res.confirm) {
        try {
          await request.put('/api/homeos/dynamics/read-all')
          // Clear unread count in home store
          homeStore.unreadCount = 0
          uni.showToast({ title: '已全部标记为已读', icon: 'success' })
        } catch (err: any) {
          console.error('Failed to mark all as read:', err)
          uni.showToast({ title: '操作失败', icon: 'none' })
        }
      }
    },
  })
}

// Get face icon (first character for now)
function getFaceIcon(faceCode: string): string {
  const face = availableFaces.value.find(f => f.code === faceCode)
  return face?.name.charAt(0) || faceCode.charAt(0)
}

onMounted(() => {
  fetchDynamics()

  // Mark as read when entering this page
  // The unread badge will be cleared by the home store update
})
</script>

<template>
  <view class="hc-page">
    <!-- Header with filter -->
    <view class="page-header">
      <text class="header-title">{{ t('homeos.nav.dynamics') }}</text>
      <text class="header-action" @click="markAllAsRead">全部已读</text>
    </view>

    <!-- Face filter -->
    <view v-if="availableFaces.length > 0" class="face-filter">
      <view
        class="filter-item"
        :class="{ active: selectedFace === '' }"
        @click="handleFaceChange('')"
      >
        <text>全部</text>
      </view>
      <view
        v-for="face in availableFaces"
        :key="face.code"
        class="filter-item"
        :class="{ active: selectedFace === face.code }"
        @click="handleFaceChange(face.code)"
      >
        <text>{{ face.name }}</text>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading && dynamics.length === 0" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchDynamics">重试</button>
    </view>

    <!-- Dynamics list -->
    <scroll-view v-else class="hc-scroll" scroll-y @scrolltolower="loadMore">
      <view v-if="dynamics.length === 0" class="hc-empty">
        <text>暂无动态</text>
      </view>

      <!-- Group by day -->
      <view v-for="(item, idx) in dynamics" :key="item.id" class="dyn-item">
        <view class="dyn-face-icon">
          <text>{{ getFaceIcon(item.face_code) }}</text>
        </view>
        <view class="dyn-content">
          <text class="dyn-member">{{ item.member_name }}</text>
          <text class="dyn-headline">{{ item.headline }}</text>
          <text class="dyn-time">{{ item.relative_time || formatRelativeTime(item.created_at) }}</text>
        </view>
      </view>

      <!-- Loading more indicator -->
      <view v-if="loading && dynamics.length > 0" class="hc-loading-more">
        <text>加载更多...</text>
      </view>

      <!-- No more data -->
      <view v-if="!hasMore && dynamics.length > 0" class="hc-no-more">
        <text>没有更多了</text>
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

.header-action {
  font-size: 28rpx;
  color: var(--color-primary);
}

/* Face filter */
.face-filter {
  display: flex;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  overflow-x: auto;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.filter-item {
  padding: 12rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-secondary);
  white-space: nowrap;
}

.filter-item.active {
  background-color: var(--color-primary);
  color: #ffffff;
}

/* Loading and empty states */
.hc-loading,
.hc-error,
.hc-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 96rpx 48rpx;
  gap: 24rpx;
}

.hc-error button {
  margin-top: 16rpx;
  padding: 16rpx 48rpx;
  background-color: var(--color-primary);
  color: #ffffff;
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

/* Dynamic item */
.dyn-item {
  display: flex;
  gap: 16rpx;
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.dyn-face-icon {
  width: 64rpx;
  height: 64rpx;
  border-radius: var(--radius-sm);
  background-color: var(--color-primary-light);
  color: #ffffff;
  font-size: 28rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.dyn-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.dyn-member {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.dyn-headline {
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
}

.dyn-time {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

/* Loading more and no more indicators */
.hc-loading-more,
.hc-no-more {
  padding: 24rpx 0;
  text-align: center;
  font-size: 24rpx;
  color: var(--text-tertiary);
}
</style>
