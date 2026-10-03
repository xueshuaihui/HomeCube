<script setup lang="ts">
// pages/homeos/messages/index —— 站内通知列表（§4.3 ①，PRD 3.4.2 / 3.6 / 17.1）
//
// 接口口径（冻结契约 homeos.yaml 的 `/notifications` 与 `/notifications/read`）：
//   · 列表   GET  /api/homeos/notifications?type=&cursor=&limit=
//            → {items:[{id,type,content,read_at,created_at}], unread, next_cursor}
//            已读**只有** `read_at`（null = 未读），没有 is_read 布尔位。
//   · 已读   POST /api/homeos/notifications/read {notification_ids, all}
//            → {marked_count, unread}
//   旧实现打的 `/api/homeos/board/messages*` 是留言板（L0 `homeos_board_message`，
//   3.4.3），与站内通知不是一张表，本轮整体改齐。
//
// 未读单一源（17.1）：本页**不**用 items 相减出一个计数，也**不**把列表长度当计数。
// 聚合值只有两条写入通道 —— 首屏 `home/summary.unread` 与 notifications / read 回执，
// 两者都是服务端同一个 `read_at IS NULL` 口径（3.6），落进 shell store 的同一份 `unread`。
// 顶栏红点与 D 行「消息」红点 + 计数读的就是它，因此「进入本页即读」时两处同时熄灭。
//
// 分型未读计数（17.1 第 3 条「消息页内按三分型各显未读计数」）取服务端的
// `unread_by_type`；契约 yaml 的 `/notifications` 200 里**没有**这个字段（已作为定版冲突上报），
// 缺字段时三个分型 tab 不显角标 —— 宁可少显，也不在客户端数一份出来。

import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, unwrapBody } from '@/utils/request'
import { formatRelativeTime } from '@/utils/format'
import { useHomeStore, type NotificationItem, type NotificationType } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

const TAB_TYPES: NotificationType[] = ['budget_alert', 'system', 'reminder']
const PAGE_SIZE = 20

type TabKey = NotificationType | 'all'

const items = ref<NotificationItem[]>([])
const activeTab = ref<TabKey>('all')
const loading = ref(false)
const error = ref('')
const cursor = ref<string | null>(null)
const hasMore = ref(true)
const marking = ref(false)

function typeLabel(type: NotificationType): string {
  return t(`homeos.messages.type_${type}`)
}

/** 行内「进入时是否未读」的快照：聚合值被「进入即读」清零后，行仍要说明它当时是新的。 */
function wasUnreadAtArrival(item: NotificationItem): boolean {
  return (arrivalReadAt.get(item.id) ?? item.read_at) === null
}
const arrivalReadAt = new Map<string, string | null>()

async function fetchList(append = false) {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const res = await request.get('/api/homeos/notifications', {
      params: {
        type: activeTab.value === 'all' ? undefined : activeTab.value,
        cursor: append ? cursor.value || undefined : undefined,
        limit: PAGE_SIZE,
      },
    })
    const body = unwrapBody<{
      items?: NotificationItem[]
      unread?: number
      unread_by_type?: Partial<Record<NotificationType, number>>
      next_cursor?: string | null
    }>(res)

    const page = Array.isArray(body?.items) ? body.items : []
    items.value = append ? [...items.value, ...page] : page
    for (const item of page) arrivalReadAt.set(item.id, item.read_at ?? null)
    cursor.value = body?.next_cursor ?? null
    hasMore.value = !!body?.next_cursor

    // 未读与分型未读都取服务端回执，不在客户端统计。
    homeStore.setNotificationsUnread(
      typeof body?.unread === 'number' ? body.unread : homeStore.unread,
      body?.unread_by_type
    )
  } catch (err: any) {
    error.value = err?.message || t('homeos.messages.load_failed')
  } finally {
    loading.value = false
  }
}

function loadMore() {
  if (!hasMore.value || loading.value) return
  fetchList(true)
}

/**
 * 类型筛选切换（页面内的筛选条，不是底部导航）。命名刻意避开 uni 的底部导航语义名，
 * 三查① 要求 pages.json 里不存在那个构造物。
 */
async function switchMsgType(tab: TabKey) {
  if (activeTab.value === tab) return
  activeTab.value = tab
  cursor.value = null
  hasMore.value = true
  await fetchList()
}

/**
 * 单条已读：POST /notifications/read 带 notification_ids。
 * 回执里的 `unread` 就是顶栏与 D 行共用的那一个数，直接覆盖，不做客户端相减。
 */
async function markOneRead(item: NotificationItem) {
  if (item.read_at) return
  try {
    await homeStore.markNotificationsRead({ ids: [item.id] })
    item.read_at = new Date().toISOString()
  } catch (err: any) {
    uni.showToast({ title: err?.message || t('homeos.messages.mark_failed'), icon: 'none' })
  }
}

/**
 * 「全部已读」走弹层（§4.3 ①）。确认后回执的 marked_count 决定文案，
 * 为 0 时不谎报「已清空」。
 */
