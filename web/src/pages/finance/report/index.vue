<script setup lang="ts">
// pages/finance/report/index —— 统计报表页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 收支统计：按分类、账户展示支出和收入分布
//   · 趋势分析：按月/季/年展示收支趋势
//   · 排行榜：支出最多的分类 TOP 10
//   · 对接 GET /api/finance/statistics/summary?family_id=&period=
//     GET /api/finance/statistics/trend?family_id=&period=&granularity=

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface CategoryStat {
  category_id: string
  category_name?: string
  amount_cents: number
  count: number
}

interface AccountStat {
  account_id: string
  account_name?: string
  amount_cents: number
  count: number
}

interface TrendPoint {
  period: string
  income_cents: number
  expense_cents: number
  net_cents: number
}

interface StatisticsSummary {
  total_income_cents: number
  total_expense_cents: number
  net_cents: number
  by_category?: CategoryStat[]
  by_account?: AccountStat[]
}

const loading = ref(false)
const error = ref<string | null>(null)
const activeTab = ref<'overview' | 'trend' | 'rank'>('overview')
const summary = ref<StatisticsSummary | null>(null)
const trendData = ref<TrendPoint[]>([])
const categoryNames = ref<Record<string, string>>({})
const accountNames = ref<Record<string, string>>({})

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

async function loadNameDicts(familyId: string) {
  try {
    const catBody = await request.get<{ items?: Array<{ id: string; name?: string; is_active?: boolean }> }>(
      '/api/finance/categories',
      { params: { family_id: familyId } }
    )
    for (const cat of catBody?.items ?? []) {
      if (cat?.id && cat.name && cat.is_active !== false) {
        categoryNames.value[cat.id] = cat.name
      }
    }

    const accBody = await request.get<{ items?: Array<{ id: string; name?: string; is_archived?: boolean }> }>(
      '/api/finance/accounts',
      { params: { family_id: familyId } }
    )
    for (const acc of accBody?.items ?? []) {
      if (acc?.id && acc.name && acc.is_archived !== true) {
        accountNames.value[acc.id] = acc.name
      }
    }
  } catch (err) {
    console.error('Failed to load name dicts:', err)
  }
}

