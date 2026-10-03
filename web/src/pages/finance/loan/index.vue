<script setup lang="ts">
// pages/finance/loan/index —— 借还款管理页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 借款列表：展示借出和借入的款项
//   · 创建借款：记录新的借款
//   · 还款记录：添加还款记录
//   · 结清借款：标记借款为已还清
//   · 对接 GET /api/finance/loans?family_id=
//     POST /api/finance/loans
//     POST /api/finance/loans/:id/repayments
//     PUT /api/finance/loans/:id/settle

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount, formatDate } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface Loan {
  id: string
  family_id: string
  type: 'lend' | 'borrow'
  counterparty_name: string
  amount_cents: number
  remaining_cents: number
  interest_rate?: number
  start_date: string
  due_date?: string
  description?: string
  status: 'active' | 'settled'
  created_at: string
  updated_at: string
}

interface Repayment {
  id: string
  loan_id: string
  amount_cents: number
  date: string
  note?: string
  created_at: string
}

const loans = ref<Loan[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const activeFilter = ref<'all' | 'lend' | 'borrow'>('all')
const showAddDialog = ref(false)
const selectedLoan = ref<Loan | null>(null)
const showRepaymentDialog = ref(false)

// Form state for new loan
const formType = ref<'lend' | 'borrow'>('lend')
const formCounterparty = ref('')
const formAmount = ref('')
const formStartDate = ref(new Date().toISOString().slice(0, 10))
const formDueDate = ref('')
const formInterestRate = ref('')
const formDescription = ref('')

// Form state for repayment
const formRepaymentAmount = ref('')
const formRepaymentDate = ref(new Date().toISOString().slice(0, 10))
const formRepaymentNote = ref('')

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

async function fetchLoans() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      loans.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const params: Record<string, any> = { family_id: familyId }
    if (activeFilter.value !== 'all') {
      params.type = activeFilter.value
    }

    const body = await request.get<{ items?: Loan[] }>('/api/finance/loans', { params })
    loans.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch loans:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  selectedLoan.value = null
  formType.value = 'lend'
  formCounterparty.value = ''
  formAmount.value = ''
  formStartDate.value = new Date().toISOString().slice(0, 10)
  formDueDate.value = ''
  formInterestRate.value = ''
  formDescription.value = ''
  showAddDialog.value = true
}

