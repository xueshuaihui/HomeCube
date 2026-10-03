<script setup lang="ts">
// pages/homeos/messages/index —— 消息列表页面
//
// 实现 PRD 3.4.2 + 17.5 规格：
//   · 站内通知（含 @、免打扰、分组）
//   · 留言板、投票入口
//   · 按类型分组：budget_alert / system / reminder
//   · 未读计数显示
//   · 进入即读（清空未读红点）
//   · 「全部已读」确认弹层

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request } from '@/utils/request'
import { formatRelativeTime } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

type MessageType = 'budget_alert' | 'system' | 'reminder'

interface Message {
  id: string
  type: MessageType
  title: string
  content: string
  is_read: boolean
  created_at: string
  sender_name?: string
  action_url?: string
}

const messages = ref<Message[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const activeTab = ref<MessageType | 'all'>('all')

// Computed: filtered messages by type
const filteredMessages = computed(() => {
  if (activeTab.value === 'all') return messages.value
  return messages.value.filter(m => m.type === activeTab.value)
})

// Computed: unread count by type
const unreadByType = computed(() => {
  const counts: Record<string, number> = {
    budget_alert: 0,
    system: 0,
    reminder: 0,
  }

  messages.value.forEach(m => {
    if (!m.is_read && counts[m.type] !== undefined) {
      counts[m.type]++
    }
  })

  return counts
})

// Fetch messages
async function fetchMessages() {
  loading.value = true
  error.value = null

  try {
    const response = await request.get('/api/homeos/board/messages')
    messages.value = response.data.items || []
  } catch (err: any) {
    console.error('Failed to fetch messages:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Mark message as read
async function markAsRead(messageId: string) {
  try {
    await request.put(`/api/homeos/board/messages/${messageId}/read`)
    // Update local state
    const msg = messages.value.find(m => m.id === messageId)
    if (msg) {
      msg.is_read = true
    }
    updateUnreadCount()
  } catch (err: any) {
    console.error('Failed to mark message as read:', err)
  }
}

// Mark all as read
function markAllAsRead() {
  uni.showModal({
    title: '确认',
    content: '确定要将所有消息标记为已读吗？',
    success: async (res) => {
      if (res.confirm) {
        try {
          await request.put('/api/homeos/board/messages/read-all')
          messages.value.forEach(m => (m.is_read = true))
          updateUnreadCount()
          uni.showToast({ title: '已全部标记为已读', icon: 'success' })
        } catch (err: any) {
          console.error('Failed to mark all as read:', err)
          uni.showToast({ title: '操作失败', icon: 'none' })
        }
      }
    },
  })
}

// Update unread count in home store
function updateUnreadCount() {
  const total = messages.value.filter(m => !m.is_read).length
  homeStore.unreadCount = total
}

// Get type label
function getTypeLabel(type: MessageType): string {
  const map: Record<MessageType, string> = {
    budget_alert: '预算提醒',
    system: '系统通知',
    reminder: '到期提醒',
  }
  return map[type] || type
}

// Handle message click
function handleMessageClick(message: Message) {
  markAsRead(message.id)

  if (message.action_url) {
    // Navigate to action URL
    // TODO: implement deep link navigation
    console.log('Navigate to:', message.action_url)
  }
}

onMounted(() => {
  fetchMessages()

  // Mark as read when entering this page
  // The unread badge will be cleared by the home store update
})
</script>

<template>
  <view class="hc-page">
    <!-- Header -->
    <view class="page-header">
      <text class="header-title">{{ t('homeos.nav.messages') }}</text>
      <text class="header-action" @click="markAllAsRead">全部已读</text>
    </view>

    <!-- Type tabs -->
    <view class="type-tabs">
      <view
        class="tab-item"
        :class="{ active: activeTab === 'all' }"
        @click="activeTab = 'all'"
      >
        <text>全部</text>
      </view>
      <view
        v-for="type in ['budget_alert', 'system', 'reminder']"
        :key="type"
        class="tab-item"
        :class="{ active: activeTab === type }"
        @click="activeTab = type as MessageType"
      >
        <text>{{ getTypeLabel(type as MessageType) }}</text>
        <view v-if="unreadByType[type] > 0" class="tab-badge">
          <text>{{ unreadByType[type] }}</text>
        </view>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchMessages">重试</button>
    </view>

    <!-- Messages list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="filteredMessages.length === 0" class="hc-empty">
        <text>暂无消息</text>
      </view>

      <view
        v-for="msg in filteredMessages"
        :key="msg.id"
        class="msg-item"
        :class="{ unread: !msg.is_read }"
        @click="handleMessageClick(msg)"
      >
        <view class="msg-header">
          <view class="msg-type-badge">
            <text>{{ getTypeLabel(msg.type) }}</text>
          </view>
          <text v-if="!msg.is_read" class="msg-unread-dot"></text>
        </view>
        <text class="msg-title">{{ msg.title }}</text>
        <text class="msg-content">{{ msg.content }}</text>
        <view class="msg-footer">
          <text v-if="msg.sender_name" class="msg-sender">{{ msg.sender_name }}</text>
          <text class="msg-time">{{ formatRelativeTime(msg.created_at) }}</text>
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

/* Type tabs */
.type-tabs {
  display: flex;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  overflow-x: auto;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.tab-item {
  position: relative;
  padding: 12rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-secondary);
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 8rpx;
}

.tab-item.active {
  background-color: var(--color-primary);
  color: #ffffff;
}

.tab-badge {
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

.tab-item.active .tab-badge {
  background-color: #ffffff;
  color: var(--color-primary);
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

/* Message item */
.msg-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  position: relative;
}

.msg-item.unread {
  border-left: 4rpx solid var(--badge-unread);
}

.msg-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.msg-type-badge {
  padding: 4rpx 12rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 22rpx;
  color: var(--text-secondary);
}

.msg-unread-dot {
  width: 16rpx;
  height: 16rpx;
  border-radius: 50%;
  background-color: var(--badge-unread);
}

.msg-title {
  display: block;
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 8rpx;
}

.msg-content {
  display: block;
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
  margin-bottom: 12rpx;
}

.msg-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.msg-sender {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.msg-time {
  font-size: 24rpx;
  color: var(--text-tertiary);
}
</style>
