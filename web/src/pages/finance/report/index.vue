<script setup lang="ts">
// pages/finance/report/index —— 统计报表页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 收支统计：按分类、账户展示支出和收入分布
//   · 趋势分析：按月/季/年展示收支趋势
//   · 排行榜：支出最多的分类 TOP 10
//   · 对接 GET /api/finance/statistics/overview?family_id=&period=（概览标量）
//     GET /api/finance/statistics/category?family_id=&period=（分类分布）
//     GET /api/finance/statistics/trend?family_id=&period=&granularity=（趋势）
//     这三段式以服务端 main.go 注册的路由为准；旧注释里的 `/statistics/summary`
//     **不存在**，请求它整页恒 404。

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

/**
 * `GET /statistics/overview` 的真实响应形状。
 * 两个坑：
 *  1. **单位是分**。服务端的 `GetOverviewStats` 直接聚合 `SUM(amount_cents)`
 *     （statistics.go:88-118），字段名虽然没带 `_cents` 后缀，值仍是分。
 *     所以这里**不能**再乘 100 —— 乘一次会让 ¥514.00 显示成 ¥51400.00。
 *  2. 支出是负数（服务端 SUM 出来的符号），页面按「支出额」展示，取绝对值。
 */
interface OverviewResponse {
  total_income?: number
  total_expense?: number
  net_balance?: number
  account_count?: number
}

/** `GET /statistics/category` 的行形状：`{category_id, category_name, amount, percentage, count}`。 */
interface RawCategoryStat {
  category_id: string
  category_name?: string
  amount?: number
  percentage?: number
  /** 笔数：服务端已在 SELECT 里 COUNT(*) 带回，页面「N 笔」读它。 */
  count?: number
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

    // 统计摘要：服务端注册的是 `/statistics/overview`（返回 total_income / total_expense /
    // net_balance / account_count），**没有** `/statistics/summary` —— 旧代码请求后者，
    // 于是「概览」页每次打开都是 HTTP 404 重试态。这里按真实路由取，并补上分类分布。
    const overviewBody = await request.get<OverviewResponse>('/api/finance/statistics/overview', {
      params: {
        family_id: familyId,
        period: homeStore.period,
      },
    })

    // 分类分布：服务端在 Overview 之外单独提供 `/statistics/category`，
    // Overview 里没有 by_category（实测响应只有四个标量字段）。
    // 这个接口此前坏过一次（SQL 缺表别名），所以 catch 兜一层：分类分布挂掉
    // 不该让整页变成错误态 —— 概览数字仍然要显示。
    const categoryBody = await request
      .get<{ items?: RawCategoryStat[] }>('/api/finance/statistics/category', {
        params: { family_id: familyId, period: homeStore.period },
      })
      .catch(() => ({ items: [] as RawCategoryStat[] }))

    const catItems = Array.isArray(categoryBody?.items) ? categoryBody.items : []

    // 字段名映射：net_balance → net_cents；金额已是分，不做换算。
    summary.value = {
      total_income_cents: overviewBody?.total_income ?? 0,
      total_expense_cents: Math.abs(overviewBody?.total_expense ?? 0),
      net_cents: overviewBody?.net_balance ?? 0,
      by_category: catItems.map((c) => ({
        category_id: c.category_id,
        category_name: categoryNames.value[c.category_id] || c.category_name || '',
        // `/statistics/category` 的金额字段是 `amount`，同样是 `SUM(amount_cents)` 的分，
        // 不换算；percentage 由服务端算好直接用。支出是负数，取绝对值。
        amount_cents: Math.abs(c.amount ?? 0),
        percentage: c.percentage ?? 0,
        count: c.count ?? 0,
      })),
      by_account: [],
    }

    // 获取趋势数据
    const trendBody = await request.get<{ items?: TrendPoint[] }>('/api/finance/statistics/trend', {
      params: {
        family_id: familyId,
        period: homeStore.period,
        granularity: 'month',
      },
    })
    // `/statistics/trend` 的行字段是 `income` / `expense`（单位：分，来自 SUM(amount_cents)），
    // **没有** `_cents` 后缀，也没有 `net`。旧代码直接把响应塞进 trendData，
    // 模板读 `point.income_cents` 全是 undefined，于是趋势页整屏显示 `¥NaN`。
    // 这里显式映射：收入取原值，支出取绝对值（服务端 SUM 出负数），结余自己算。
    const rawTrend: any[] = Array.isArray(trendBody?.items) ? trendBody.items : []
    trendData.value = rawTrend.map((p: any) => {
      const income = p.income ?? p.income_cents ?? 0
      const expense = Math.abs(p.expense ?? p.expense_cents ?? 0)
      return {
        period: p.period,
        income_cents: income,
        expense_cents: expense,
        net_cents: income - expense,
      } as TrendPoint
    })

    await loadNameDicts(familyId)
  } catch (err: any) {
    console.error('Failed to fetch statistics:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Computed: top expense categories
//
// 收支方向**不能靠金额正负推断**：`/statistics/category` 返回的 `amount` 已经是负数
// （服务端 SUM 出来就是负的），而取数处为了页面展示统一取了绝对值 —— 于是
// 「支出排行」按 `amount_cents < 0` 过滤永远为空，而同一批数据又都满足 `> 0`
// 全部落进「收入排行」，出现「支出榜空、收入榜列出餐饮 ¥514.00」这种自相矛盾的界面。
// 正确做法是让方向成为数据的一部分：这里用服务端给的 percentage 不可靠（支出也可能是 0%），
// 所以改为按「本周期是否有收入」区分—— 服务端 Overview 明确给了 total_income。
const hasIncome = computed(() => (summary.value?.total_income_cents ?? 0) > 0)

const topExpenseCategories = computed(() => {
  // 有收入数据时，分类分布是支出分布（服务端 /statistics/category 固定过滤 type='expense'）
  if (!summary.value?.by_category || hasIncome.value) return []
  return [...summary.value.by_category]
    .sort((a, b) => b.amount_cents - a.amount_cents) // 绝对值从大到小
    .slice(0, 10)
})

// Computed: top income categories
const topIncomeCategories = computed(() => {
  // 服务端目前只提供支出侧的分类分布（SQL 里写死 type = 'expense'），
  // 收入侧没有对应接口 —— 所以本周期没有收入数据时，收入榜必须为空，
  // 不能拿支出数据顶替。
  if (!summary.value?.by_category || !hasIncome.value) return []
  return []
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
