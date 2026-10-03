<script setup lang="ts">
// pages/finance/trash/index —— 回收站页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 回收站列表：展示30天内删除的流水记录
//   · 恢复记录：将删除的记录恢复到正常状态
//   · 彻底删除：永久删除记录（不可恢复）
//   · 清空回收站：清空所有已过期的记录
//   · 对接 GET /api/finance/trash?family_id=
//     POST /api/finance/trash/:id/restore
//     DELETE /api/finance/trash/:id
//     POST /api/finance/trash/clear-expired

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount, formatDate } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface TrashItem {
  id: string
  family_id: string
  original_type: 'transaction' | 'category' | 'account' | 'budget' | 'bill'
  original_id: string
  deleted_at: string
  expires_at: string
  data: Record<string, any>
  can_restore: boolean
}

const trashItems = ref<TrashItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const activeFilter = ref<'all' | 'transaction' | 'category' | 'account'>('all')

const homeStore = useHomeStore()

async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

async function fetchTrash() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      trashItems.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const params: Record<string, any> = { family_id: familyId }
    if (activeFilter.value !== 'all') {
      params.type = activeFilter.value
    }

    const body = await request.get<{ items?: TrashItem[] }>('/api/finance/trash', { params })
    trashItems.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch trash:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function restoreItem(item: TrashItem) {
  if (!item.can_restore) {
    uni.showToast({ title: '该记录已过期，无法恢复', icon: 'none' })
    return
  }

  uni.showModal({
    title: '恢复记录',
    content: `确定要恢复这条${getTypeLabel(item.original_type)}记录吗？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.post(`/api/finance/trash/${item.id}/restore`)
        uni.showToast({ title: '恢复成功', icon: 'success' })
        await fetchTrash()
      } catch (err: any) {
        console.error('Failed to restore item:', err)
        uni.showToast({ title: err.message || '恢复失败', icon: 'none' })
      }
    },
  })
}

function permanentlyDelete(item: TrashItem) {
  uni.showModal({
    title: '彻底删除',
    content: '确定要永久删除这条记录吗？此操作不可恢复！',
    confirmText: '删除',
    confirmColor: 'var(--color-error)',
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.delete(`/api/finance/trash/${item.id}`)
        uni.showToast({ title: '已永久删除', icon: 'success' })
        await fetchTrash()
      } catch (err: any) {
        console.error('Failed to delete item:', err)
        uni.showToast({ title: err.message || '删除失败', icon: 'none' })
      }
    },
  })
}

async function clearExpired() {
  uni.showModal({
    title: '清空过期记录',
    content: '确定要清空所有已过期的回收站记录吗？',
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.post('/api/finance/trash/clear-expired')
        uni.showToast({ title: '已清空', icon: 'success' })
        await fetchTrash()
      } catch (err: any) {
        console.error('Failed to clear expired:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

function getTypeLabel(type: string): string {
  const labels: Record<string, string> = {
    transaction: '流水',
    category: '分类',
    account: '账户',
    budget: '预算',
    bill: '账单',
  }
  return labels[type] || type
}

function getDaysRemaining(expiresAt: string): number {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diffMs = expires.getTime() - now.getTime()
  return Math.max(0, Math.ceil(diffMs / (1000 * 60 * 60 * 24)))
}

// Computed: filtered items
const filteredItems = computed(() => {
  if (activeFilter.value === 'all') return trashItems.value
  return trashItems.value.filter((item) => item.original_type === activeFilter.value)
})

// Computed: expired count
const expiredCount = computed(() =>
  trashItems.value.filter((item) => !item.can_restore).length
)

onMounted(() => {
  fetchTrash()
})
</script>

<template>
  <view class="hc-page">
    <!-- Summary bar -->
    <view class="summary-bar">
      <view class="summary-item">
        <text class="summary-label">回收站总数</text>
        <text class="summary-value">{{ trashItems.length }}</text>
      </view>
      <view class="summary-item">
        <text class="summary-label">已过期</text>
        <text class="summary-value summary-expired">{{ expiredCount }}</text>
      </view>
    </view>

    <!-- Filter tabs -->
    <view class="filter-bar">
      <view
        v-for="filter in [
          { key: 'all', label: '全部' },
          { key: 'transaction', label: '流水' },
          { key: 'category', label: '分类' },
          { key: 'account', label: '账户' },
        ]"
        :key="filter.key"
        class="filter-item"
        :class="{ active: activeFilter === filter.key }"
        @click="activeFilter = filter.key as any"
      >
        <text>{{ filter.label }}</text>
      </view>
    </view>

    <!-- Action bar -->
    <view v-if="expiredCount > 0" class="action-bar">
      <text class="action-text" @click="clearExpired">清空过期记录</text>
    </view>

    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchTrash">重试</button>
    </view>

    <!-- Trash list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="filteredItems.length === 0" class="hc-empty">
        <text>回收站为空</text>
      </view>

      <view v-for="item in filteredItems" :key="item.id" class="trash-item">
        <view class="trash-header">
          <view class="trash-info">
            <text class="trash-type">{{ getTypeLabel(item.original_type) }}</text>
            <text v-if="!item.can_restore" class="trash-expired-badge">已过期</text>
          </view>
          <text class="trash-days">剩余{{ getDaysRemaining(item.expires_at) }}天</text>
        </view>

        <view class="trash-content">
          <text class="trash-detail">删除时间: {{ formatDate(item.deleted_at) }}</text>
          <text class="trash-detail">到期时间: {{ formatDate(item.expires_at) }}</text>
        </view>

        <view class="trash-actions">
          <text
            v-if="item.can_restore"
            class="trash-action trash-restore"
            @click="restoreItem(item)"
          >
            恢复
          </text>
          <text class="trash-action trash-delete" @click="permanentlyDelete(item)">
            彻底删除
          </text>
        </view>
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

.summary-bar {
  display: flex;
  justify-content: space-around;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.summary-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
}

.summary-label {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.summary-value {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.summary-expired {
  color: var(--color-error);
}

.filter-bar {
  display: flex;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.filter-item {
  flex: 1;
  padding: 20rpx 0;
  text-align: center;
  font-size: 28rpx;
  color: var(--text-secondary);
}

.filter-item.active {
  color: var(--color-primary);
  font-weight: 600;
  border-bottom: 4rpx solid var(--color-primary);
}

.action-bar {
  padding: 16rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
  text-align: right;
}

.action-text {
  font-size: 26rpx;
  color: var(--color-error);
}

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
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

.trash-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.trash-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.trash-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.trash-type {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.trash-expired-badge {
  padding: 4rpx 12rpx;
  background-color: var(--color-error);
  color: var(--color-white);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.trash-days {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.trash-content {
  margin-bottom: 12rpx;
}

.trash-detail {
  display: block;
  font-size: 24rpx;
  color: var(--text-secondary);
  margin-bottom: 4rpx;
}

.trash-actions {
  display: flex;
  gap: 24rpx;
  justify-content: flex-end;
}

.trash-action {
  font-size: 26rpx;
}

.trash-restore {
  color: var(--color-success);
}

.trash-delete {
  color: var(--color-error);
}
</style>