async function fetchStatistics() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    // 获取统计摘要
    const summaryBody = await request.get<StatisticsSummary>('/api/finance/statistics/summary', {
      params: {
        family_id: familyId,
        period: homeStore.period,
      },
    })
    summary.value = summaryBody

    // 填充分类和账户名称
    if (summaryBody?.by_category) {
      for (const stat of summaryBody.by_category) {
        if (stat.category_id && !stat.category_name) {
          stat.category_name = categoryNames.value[stat.category_id] || ''
        }
      }
    }
    if (summaryBody?.by_account) {
      for (const stat of summaryBody.by_account) {
        if (stat.account_id && !stat.account_name) {
          stat.account_name = accountNames.value[stat.account_id] || ''
        }
      }
    }

    // 获取趋势数据
    const trendBody = await request.get<{ items?: TrendPoint[] }>('/api/finance/statistics/trend', {
      params: {
        family_id: familyId,
        period: homeStore.period,
        granularity: 'month',
      },
    })
    trendData.value = Array.isArray(trendBody?.items) ? trendBody.items : []

    await loadNameDicts(familyId)
  } catch (err: any) {
    console.error('Failed to fetch statistics:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Computed: top expense categories
const topExpenseCategories = computed(() => {
  if (!summary.value?.by_category) return []
  return [...summary.value.by_category]
    .filter((c) => c.amount_cents < 0) // 支出为负数
    .sort((a, b) => a.amount_cents - b.amount_cents) // 从小到大（绝对值从大到小）
    .slice(0, 10)
})

// Computed: top income categories
const topIncomeCategories = computed(() => {
  if (!summary.value?.by_category) return []
  return [...summary.value.by_category]
    .filter((c) => c.amount_cents > 0)
    .sort((a, b) => b.amount_cents - a.amount_cents)
    .slice(0, 10)
})

onMounted(() => {
  fetchStatistics()
})
</script>

<template>
  <view class="hc-page">
    <!-- Tab selector -->
    <view class="tab-bar">
      <view
        v-for="tab in [
          { key: 'overview', label: '概览' },
          { key: 'trend', label: '趋势' },
          { key: 'rank', label: '排行' },
        ]"
        :key="tab.key"
        class="tab-item"
        :class="{ active: activeTab === tab.key }"
        @click="activeTab = tab.key as any"
      >
        <text>{{ tab.label }}</text>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchStatistics">重试</button>
    </view>

    <!-- Content -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- Overview tab -->
      <view v-if="activeTab === 'overview'" class="tab-content">
        <!-- Summary cards -->
        <view class="summary-cards">
          <view class="summary-card">
            <text class="card-label">总收入</text>
            <text class="card-value card-income">{{ formatAmount(summary?.total_income_cents || 0) }}</text>
          </view>
          <view class="summary-card">
            <text class="card-label">总支出</text>
            <text class="card-value card-expense">{{ formatAmount(Math.abs(summary?.total_expense_cents || 0)) }}</text>
          </view>
          <view class="summary-card">
            <text class="card-label">结余</text>
            <text
              class="card-value"
              :class="(summary?.net_cents || 0) >= 0 ? 'card-positive' : 'card-negative'"
            >
              {{ formatAmount(summary?.net_cents || 0) }}
            </text>
          </view>
        </view>

        <!-- By category -->
        <view v-if="summary?.by_category && summary.by_category.length > 0" class="section">
          <text class="section-title">按分类统计</text>
          <view v-for="stat in summary.by_category" :key="stat.category_id" class="stat-row">
            <view class="stat-info">
              <text class="stat-name">{{ stat.category_name || '未命名' }}</text>
              <text class="stat-count">{{ stat.count }}笔</text>
            </view>
            <text
              class="stat-amount"
              :class="stat.amount_cents >= 0 ? 'stat-income' : 'stat-expense'"
            >
              {{ formatAmount(Math.abs(stat.amount_cents)) }}
            </text>
          </view>
        </view>

        <!-- By account -->
        <view v-if="summary?.by_account && summary.by_account.length > 0" class="section">
          <text class="section-title">按账户统计</text>
          <view v-for="stat in summary.by_account" :key="stat.account_id" class="stat-row">
            <view class="stat-info">
              <text class="stat-name">{{ stat.account_name || '未命名' }}</text>
              <text class="stat-count">{{ stat.count }}笔</text>
            </view>
            <text
              class="stat-amount"
              :class="stat.amount_cents >= 0 ? 'stat-income' : 'stat-expense'"
            >
              {{ formatAmount(Math.abs(stat.amount_cents)) }}
            </text>
          </view>
        </view>
      </view>

      <!-- Trend tab -->
      <view v-if="activeTab === 'trend'" class="tab-content">
        <view v-if="trendData.length === 0" class="hc-empty">
          <text>暂无趋势数据</text>
        </view>
        <view v-else class="trend-list">
          <view v-for="point in trendData" :key="point.period" class="trend-item">
            <text class="trend-period">{{ point.period }}</text>
            <view class="trend-values">
              <text class="trend-income">收入: {{ formatAmount(point.income_cents) }}</text>
              <text class="trend-expense">支出: {{ formatAmount(Math.abs(point.expense_cents)) }}</text>
              <text
                class="trend-net"
                :class="point.net_cents >= 0 ? 'trend-positive' : 'trend-negative'"
              >
                结余: {{ formatAmount(point.net_cents) }}
              </text>
            </view>
          </view>
        </view>
      </view>

      <!-- Rank tab -->
      <view v-if="activeTab === 'rank'" class="tab-content">
        <!-- Expense rank -->
        <view class="section">
          <text class="section-title">支出排行 TOP 10</text>
          <view v-if="topExpenseCategories.length === 0" class="hc-empty-small">
            <text>暂无支出数据</text>
          </view>
          <view v-for="(stat, index) in topExpenseCategories" :key="stat.category_id" class="rank-row">
            <text class="rank-index">{{ index + 1 }}</text>
            <text class="rank-name">{{ stat.category_name || '未命名' }}</text>
            <text class="rank-amount rank-expense">{{ formatAmount(Math.abs(stat.amount_cents)) }}</text>
          </view>
        </view>

        <!-- Income rank -->
        <view class="section">
          <text class="section-title">收入排行 TOP 10</text>
          <view v-if="topIncomeCategories.length === 0" class="hc-empty-small">
            <text>暂无收入数据</text>
          </view>
          <view v-for="(stat, index) in topIncomeCategories" :key="stat.category_id" class="rank-row">
            <text class="rank-index">{{ index + 1 }}</text>
            <text class="rank-name">{{ stat.category_name || '未命名' }}</text>
            <text class="rank-amount rank-income">{{ formatAmount(stat.amount_cents) }}</text>
          </view>
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

.tab-bar {
  display: flex;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.tab-item {
  flex: 1;
  padding: 24rpx 0;
  text-align: center;
  font-size: 28rpx;
  color: var(--text-secondary);
}

.tab-item.active {
  color: var(--color-primary);
  font-weight: 600;
  border-bottom: 4rpx solid var(--color-primary);
}

.hc-loading,
.hc-error {
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

.tab-content {
  padding-bottom: 24rpx;
}

/* Summary cards */
.summary-cards {
  display: flex;
  gap: 16rpx;
  margin-bottom: 32rpx;
}

.summary-card {
  flex: 1;
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.card-label {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.card-value {
  font-size: 32rpx;
  font-weight: 600;
}

.card-income {
  color: var(--color-success);
}

.card-expense {
  color: var(--color-error);
}

.card-positive {
  color: var(--color-success);
}

.card-negative {
  color: var(--color-error);
}

/* Section */
.section {
  margin-bottom: 32rpx;
}

.section-title {
  display: block;
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 16rpx;
}

/* Stat row */
.stat-row {
  padding: 20rpx 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-sm);
  margin-bottom: 8rpx;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.stat-info {
  display: flex;
  flex-direction: column;
  gap: 4rpx;
}

.stat-name {
  font-size: 28rpx;
  color: var(--text-primary);
}

.stat-count {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.stat-amount {
  font-size: 30rpx;
  font-weight: 600;
}

.stat-income {
  color: var(--color-success);
}

.stat-expense {
  color: var(--color-error);
}

/* Trend list */
.trend-list {
  display: flex;
  flex-direction: column;
  gap: 16rpx;
}

.trend-item {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.trend-period {
  display: block;
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 12rpx;
}

.trend-values {
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.trend-income,
.trend-expense,
.trend-net {
  font-size: 26rpx;
}

.trend-income {
  color: var(--color-success);
}

.trend-expense {
  color: var(--color-error);
}

.trend-positive {
  color: var(--color-success);
}

.trend-negative {
  color: var(--color-error);
}

/* Rank row */
.rank-row {
  padding: 20rpx 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-sm);
  margin-bottom: 8rpx;
  display: flex;
  align-items: center;
  gap: 16rpx;
}

.rank-index {
  width: 48rpx;
  height: 48rpx;
  border-radius: 50%;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  font-size: 24rpx;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
}

.rank-name {
  flex: 1;
  font-size: 28rpx;
  color: var(--text-primary);
}

.rank-amount {
  font-size: 28rpx;
  font-weight: 600;
}

.rank-income {
  color: var(--color-success);
}

.rank-expense {
  color: var(--color-error);
}

.hc-empty,
.hc-empty-small {
  padding: 48rpx;
  text-align: center;
  font-size: 28rpx;
  color: var(--text-tertiary);
}

.hc-empty-small {
  padding: 24rpx;
  font-size: 26rpx;
}
</style>
