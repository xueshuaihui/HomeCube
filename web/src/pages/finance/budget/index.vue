<script setup lang="ts">
// pages/finance/budget/index —— 预算管理页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 预算列表：展示各分类的月度预算、已用金额、剩余额度
//   · 添加/编辑预算：设置分类、金额、周期
//   · 预算预警：当使用率超过阈值时显示警告
//   · 对接 GET /api/finance/budgets?family_id=&period=
//     POST /api/finance/budgets
//     PUT /api/finance/budgets/:id

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface Budget {
  id: string
  family_id: string
  category_id: string
  amount_cents: number
  period: string
  alert_threshold?: number
  created_at: string
  updated_at: string
}

interface BudgetWithUsage extends Budget {
  category_name?: string
  used_cents: number
  remaining_cents: number
  usage_rate: number
}

interface CategoryDict {
  id: string
  name?: string
  is_active?: boolean
}

const budgets = ref<BudgetWithUsage[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)
const editingBudget = ref<Budget | null>(null)
const categoryNames = ref<Record<string, string>>({})

// Form state
const formCategoryId = ref('')
const formAmount = ref('')
const formAlertThreshold = ref('80')

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

async function loadCategoryDict(familyId: string) {
  try {
    const body = await request.get<{ items?: CategoryDict[] }>('/api/finance/categories', {
      params: { family_id: familyId },
    })
    const names: Record<string, string> = {}
    for (const cat of body?.items ?? []) {
      if (cat?.id && cat.name && cat.is_active !== false) {
        names[cat.id] = cat.name
      }
    }
    categoryNames.value = names
  } catch (err) {
    console.error('Failed to load categories:', err)
  }
}