function markAllRead() {
  uni.showModal({
    title: t('homeos.messages.all_read'),
    content: t('homeos.messages.all_read_confirm'),
    confirmText: t('homeos.common.confirm'),
    cancelText: t('homeos.common.cancel'),
    success: async (res) => {
      if (!res.confirm || marking.value) return
      marking.value = true
      try {
        const body = await homeStore.markNotificationsRead({ all: true })
        const marked = typeof body?.marked_count === 'number' ? body.marked_count : 0
        for (const item of items.value) {
          if (!item.read_at) item.read_at = new Date().toISOString()
        }
        uni.showToast({
          title: marked > 0 ? t('homeos.messages.all_read_done', { count: marked }) : t('homeos.messages.all_read_none'),
          icon: 'none',
        })
      } catch (err: any) {
        uni.showToast({ title: err?.message || t('homeos.messages.mark_failed'), icon: 'none' })
      } finally {
        marking.value = false
      }
    },
  })
}

/**
 * 行点击：先把这条标为已读。**不做跳转** ——
 * §2.5 定 `homeos/notification` 不独立落地：通知是投递记录，落点是它携带的目标对象深链，
 * 而契约 `GET /notifications` 的 items[] 只有 `{id,type,content,read_at,created_at}`，
 * 没有 code/entity/id 可用（已作为定版冲突上报）。因此这里既不猜路也不静默 no-op，
 * 行尾显式标「无跳转目标」（未开放），跳转能力待投递记录补上目标字段后再接。
 */
function onItemTap(item: NotificationItem) {
  markOneRead(item)
}

onMounted(async () => {
  await fetchList()
  // 进入本页即读：顶栏红点与 D 行红点同时熄灭（§4.3 ①、17.1 第 4 条）。
  // 行的「新」标记保留为到达快照，只有聚合计数熄灭。
  if (homeStore.unread > 0) {
    try {
      await homeStore.markNotificationsRead({ all: true })
    } catch {
      // 标已读失败不影响列表呈现：下一次进入本页或首屏仍会拿到服务端口径的 unread。
    }
  }
})
</script>

<template>
  <view class="hc-page">
    <!-- Header -->
    <view class="page-header">
      <text class="header-title">{{ t('homeos.nav.messages') }}</text>
      <text class="header-action" @click="markAllRead">{{ t('homeos.messages.all_read') }}</text>
    </view>

    <!-- Type tabs：分型角标只来自服务端 unread_by_type，缺字段即不显 -->
    <view class="type-tabs">
      <view class="tab-item" :class="{ active: activeTab === 'all' }" @click="switchMsgType('all')">
        <text>{{ t('homeos.messages.tab_all') }}</text>
      </view>
      <view
        v-for="type in TAB_TYPES"
        :key="type"
        class="tab-item"
        :class="{ active: activeTab === type }"
        @click="switchMsgType(type)"
      >
        <text>{{ typeLabel(type) }}</text>
        <view v-if="homeStore.unreadByType[type] > 0" class="tab-badge">
          <text>{{ homeStore.unreadByType[type] > 99 ? '99+' : homeStore.unreadByType[type] }}</text>
        </view>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading && items.length === 0" class="hc-loading">
      <text>{{ t('homeos.messages.loading') }}</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchList()">{{ t('homeos.common.retry') }}</button>
    </view>

    <!-- Notification list -->
    <scroll-view v-else class="hc-scroll" scroll-y @scrolltolower="loadMore">
      <view v-if="items.length === 0" class="hc-empty">
        <text>{{ t('homeos.messages.empty') }}</text>
      </view>

      <view
        v-for="msg in items"
        :key="msg.id"
        class="msg-item"
        :class="{ unread: wasUnreadAtArrival(msg) }"
        @click="onItemTap(msg)"
      >
        <view class="msg-header">
          <view class="msg-type-badge">
            <text>{{ typeLabel(msg.type) }}</text>
          </view>
          <text v-if="wasUnreadAtArrival(msg)" class="msg-unread-dot" />
        </view>
        <text class="msg-content">{{ msg.content }}</text>
        <view class="msg-footer">
          <text class="msg-no-target">{{ t('homeos.messages.no_target') }}</text>
          <text class="msg-time">{{ formatRelativeTime(msg.created_at) }}</text>
        </view>
      </view>

      <view v-if="hasMore && items.length > 0" class="list-more" @click="loadMore">
        <text>{{ loading ? t('homeos.messages.loading') : t('homeos.messages.load_more') }}</text>
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
  color: var(--color-on-primary);
}

.tab-badge {
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

.tab-item.active .tab-badge {
  background-color: var(--color-on-primary);
  color: var(--color-primary);
}

/* Loading / error / empty */
.hc-loading,
.hc-error,
.hc-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 96rpx 48rpx;
  gap: 24rpx;
  color: var(--text-tertiary);
}

.hc-error button {
  margin-top: 16rpx;
  padding: 16rpx 48rpx;
  background-color: var(--color-primary);
  color: var(--color-on-primary);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

/* Notification item */
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

.msg-content {
  display: block;
  font-size: 28rpx;
  color: var(--text-primary);
  line-height: var(--line-height-normal);
  margin-bottom: 12rpx;
}

.msg-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

/* 契约的通知项不带跳转目标：这一行是「未开放」的显式说明，不是占位装饰 */
.msg-no-target {
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.msg-time {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.list-more {
  padding: 24rpx;
  text-align: center;
  font-size: 26rpx;
  color: var(--text-secondary);
}
</style>
