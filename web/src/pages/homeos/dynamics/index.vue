<script setup lang="ts">
// pages/homeos/dynamics/index —— 动态流页（§4.3 ①「动态流」）
//
// 能力：按天分组、按面筛选、游标分页。条目五要素 = 面图标 + 发起人 + 动作句 + 对象摘要 + 相对时间
// （§3.3 D 区口径，首页 D 区与本页共用同一份字段名）。
//
// 字段名以冻结契约与服务端表为准：`dynamics.items[]` = `{id, code, actor_name, action, summary, at}`。
// 旧客户端读的 `face_code / member_name / headline / created_at` 都是自造名，本轮整体改齐；
// TS 接口直接复用 `@/stores/home` 的 `DynamicItem`，不再在本页声明第二份形状。
//
// 筛选 chips 的集合**不来自本页响应**：面集合的五个消费位（首页矩阵、「＋」目标、搜索分组、
// 到期中心注册项、动态流筛选）同源于 shell store 的 `faces`（§1.3 第 6/8 条、17.8）。
// 从返回条目里现攒一份面清单会造出第二个集合 —— 翻页少一条就少一个 chip，
// 而且「这一面有没有被启用」会变成由列表长度决定，正是同源检查②要拦的形状。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request, unwrapBody } from '@/utils/request'
import { formatRelativeTime } from '@/utils/format'
import { useHomeStore, type DynamicItem } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

const PAGE_SIZE = 20

const dynamics = ref<DynamicItem[]>([])
const loading = ref(false)
const error = ref('')
const cursor = ref<string | null>(null)
const hasMore = ref(true)
const selectedFace = ref('')
const marking = ref(false)

/** 筛选集合 = shell 的同一份 faces（服务端已按角色裁好，客户端不得二次裁剪，⑯）。 */
const filterFaces = computed(() => homeStore.faces.map((f) => ({ code: f.code, name: f.name })))

/** 面图标取 C 区同一个 face 的名首字，不在本页另建第二套缩写（§3.3 第 295 行）。 */
function faceInitial(code: string): string {
  const face = homeStore.faces.find((f) => f.code === code)
  return face ? face.name.charAt(0) : code.charAt(0)
}