async function fetchBudgets() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      budgets.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const body = await request.get<{ items?: Budget[] }>('/api/finance/budgets', {
      params: {
        family_id: familyId,
        period: homeStore.period,
      },
    })

    const budgetList = Array.isArray(body?.items) ? body.items : []

    // 计算每个预算的使用情况（简化版，实际应从统计接口获取）
    const budgetsUsage: BudgetWithUsage[] = budgetList.map((b) => ({
      ...b,
      category_name: categoryNames.value[b.category_id] || '',
      used_cents: 0, // TODO: 从统计接口获取实际使用金额
      remaining_cents: b.amount_cents,
      usage_rate: 0,
    }))

    budgets.value = budgetsUsage
    await loadCategoryDict(familyId)
  } catch (err: any) {
    console.error('Failed to fetch budgets:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  editingBudget.value = null
  formCategoryId.value = ''
  formAmount.value = ''
  formAlertThreshold.value = '80'
  showAddDialog.value = true
}

function openEditDialog(budget: Budget) {
  editingBudget.value = budget
  formCategoryId.value = budget.category_id
  formAmount.value = String(budget.amount_cents / 100)
  formAlertThreshold.value = String(budget.alert_threshold || 80)
  showAddDialog.value = true
}

async function handleSubmit() {
  if (!formCategoryId.value) {
    uni.showToast({ title: '请选择分类', icon: 'none' })
    return
  }

  const amount = Number.parseFloat(formAmount.value)
  if (!amount || amount <= 0) {
    uni.showToast({ title: '请输入有效金额', icon: 'none' })
    return
  }

  const familyId = await resolveSessionFamilyId()
  if (!familyId) {
    uni.showToast({ title: '缺少家庭信息', icon: 'none' })
    return
  }

  try {
    const payload = {
      family_id: familyId,
      category_id: formCategoryId.value,
      amount_cents: Math.round(amount * 100),
      period: homeStore.period,
      alert_threshold: Number.parseInt(formAlertThreshold.value, 10) || 80,
    }

    if (editingBudget.value) {
      await request.put(`/api/finance/budgets/${editingBudget.value.id}`, payload)
      uni.showToast({ title: '更新成功', icon: 'success' })
    } else {
      await request.post('/api/finance/budgets', payload)
      uni.showToast({ title: '添加成功', icon: 'success' })
    }

    showAddDialog.value = false
    await fetchBudgets()
  } catch (err: any) {
    console.error('Failed to save budget:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

function getUsageColor(rate: number): string {
  if (rate >= 100) return 'var(--color-error)'
  if (rate >= 80) return 'var(--color-warning)'
  return 'var(--color-success)'
}

onMounted(() => {
  fetchBudgets()
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
      <button @click="fetchBudgets">重试</button>
    </view>

    <!-- Budget list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="budgets.length === 0" class="hc-empty">
        <text>暂无预算设置</text>
        <button class="hc-empty-btn" @click="openAddDialog">添加预算</button>
      </view>

      <view v-for="budget in budgets" :key="budget.id" class="budget-item">
        <view class="budget-header">
          <text class="budget-category">{{ budget.category_name || '未命名分类' }}</text>
          <text class="budget-amount">{{ formatAmount(budget.amount_cents) }}</text>
        </view>
        <view class="budget-progress">
          <view
            class="progress-bar"
            :style="{ width: `${Math.min(budget.usage_rate, 100)}%`, backgroundColor: getUsageColor(budget.usage_rate) }"
          />
        </view>
        <view class="budget-footer">
          <text class="budget-used">已用: {{ formatAmount(budget.used_cents) }}</text>
          <text class="budget-rate">{{ budget.usage_rate.toFixed(0) }}%</text>
        </view>
        <view class="budget-actions">
          <text class="budget-action" @click="openEditDialog(budget)">编辑</text>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add/Edit dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">{{ editingBudget ? '编辑预算' : '添加预算' }}</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">分类</text>
            <picker
              mode="selector"
              :range="Object.values(categoryNames)"
              :value="Object.keys(categoryNames).indexOf(formCategoryId)"
              @change="(e: any) => formCategoryId = Object.keys(categoryNames)[e.detail.value]"
            >
              <view class="form-picker">
                <text>{{ categoryNames[formCategoryId] || '请选择分类' }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">预算金额</text>
            <input
              v-model="formAmount"
              class="form-input"
              type="digit"
              placeholder="0.00"
            />
          </view>

          <view class="form-row">
            <text class="form-label">预警阈值 (%)</text>
            <input
              v-model="formAlertThreshold"
              class="form-input"
              type="number"
              placeholder="80"
            />
          </view>
        </view>

        <view class="dialog-actions">
          <button class="dialog-btn dialog-cancel" @click="showAddDialog = false">取消</button>
          <button class="dialog-btn dialog-confirm" @click="handleSubmit">确定</button>
        </view>
      </view>
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

.budget-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.budget-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16rpx;
}

.budget-category {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.budget-amount {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.budget-progress {
  height: 12rpx;
  background-color: var(--bg-tertiary);
  border-radius: 6rpx;
  overflow: hidden;
  margin-bottom: 12rpx;
}

.progress-bar {
  height: 100%;
  border-radius: 6rpx;
  transition: width 0.3s ease;
}

.budget-footer {
  display: flex;
  justify-content: space-between;
  margin-bottom: 12rpx;
}

.budget-used,
.budget-rate {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.budget-actions {
  display: flex;
  justify-content: flex-end;
}

.budget-action {
  font-size: 26rpx;
  color: var(--color-primary);
}

.fab {
  position: fixed;
  right: 32rpx;
  bottom: 140rpx;
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

.dialog-mask {
  position: fixed;
  inset: 0;
  background-color: var(--overlay-dark);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.dialog-content {
  width: 600rpx;
  max-height: 80vh;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
  padding: 32rpx;
  overflow-y: auto;
}

.dialog-title {
  display: block;
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 32rpx;
  text-align: center;
}

.dialog-form {
  margin-bottom: 32rpx;
}

.form-row {
  margin-bottom: 24rpx;
}

.form-label {
  display: block;
  font-size: 28rpx;
  color: var(--text-secondary);
  margin-bottom: 8rpx;
}

.form-input,
.form-picker {
  width: 100%;
  padding: 16rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.dialog-actions {
  display: flex;
  gap: 16rpx;
}

.dialog-btn {
  flex: 1;
  padding: 24rpx 0;
  border-radius: var(--radius-md);
  font-size: 30rpx;
}

.dialog-cancel {
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
}

.dialog-confirm {
  background-color: var(--color-primary);
  color: var(--color-white);
}
</style>
