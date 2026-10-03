<script setup lang="ts">
// pages/finance/bill/index —— 账单管理页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 账单列表：展示待支付和已支付的账单
//   · 创建账单：添加新的周期性或一次性账单
//   · 标记支付：将账单标记为已支付
//   · 删除账单：删除未支付的账单
//   · 对接 GET /api/finance/bills?family_id=&status=
//     POST /api/finance/bills
//     PUT /api/finance/bills/:id/pay
//     DELETE /api/finance/bills/:id

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount, formatDate } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface Bill {
  id: string
  family_id: string
  title: string
  amount_cents: number
  due_at: string
  category_id?: string
  account_id?: string
  status: 'pending' | 'paid' | 'overdue'
  description?: string
  recurrence?: 'none' | 'monthly' | 'quarterly' | 'yearly'
  created_at: string
  updated_at: string
}

const bills = ref<Bill[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const activeFilter = ref<'all' | 'pending' | 'paid'>('all')
const showAddDialog = ref(false)

// Form state
const formTitle = ref('')
const formAmount = ref('')
const formDueDate = ref(new Date().toISOString().slice(0, 10))
const formDescription = ref('')
const formRecurrence = ref<'none' | 'monthly' | 'quarterly' | 'yearly'>('none')

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

async function fetchBills() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      bills.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const params: Record<string, any> = { family_id: familyId }
    if (activeFilter.value !== 'all') {
      params.status = activeFilter.value
    }

    const body = await request.get<{ items?: Bill[] }>('/api/finance/bills', { params })
    bills.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch bills:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  formTitle.value = ''
  formAmount.value = ''
  formDueDate.value = new Date().toISOString().slice(0, 10)
  formDescription.value = ''
  formRecurrence.value = 'none'
  showAddDialog.value = true
}

