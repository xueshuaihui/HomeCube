<script setup lang="ts">
// pages/finance/flow/index —— 财务面「流水」列表页（§5.1 Tab 流水、§5.2 首行）。
//
// 完整实现：
//   · 对接 GET /api/finance/transactions?family_id=&period=&cursor=
//     （family_id 是服务端 ListTransactions 的必填 query，缺它即 400，见 handler/finance.go；
//      该 handler 不读 `limit`，每页固定 50 条，所以本页不发送 `limit`）
//   · 游标分页：响应体 `{items, next_cursor}`，请求层裸回、不再 `.data` 剥壳
//   · 行内的分类名/账户名：服务端 FinanceTransaction **只有** category_id / account_id
//     （无 join、无任何 *_name 字段），因此本页另取本家庭的两份字典在前端解析
//   · 面内维护入口：分类管理 / 账户管理（§5.1 末位 Tab「账户与设置」`finance/settings/index`
//     本期未建，先由本栏承载这两个已注册路由，不让它们成为孤儿）
//   · 记账入口：navigateTo 压栈到记账表单页（§2.3）

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'
// period 与家庭快照都是 shell 级状态（§1.3 第 16/19 条），分包只读、不自存、不自拉。
import { useHomeStore } from '@/stores/home'

/** 服务端 model.FinanceTransaction 的 JSON 形状。金额单位：整数分；支出存**负数**。 */
interface Transaction {
  id: string
  family_id: string
  type: 'income' | 'expense' | 'transfer'
  amount_cents: number
  category_id?: string
  account_id: string
  occurred_at: string
  description?: string
  receipt_file_id?: string
  transfer_group_id?: string
  client_request_id?: string
  tag_ids?: string[]
  version: number
  created_at: string
  updated_at: string
}

/** 契约 `/transactions` 的 200 体：`{items:[Transaction], next_cursor?}`。 */
interface TransactionListBody {
  items?: Transaction[]
  next_cursor?: string | null
}

/** 分类/账户字典行（只取解析名字要用的字段，两者都带 id/name）。 */
interface DictRow {
  id: string
  name?: string
  is_active?: boolean
  is_archived?: boolean
}

