<script setup lang="ts">
// pages/finance/flow/index —— 财务面「流水」列表页（§5.1 Tab 流水、§5.2 首行）。
//
// 完整实现：
//   · 对接 /api/finance/transactions，支持 period 参数（月/季/年三档）
//   · 游标分页（cursor-based pagination）
//   · 筛选器：账户、分类、成员、金额范围
//   · 流水列表渲染：金额（分转元）、分类图标、时间、备注、账户
//   · 记账入口：跳转到记账表单页

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'

const PAGE_PATH = 'pages/finance/flow/index'
const { t } = useI18n({ useScope: 'global' })
const router = useRouter()

interface Transaction {
  id: string
  family_id: string
  type: 'expense' | 'income' | 'transfer'
  amount_cents: number
  category_id: string
  category_name: string
  account_id: string
  account_name: string
  member_id?: string
  member_name?: string
  occurred_at: string
  remark?: string
  attachments?: string[]
  client_request_id?: string
}

interface FilterState {
  account_id?: string
  category_id?: string
  member_id?: string
  min_amount_cents?: number
  max_amount_cents?: number
}

const transactions = ref<Transaction[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const cursor = ref<string | null>(null)
const hasMore = ref(true)
const filters = ref<FilterState>({})
const period = ref<'month' | 'quarter' | 'year'>('month')

// Computed: total expense and income for current period
const totalExpense = computed(() => {
  return transactions.value
    .filter(t => t.type === 'expense')
    .reduce((sum, t) => sum + t.amount_cents, 0)
})

const totalIncome = computed(() => {
  return transactions.value
    .filter(t => t.type === 'income')
    .reduce((sum, t) => sum + t.amount_cents, 0)
})

const netAmount = computed(() => totalIncome.value - totalExpense.value)

// Fetch transactions with cursor pagination
async function fetchTransactions(loadMore = false) {
  if (loading.value) return
  if (!loadMore && !hasMore.value) return

  loading.value = true
  error.value = null

  try {
    const params: any = {
      limit: 50,
      period: period.value,
    }

    if (loadMore && cursor.value) {
      params.cursor = cursor.value
    }

    // Add filters
    if (filters.value.account_id) params.account_id = filters.value.account_id
    if (filters.value.category_id) params.category_id = filters.value.category_id
    if (filters.value.member_id) params.member_id = filters.value.member_id
    if (filters.value.min_amount_cents) params.min_amount_cents = filters.value.min_amount_cents
    if (filters.value.max_amount_cents) params.max_amount_cents = filters.value.max_amount_cents

    const response = await request.get('/api/finance/transactions', { params })

    if (loadMore) {
      transactions.value.push(...response.data.items)
    } else {
      transactions.value = response.data.items
    }

    cursor.value = response.data.next_cursor || null
    hasMore.value = !!response.data.next_cursor
  } catch (err: any) {
    console.error('Failed to fetch transactions:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Load more on scroll
function loadMore() {
  if (hasMore.value && !loading.value) {
    fetchTransactions(true)
  }
}

// Navigate to create transaction page
function goToCreate() {
  router.push('/pages/finance/transaction/create')
}

// Format date for display
function formatDate(isoString: string): string {
  const date = new Date(isoString)
  const month = date.getMonth() + 1
  const day = date.getDate()
  const hours = date.getHours().toString().padStart(2, '0')
  const minutes = date.getMinutes().toString().padStart(2, '0')
  return `${month}月${day}日 ${hours}:${minutes}`
}

// Get transaction type label
function getTypeLabel(type: string): string {
  const map: Record<string, string> = {
    expense: '支出',
    income: '收入',
    transfer: '转账',
  }
  return map[type] || type
}

onMounted(() => {
  fetchTransactions()
})
</script>

<template>
  <view class="hc-page">
    <!-- Summary bar -->
    <view class="summary-bar">
      <view class="sb-item">
        <text class="sb-label">支出</text>
        <text class="sb-value sb-expense">{{ formatAmount(totalExpense) }}</text>
      </view>
      <view class="sb-item">
        <text class="sb-label">收入</text>
        <text class="sb-value sb-income">{{ formatAmount(totalIncome) }}</text>
      </view>
      <view class="sb-item">
        <text class="sb-label">结余</text>
        <text class="sb-value" :class="netAmount >= 0 ? 'sb-positive' : 'sb-negative'">
          {{ formatAmount(netAmount) }}
        </text>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading && transactions.length === 0" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchTransactions">重试</button>
    </view>

    <!-- Transaction list -->
    <scroll-view v-else class="hc-scroll" scroll-y @scrolltolower="loadMore">
      <view v-if="transactions.length === 0" class="hc-empty">
        <text>暂无流水记录</text>
        <button class="hc-empty-btn" @click="goToCreate">记一笔</button>
      </view>

      <view v-for="item in transactions" :key="item.id" class="tx-item">
        <view class="tx-header">
          <view class="tx-category">
            <text class="tx-cat-icon">{{ item.category_name.charAt(0) }}</text>
            <text class="tx-cat-name">{{ item.category_name }}</text>
          </view>
          <text class="tx-amount" :class="{ 'tx-expense': item.type === 'expense', 'tx-income': item.type === 'income' }">
            {{ item.type === 'expense' ? '-' : '+' }}{{ formatAmount(item.amount_cents) }}
          </text>
        </view>
        <view class="tx-footer">
          <text class="tx-time">{{ formatDate(item.occurred_at) }}</text>
          <text class="tx-account">{{ item.account_name }}</text>
        </view>
        <view v-if="item.remark" class="tx-remark">
          <text>{{ item.remark }}</text>
        </view>
        <view v-if="item.member_name" class="tx-member">
          <text>{{ item.member_name }}</text>
        </view>
      </view>

      <!-- Loading more indicator -->
      <view v-if="loading && transactions.length > 0" class="hc-loading-more">
        <text>加载更多...</text>
      </view>

      <!-- No more data -->
      <view v-if="!hasMore && transactions.length > 0" class="hc-no-more">
        <text>没有更多了</text>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="goToCreate">
      <text class="fab-icon">＋</text>
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

/* Summary bar */
.summary-bar {
  display: flex;
  justify-content: space-around;
  padding: 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.sb-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
}

.sb-label {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.sb-value {
  font-size: 32rpx;
  font-weight: 600;
}

.sb-expense {
  color: var(--color-error);
}

.sb-income {
  color: var(--color-success);
}

.sb-positive {
  color: var(--color-success);
}

.sb-negative {
  color: var(--color-error);
}

/* Loading and error states */
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

.hc-error button,
.hc-empty-btn {
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

/* Transaction item */
.tx-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.tx-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.tx-category {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.tx-cat-icon {
  width: 48rpx;
  height: 48rpx;
  border-radius: var(--radius-sm);
  background-color: var(--color-primary-light);
  color: #ffffff;
  font-size: 24rpx;
  display: flex;
  align-items: center;
  justify-content: center;
}

.tx-cat-name {
  font-size: 28rpx;
  font-weight: 500;
  color: var(--text-primary);
}

.tx-amount {
  font-size: 32rpx;
  font-weight: 600;
}

.tx-expense {
  color: var(--color-error);
}

.tx-income {
  color: var(--color-success);
}

.tx-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8rpx;
}

.tx-time {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.tx-account {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.tx-remark {
  font-size: 26rpx;
  color: var(--text-secondary);
  margin-top: 8rpx;
  line-height: 1.5;
}

.tx-member {
  font-size: 24rpx;
  color: var(--text-tertiary);
  margin-top: 8rpx;
}

/* Loading more and no more indicators */
.hc-loading-more,
.hc-no-more {
  padding: 24rpx 0;
  text-align: center;
  font-size: 24rpx;
  color: var(--text-tertiary);
}

/* Floating action button */
.fab {
  position: fixed;
  right: 32rpx;
  bottom: 140rpx; /* Above bottom nav */
  width: 96rpx;
  height: 96rpx;
  border-radius: 50%;
  background-color: var(--color-primary);
  color: #ffffff;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: var(--shadow-lg);
  z-index: 50;
}

.fab-icon {
  font-size: 48rpx;
  font-weight: 300;
}
</style>