async function handleSubmit() {
  if (!formTitle.value.trim()) {
    uni.showToast({ title: '请输入账单标题', icon: 'none' })
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
    await request.post('/api/finance/bills', {
      family_id: familyId,
      title: formTitle.value.trim(),
      amount_cents: Math.round(amount * 100),
      due_at: formDueDate.value,
      description: formDescription.value.trim(),
      recurrence: formRecurrence.value,
    })
    uni.showToast({ title: '创建成功', icon: 'success' })
    showAddDialog.value = false
    await fetchBills()
  } catch (err: any) {
    console.error('Failed to create bill:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

async function markAsPaid(bill: Bill) {
  uni.showModal({
    title: '确认支付',
    content: `确定要标记「${bill.title}」为已支付吗？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/bills/${bill.id}/pay`)
        uni.showToast({ title: '已标记为支付', icon: 'success' })
        await fetchBills()
      } catch (err: any) {
        console.error('Failed to mark bill as paid:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

function deleteBill(bill: Bill) {
  if (bill.status === 'paid') {
    uni.showToast({ title: '已支付的账单不能删除', icon: 'none' })
    return
  }

  uni.showModal({
    title: '删除账单',
    content: `确定要删除账单「${bill.title}」吗？`,
    confirmText: '删除',
    confirmColor: 'var(--color-error)',
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.delete(`/api/finance/bills/${bill.id}`)
        uni.showToast({ title: '删除成功', icon: 'success' })
        await fetchBills()
      } catch (err: any) {
        console.error('Failed to delete bill:', err)
        uni.showToast({ title: err.message || '删除失败', icon: 'none' })
      }
    },
  })
}

// Computed: filtered bills
const filteredBills = computed(() => {
  if (activeFilter.value === 'all') return bills.value
  return bills.value.filter((b) => b.status === activeFilter.value)
})

// Computed: summary stats
const pendingCount = computed(() => bills.value.filter((b) => b.status === 'pending').length)
const pendingAmount = computed(() =>
  bills.value
    .filter((b) => b.status === 'pending')
    .reduce((sum, b) => sum + b.amount_cents, 0)
)

onMounted(() => {
  fetchBills()
})
</script>

<template>
  <view class="hc-page">
    <!-- Summary bar -->
    <view class="summary-bar">
      <view class="summary-item">
        <text class="summary-label">待支付</text>
        <text class="summary-value">{{ pendingCount }}</text>
      </view>
      <view class="summary-item">
        <text class="summary-label">待付金额</text>
        <text class="summary-value summary-amount">{{ formatAmount(pendingAmount) }}</text>
      </view>
    </view>

    <!-- Filter tabs -->
    <view class="filter-bar">
      <view
        v-for="filter in [
          { key: 'all', label: '全部' },
          { key: 'pending', label: '待支付' },
          { key: 'paid', label: '已支付' },
        ]"
        :key="filter.key"
        class="filter-item"
        :class="{ active: activeFilter === filter.key }"
        @click="activeFilter = filter.key as any"
      >
        <text>{{ filter.label }}</text>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchBills">重试</button>
    </view>

    <!-- Bill list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="filteredBills.length === 0" class="hc-empty">
        <text>暂无账单</text>
        <button class="hc-empty-btn" @click="openAddDialog">添加账单</button>
      </view>

      <view v-for="bill in filteredBills" :key="bill.id" class="bill-item">
        <view class="bill-header">
          <view class="bill-info">
            <text class="bill-title">{{ bill.title }}</text>
            <text v-if="bill.recurrence && bill.recurrence !== 'none'" class="bill-recurrence">
              {{ bill.recurrence === 'monthly' ? '每月' : bill.recurrence === 'quarterly' ? '每季' : '每年' }}
            </text>
          </view>
          <text class="bill-amount">{{ formatAmount(bill.amount_cents) }}</text>
        </view>

        <view class="bill-footer">
          <text class="bill-due">到期: {{ formatDate(bill.due_at) }}</text>
          <text class="bill-status" :class="`status-${bill.status}`">
            {{ bill.status === 'pending' ? '待支付' : bill.status === 'paid' ? '已支付' : '已逾期' }}
          </text>
        </view>

        <view v-if="bill.description" class="bill-desc">
          <text>{{ bill.description }}</text>
        </view>

        <view class="bill-actions">
          <text
            v-if="bill.status === 'pending'"
            class="bill-action bill-pay"
            @click="markAsPaid(bill)"
          >
            标记支付
          </text>
          <text
            v-if="bill.status === 'pending'"
            class="bill-action bill-delete"
            @click="deleteBill(bill)"
          >
            删除
          </text>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">添加账单</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">账单标题</text>
            <input v-model="formTitle" class="form-input" placeholder="例如：房租" />
          </view>

          <view class="form-row">
            <text class="form-label">金额</text>
            <input v-model="formAmount" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">到期日期</text>
            <picker
              mode="date"
              :value="formDueDate"
              @change="(e: any) => formDueDate = e.detail.value"
            >
              <view class="form-picker">
                <text>{{ formDueDate }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">重复周期</text>
            <picker
              mode="selector"
              :range="['不重复', '每月', '每季', '每年']"
              :value="formRecurrence === 'none' ? 0 : formRecurrence === 'monthly' ? 1 : formRecurrence === 'quarterly' ? 2 : 3"
              @change="(e: any) => {
                const map = ['none', 'monthly', 'quarterly', 'yearly']
                formRecurrence = map[e.detail.value] as any
              }"
            >
              <view class="form-picker">
                <text>{{ formRecurrence === 'none' ? '不重复' : formRecurrence === 'monthly' ? '每月' : formRecurrence === 'quarterly' ? '每季' : '每年' }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">备注（可选）</text>
            <textarea
              v-model="formDescription"
              class="form-textarea"
              placeholder="添加备注..."
              maxlength="200"
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

.summary-amount {
  color: var(--color-warning);
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

.bill-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.bill-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.bill-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.bill-title {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.bill-recurrence {
  padding: 4rpx 12rpx;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.bill-amount {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.bill-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8rpx;
}

.bill-due {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.bill-status {
  font-size: 24rpx;
  padding: 4rpx 12rpx;
  border-radius: var(--radius-sm);
}

.status-pending {
  color: var(--color-warning);
  background-color: var(--color-warning-light);
}

.status-paid {
  color: var(--color-success);
  background-color: var(--color-success-light);
}

.status-overdue {
  color: var(--color-error);
  background-color: var(--color-error-light);
}

.bill-desc {
  margin-bottom: 12rpx;
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
}

.bill-actions {
  display: flex;
  gap: 24rpx;
  justify-content: flex-end;
}

.bill-action {
  font-size: 26rpx;
}

.bill-pay {
  color: var(--color-success);
}

.bill-delete {
  color: var(--color-error);
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

.form-textarea {
  width: 100%;
  min-height: 120rpx;
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