const transactions = ref<Transaction[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const cursor = ref<string | null>(null)
const hasMore = ref(true)
const categoryNames = ref<Record<string, string>>({})
const accountNames = ref<Record<string, string>>({})

const homeStore = useHomeStore()

/**
 * 取当前家庭 id：读 shell store 的那一份，分包不持有跨路由的会话状态、
 * 也不在此重新拉 `/families`（`ensureSession` 内部已经拉过并缓存）。
 */
async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

// Computed: total expense and income for current period
// 服务端的 expense 行 amount_cents 本身就是负数（CreateTransaction 会取负），
// 求和取绝对值，才不会出现「支出 = ¥-1234.00」。
const totalExpense = computed(() => {
  return transactions.value
    .filter(t => t.type === 'expense')
    .reduce((sum, t) => sum + Math.abs(t.amount_cents), 0)
})

const totalIncome = computed(() => {
  return transactions.value
    .filter(t => t.type === 'income')
    .reduce((sum, t) => sum + Math.abs(t.amount_cents), 0)
})

const netAmount = computed(() => totalIncome.value - totalExpense.value)

/** 行的金额文案：符号由 type 决定，数字取绝对值（避免出现「-¥-299.50」）。 */
function amountText(item: Transaction): string {
  const text = formatAmount(Math.abs(item.amount_cents))
  if (item.type === 'expense') return `-${text}`
  if (item.type === 'income') return `+${text}`
  return text
}

function categoryName(id?: string): string {
  if (!id) return ''
  return categoryNames.value[id] || ''
}

function accountName(id: string): string {
  return accountNames.value[id] || ''
}

/**
 * 取本家庭的分类与账户字典，用来把行内的 id 解析成名字。
 * 停用分类（is_active=false）与已归档账户不进字典：历史流水仍显示行，只是不标名字。
 */
async function loadNameDicts(familyId: string) {
  const catBody = await request.get<{ items?: DictRow[] }>('/api/finance/categories', {
    params: { family_id: familyId },
  })
  const nextCats: Record<string, string> = {}
  for (const row of catBody?.items ?? []) {
    if (row?.id && row.name && row.is_active !== false) nextCats[row.id] = row.name
  }
  categoryNames.value = nextCats

  const accBody = await request.get<{ items?: DictRow[] }>('/api/finance/accounts', {
    params: { family_id: familyId },
  })
  const nextAccounts: Record<string, string> = {}
  for (const row of accBody?.items ?? []) {
    if (row?.id && row.name && row.is_archived !== true) nextAccounts[row.id] = row.name
  }
  accountNames.value = nextAccounts
}

// Fetch transactions with cursor pagination
async function fetchTransactions(loadMore = false) {
  if (loading.value) return
  if (!loadMore && !hasMore.value) return

  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      transactions.value = []
      cursor.value = null
      hasMore.value = false
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    // 每页尺寸由服务端 handler 写死 50，本页不发送 `limit`（发了也被忽略）。
    // `period` 只读 shell store 的时间窗编码，分包不自造（同源④）。
    const params: Record<string, any> = {
      family_id: familyId,
      // `period` 只读 shell 级那一份时间窗编码（§1.3 第 19 条），本页不造第二个 period。
      // 服务端按 `TO_CHAR(occurred_at,'YYYY-MM') = ?` 精确匹配：`YYYY-MM` 档有效，
      // `YYYY-Qn` / `YYYY` 两档服务端没有季/年粒度实现，会返回空列表（已作为服务端缺陷上报）。
      period: homeStore.period,
    }

    if (loadMore && cursor.value) {
      params.cursor = cursor.value
    }

    // 请求层 resolve 的就是裸响应体：直接读 items / next_cursor。
    const body = await request.get<TransactionListBody>('/api/finance/transactions', { params })
    const page = Array.isArray(body?.items) ? body.items : []

    transactions.value = loadMore ? [...transactions.value, ...page] : page
    cursor.value = body?.next_cursor ?? null
    hasMore.value = !!body?.next_cursor

    await loadNameDicts(familyId)
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

// Navigate to create transaction page（一级页 → 面内二级页 = 压栈，§2.3）
function goToCreate() {
  uni.navigateTo({ url: '/pages/finance/transaction/create' })
}

/** 面内维护：分类管理 / 账户管理（同分包内压栈，§2.3、17.7 第 4 条）。 */
function goToCategories() {
  uni.navigateTo({ url: '/pages/finance/category/index' })
}

function goToAccounts() {
  uni.navigateTo({ url: '/pages/finance/account/index' })
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

    <!-- 面内维护入口（§5.1 的「账户与设置」未建，先挂在本面首屏的工具栏上） -->
    <view class="maintain-bar">
      <text class="maintain-item" @click="goToCategories">分类管理</text>
      <text class="maintain-item" @click="goToAccounts">账户管理</text>
    </view>

    <!-- Loading state -->
    <view v-if="loading && transactions.length === 0" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchTransactions()">重试</button>
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
            <text v-if="categoryName(item.category_id)" class="tx-cat-icon">
              {{ categoryName(item.category_id).charAt(0) }}
            </text>
            <text v-if="categoryName(item.category_id)" class="tx-cat-name">
              {{ categoryName(item.category_id) }}
            </text>
          </view>
          <text class="tx-amount" :class="{ 'tx-expense': item.type === 'expense', 'tx-income': item.type === 'income' }">
            {{ amountText(item) }}
          </text>
        </view>
        <view class="tx-footer">
          <text class="tx-time">{{ formatDate(item.occurred_at) }}</text>
          <text v-if="accountName(item.account_id)" class="tx-account">{{ accountName(item.account_id) }}</text>
        </view>
        <view v-if="item.description" class="tx-remark">
          <text>{{ item.description }}</text>
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

/* 面内维护入口栏 */
.maintain-bar {
  display: flex;
  gap: 32rpx;
  padding: 20rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.maintain-item {
  font-size: 26rpx;
  color: var(--color-primary);
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
  color: var(--color-white);
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
  color: var(--color-white);
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
  color: var(--color-white);
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