function dayKey(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`
}

/** 按天分组：组标题取「今天 / 昨天 / M月D日」，分组在渲染层做，不改服务端给的顺序。 */
const groups = computed<Array<{ key: string; label: string; items: DynamicItem[] }>>(() => {
  const todayKey = dayKey(new Date().toISOString())
  const yesterday = new Date()
  yesterday.setDate(yesterday.getDate() - 1)
  const yesterdayKey = dayKey(yesterday.toISOString())

  const out: Array<{ key: string; label: string; items: DynamicItem[] }> = []
  for (const item of dynamics.value) {
    const key = dayKey(item.at)
    const last = out[out.length - 1]
    if (last && last.key === key) {
      last.items.push(item)
      continue
    }
    const date = new Date(item.at)
    let label = ''
    if (key === todayKey) label = t('homeos.dynamics.day_today')
    else if (key === yesterdayKey) label = t('homeos.dynamics.day_yesterday')
    else if (!Number.isNaN(date.getTime())) label = `${date.getMonth() + 1}月${date.getDate()}日`
    out.push({ key: `${key}|${item.id}`, label, items: [item] })
  }
  return out
})

async function fetchDynamics(loadMore = false) {
  if (loading.value) return
  loading.value = true
  error.value = ''

  try {
    const res = await request.get('/api/homeos/dynamics', {
      params: {
        limit: PAGE_SIZE,
        cursor: loadMore && cursor.value ? cursor.value : undefined,
        // 筛选参数名与条目字段名一致：`code`（旧的 face_code 是自造名）
        code: selectedFace.value || undefined,
        period: homeStore.period,
      },
    })
    const body = unwrapBody<{
      items?: DynamicItem[]
      next_cursor?: string | null
      unread?: number
    }>(res)

    const page = Array.isArray(body?.items) ? body.items : []
    dynamics.value = loadMore ? [...dynamics.value, ...page] : page
    cursor.value = body?.next_cursor ?? null
    hasMore.value = !!body?.next_cursor
  } catch (err: any) {
    error.value = err?.message || t('homeos.dynamics.load_failed')
  } finally {
    loading.value = false
  }
}

function loadMore() {
  if (!hasMore.value || loading.value) return
  fetchDynamics(true)
}

async function handleFaceChange(code: string) {
  if (selectedFace.value === code) return
  selectedFace.value = code
  cursor.value = null
  hasMore.value = true
  await fetchDynamics()
}

/**
 * 「全部已读」：`POST /api/homeos/dynamics/read` `{dynamic_ids:[], all:true}`
 * → `{marked_count, unread}`。回执里的 `unread` 就是顶栏与 D 行共用的那一个数，
 * 由 store 覆盖写入，本页不自算、不清零（17.1）。
 */
function markAllAsRead() {
  uni.showModal({
    title: t('homeos.dynamics.all_read'),
    content: t('homeos.dynamics.all_read_confirm'),
    confirmText: t('homeos.common.confirm'),
    cancelText: t('homeos.common.cancel'),
    success: async (res) => {
      if (!res.confirm || marking.value) return
      marking.value = true
      try {
        const body = await homeStore.markDynamicsRead({ all: true })
        const marked = typeof body?.marked_count === 'number' ? body.marked_count : 0
        uni.showToast({
          title: marked > 0 ? t('homeos.dynamics.all_read_done', { count: marked }) : t('homeos.dynamics.all_read_none'),
          icon: 'none',
        })
      } catch (err: any) {
        uni.showToast({ title: err?.message || t('homeos.dynamics.mark_failed'), icon: 'none' })
      } finally {
        marking.value = false
      }
    },
  })
}

onMounted(() => {
  fetchDynamics()
})
</script>

<template>
  <view class="hc-page">
    <!-- Header with filter -->
    <view class="page-header">
      <text class="header-title">{{ t('homeos.nav.dynamics') }}</text>
      <text class="header-action" @click="markAllAsRead">{{ t('homeos.dynamics.all_read') }}</text>
    </view>

    <!-- Face filter：与首页矩阵同源，只有一个 chip 时不渲染整条筛选栏 -->
    <view v-if="filterFaces.length > 1" class="face-filter">
      <view
        class="filter-item"
        :class="{ active: selectedFace === '' }"
        @click="handleFaceChange('')"
      >
        <text>{{ t('homeos.dynamics.filter_all') }}</text>
      </view>
      <view
        v-for="face in filterFaces"
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
      <text>{{ t('homeos.dynamics.loading') }}</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchDynamics()">{{ t('homeos.common.retry') }}</button>
    </view>

    <!-- Dynamics list -->
    <scroll-view v-else class="hc-scroll" scroll-y @scrolltolower="loadMore">
      <view v-if="dynamics.length === 0" class="hc-empty">
        <text>{{ t('homeos.dynamics.empty') }}</text>
      </view>

      <view v-for="group in groups" :key="group.key" class="dyn-group">
        <text v-if="group.label" class="dyn-day">{{ group.label }}</text>
        <view v-for="item in group.items" :key="item.id" class="dyn-item">
          <view class="dyn-face-icon">
            <text>{{ faceInitial(item.code) }}</text>
          </view>
          <view class="dyn-content">
            <text class="dyn-member">{{ item.actor_name }}</text>
            <text class="dyn-action">{{ item.action }}</text>
            <text v-if="item.summary" class="dyn-summary">{{ item.summary }}</text>
            <text class="dyn-time">{{ formatRelativeTime(item.at) }}</text>
          </view>
        </view>
      </view>

      <view v-if="loading && dynamics.length > 0" class="hc-loading-more">
        <text>{{ t('homeos.dynamics.loading') }}</text>
      </view>
      <view v-if="!hasMore && dynamics.length > 0" class="hc-no-more">
        <text>{{ t('homeos.dynamics.no_more') }}</text>
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
  color: var(--color-on-primary);
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

/* Day group */
.dyn-day {
  display: block;
  font-size: 24rpx;
  color: var(--text-tertiary);
  margin: 16rpx 0 12rpx;
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
  color: var(--color-on-primary);
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

.dyn-action {
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: var(--line-height-tight);
}

.dyn-summary {
  font-size: 24rpx;
  color: var(--text-tertiary);
  line-height: var(--line-height-normal);
}

.dyn-time {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

/* Pagination indicators */
.hc-loading-more,
.hc-no-more {
  padding: 24rpx 0;
  text-align: center;
  font-size: 24rpx;
  color: var(--text-tertiary);
}
</style>