async function handleSubmit() {
  if (!formCounterparty.value.trim()) {
    uni.showToast({ title: '请输入对方姓名', icon: 'none' })
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
    const payload: Record<string, any> = {
      family_id: familyId,
      type: formType.value,
      counterparty_name: formCounterparty.value.trim(),
      amount_cents: Math.round(amount * 100),
      start_date: formStartDate.value,
    }

    if (formDueDate.value) payload.due_date = formDueDate.value
    if (formInterestRate.value) payload.interest_rate = Number.parseFloat(formInterestRate.value)
    if (formDescription.value) payload.description = formDescription.value.trim()

    await request.post('/api/finance/loans', payload)
    uni.showToast({ title: '创建成功', icon: 'success' })
    showAddDialog.value = false
    await fetchLoans()
  } catch (err: any) {
    console.error('Failed to create loan:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

function openRepaymentDialog(loan: Loan) {
  selectedLoan.value = loan
  formRepaymentAmount.value = ''
  formRepaymentDate.value = new Date().toISOString().slice(0, 10)
  formRepaymentNote.value = ''
  showRepaymentDialog.value = true
}

async function handleRepayment() {
  if (!selectedLoan.value) return

  const amount = Number.parseFloat(formRepaymentAmount.value)
  if (!amount || amount <= 0) {
    uni.showToast({ title: '请输入有效金额', icon: 'none' })
    return
  }

  try {
    await request.post(`/api/finance/loans/${selectedLoan.value.id}/repayments`, {
      amount_cents: Math.round(amount * 100),
      date: formRepaymentDate.value,
      note: formRepaymentNote.value.trim(),
    })
    uni.showToast({ title: '还款记录已添加', icon: 'success' })
    showRepaymentDialog.value = false
    await fetchLoans()
  } catch (err: any) {
    console.error('Failed to add repayment:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

async function settleLoan(loan: Loan) {
  uni.showModal({
    title: '结清借款',
    content: `确定要结清与「${loan.counterparty_name}」的借款吗？剩余金额将清零。`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/loans/${loan.id}/settle`)
        uni.showToast({ title: '已结清', icon: 'success' })
        await fetchLoans()
      } catch (err: any) {
        console.error('Failed to settle loan:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

// Computed: filtered loans
const filteredLoans = computed(() => {
  if (activeFilter.value === 'all') return loans.value
  return loans.value.filter((l) => l.type === activeFilter.value)
})

// Computed: summary stats
const totalLent = computed(() =>
  loans.value
    .filter((l) => l.type === 'lend' && l.status === 'active')
    .reduce((sum, l) => sum + l.remaining_cents, 0)
)

const totalBorrowed = computed(() =>
  loans.value
    .filter((l) => l.type === 'borrow' && l.status === 'active')
    .reduce((sum, l) => sum + l.remaining_cents, 0)
)

onMounted(() => {
  fetchLoans()
})
</script>

<template>
  <view class="hc-page">
    <!-- Summary bar -->
    <view class="summary-bar">
      <view class="summary-item">
        <text class="summary-label">借出未还</text>
        <text class="summary-value summary-lent">{{ formatAmount(totalLent) }}</text>
      </view>
      <view class="summary-item">
        <text class="summary-label">借入未还</text>
        <text class="summary-value summary-borrowed">{{ formatAmount(totalBorrowed) }}</text>
      </view>
    </view>

    <!-- Filter tabs -->
    <view class="filter-bar">
      <view
        v-for="filter in [
          { key: 'all', label: '全部' },
          { key: 'lend', label: '借出' },
          { key: 'borrow', label: '借入' },
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
      <button @click="fetchLoans">重试</button>
    </view>

    <!-- Loan list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="filteredLoans.length === 0" class="hc-empty">
        <text>暂无借款记录</text>
        <button class="hc-empty-btn" @click="openAddDialog">添加借款</button>
      </view>

      <view v-for="loan in filteredLoans" :key="loan.id" class="loan-item">
        <view class="loan-header">
          <view class="loan-info">
            <text class="loan-type-badge" :class="loan.type === 'lend' ? 'badge-lend' : 'badge-borrow'">
              {{ loan.type === 'lend' ? '借出' : '借入' }}
            </text>
            <text class="loan-counterparty">{{ loan.counterparty_name }}</text>
          </view>
          <text class="loan-amount">{{ formatAmount(loan.amount_cents) }}</text>
        </view>

        <view class="loan-progress">
          <view class="progress-info">
            <text class="progress-label">剩余: {{ formatAmount(loan.remaining_cents) }}</text>
            <text class="progress-percent">
              {{ ((1 - loan.remaining_cents / loan.amount_cents) * 100).toFixed(0) }}%
            </text>
          </view>
          <view class="progress-bar-bg">
            <view
              class="progress-bar-fill"
              :style="{ width: `${(1 - loan.remaining_cents / loan.amount_cents) * 100}%` }"
            />
          </view>
        </view>

        <view class="loan-footer">
          <text class="loan-date">开始: {{ formatDate(loan.start_date) }}</text>
          <text v-if="loan.due_date" class="loan-due">到期: {{ formatDate(loan.due_date) }}</text>
        </view>

        <view v-if="loan.description" class="loan-desc">
          <text>{{ loan.description }}</text>
        </view>

        <view class="loan-actions">
          <text
            v-if="loan.status === 'active'"
            class="loan-action loan-repay"
            @click="openRepaymentDialog(loan)"
          >
            还款
          </text>
          <text
            v-if="loan.status === 'active'"
            class="loan-action loan-settle"
            @click="settleLoan(loan)"
          >
            结清
          </text>
          <text v-if="loan.status === 'settled'" class="loan-status-text">已结清</text>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add loan dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">添加借款</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">类型</text>
            <picker
              mode="selector"
              :range="['借出', '借入']"
              :value="formType === 'lend' ? 0 : 1"
              @change="(e: any) => formType = e.detail.value === 0 ? 'lend' : 'borrow'"
            >
              <view class="form-picker">
                <text>{{ formType === 'lend' ? '借出' : '借入' }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">对方姓名</text>
            <input v-model="formCounterparty" class="form-input" placeholder="例如：张三" />
          </view>

          <view class="form-row">
            <text class="form-label">金额</text>
            <input v-model="formAmount" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">开始日期</text>
            <picker
              mode="date"
              :value="formStartDate"
              @change="(e: any) => formStartDate = e.detail.value"
            >
              <view class="form-picker">
                <text>{{ formStartDate }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">到期日期（可选）</text>
            <picker
              mode="date"
              :value="formDueDate"
              @change="(e: any) => formDueDate = e.detail.value"
            >
              <view class="form-picker">
                <text>{{ formDueDate || '请选择' }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">年利率 %（可选）</text>
            <input v-model="formInterestRate" class="form-input" type="digit" placeholder="0" />
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

    <!-- Repayment dialog -->
    <view v-if="showRepaymentDialog && selectedLoan" class="dialog-mask" @click="showRepaymentDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">添加还款</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">还款金额</text>
            <input v-model="formRepaymentAmount" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">还款日期</text>
            <picker
              mode="date"
              :value="formRepaymentDate"
              @change="(e: any) => formRepaymentDate = e.detail.value"
            >
              <view class="form-picker">
                <text>{{ formRepaymentDate }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">备注（可选）</text>
            <textarea
              v-model="formRepaymentNote"
              class="form-textarea"
              placeholder="添加备注..."
              maxlength="200"
            />
          </view>
        </view>

        <view class="dialog-actions">
          <button class="dialog-btn dialog-cancel" @click="showRepaymentDialog = false">取消</button>
          <button class="dialog-btn dialog-confirm" @click="handleRepayment">确定</button>
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
}

.summary-lent {
  color: var(--color-success);
}

.summary-borrowed {
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

.loan-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.loan-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16rpx;
}

.loan-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.loan-type-badge {
  padding: 4rpx 12rpx;
  font-size: 20rpx;
  border-radius: var(--radius-sm);
  color: var(--color-white);
}

.badge-lend {
  background-color: var(--color-success);
}

.badge-borrow {
  background-color: var(--color-error);
}

.loan-counterparty {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.loan-amount {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.loan-progress {
  margin-bottom: 12rpx;
}

.progress-info {
  display: flex;
  justify-content: space-between;
  margin-bottom: 8rpx;
}

.progress-label,
.progress-percent {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.progress-bar-bg {
  height: 8rpx;
  background-color: var(--bg-tertiary);
  border-radius: 4rpx;
  overflow: hidden;
}

.progress-bar-fill {
  height: 100%;
  background-color: var(--color-primary);
  border-radius: 4rpx;
  transition: width 0.3s ease;
}

.loan-footer {
  display: flex;
  justify-content: space-between;
  margin-bottom: 8rpx;
}

.loan-date,
.loan-due {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.loan-desc {
  margin-bottom: 12rpx;
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
}

.loan-actions {
  display: flex;
  gap: 24rpx;
  justify-content: flex-end;
}

.loan-action {
  font-size: 26rpx;
}

.loan-repay {
  color: var(--color-success);
}

.loan-settle {
  color: var(--color-primary);
}

.loan-status-text {
  font-size: 26rpx;
  color: var(--text-tertiary);
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
