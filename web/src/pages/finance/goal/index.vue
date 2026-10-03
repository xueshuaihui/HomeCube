<script setup lang="ts">
// pages/finance/goal/index —— 储蓄目标页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 目标列表：展示所有储蓄目标及进度
//   · 创建目标：设置新的储蓄目标
//   · 更新进度：添加存款记录
//   · 完成目标：标记目标为已完成
//   · 对接 GET /api/finance/goals?family_id=
//     POST /api/finance/goals
//     POST /api/finance/goals/:id/deposits
//     PUT /api/finance/goals/:id/complete

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount, formatDate } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface Goal {
  id: string
  family_id: string
  name: string
  target_amount_cents: number
  current_amount_cents: number
  deadline?: string
  description?: string
  status: 'active' | 'completed'
  created_at: string
  updated_at: string
}

const goals = ref<Goal[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)
const selectedGoal = ref<Goal | null>(null)
const showDepositDialog = ref(false)

// Form state for new goal
const formName = ref('')
const formTargetAmount = ref('')
const formDeadline = ref('')
const formDescription = ref('')

// Form state for deposit
const formDepositAmount = ref('')
const formDepositDate = ref(new Date().toISOString().slice(0, 10))
const formDepositNote = ref('')

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

async function fetchGoals() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      goals.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const body = await request.get<{ items?: Goal[] }>('/api/finance/goals', {
      params: { family_id: familyId },
    })
    goals.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch goals:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  selectedGoal.value = null
  formName.value = ''
  formTargetAmount.value = ''
  formDeadline.value = ''
  formDescription.value = ''
  showAddDialog.value = true
}

async function handleSubmit() {
  if (!formName.value.trim()) {
    uni.showToast({ title: '请输入目标名称', icon: 'none' })
    return
  }

  const targetAmount = Number.parseFloat(formTargetAmount.value)
  if (!targetAmount || targetAmount <= 0) {
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
      name: formName.value.trim(),
      target_amount_cents: Math.round(targetAmount * 100),
    }

    if (formDeadline.value) payload.deadline = formDeadline.value
    if (formDescription.value) payload.description = formDescription.value.trim()

    await request.post('/api/finance/goals', payload)
    uni.showToast({ title: '创建成功', icon: 'success' })
    showAddDialog.value = false
    await fetchGoals()
  } catch (err: any) {
    console.error('Failed to create goal:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

function openDepositDialog(goal: Goal) {
  selectedGoal.value = goal
  formDepositAmount.value = ''
  formDepositDate.value = new Date().toISOString().slice(0, 10)
  formDepositNote.value = ''
  showDepositDialog.value = true
}

async function handleDeposit() {
  if (!selectedGoal.value) return

  const amount = Number.parseFloat(formDepositAmount.value)
  if (!amount || amount <= 0) {
    uni.showToast({ title: '请输入有效金额', icon: 'none' })
    return
  }

  try {
    await request.post(`/api/finance/goals/${selectedGoal.value.id}/deposits`, {
      amount_cents: Math.round(amount * 100),
      date: formDepositDate.value,
      note: formDepositNote.value.trim(),
    })
    uni.showToast({ title: '存款已记录', icon: 'success' })
    showDepositDialog.value = false
    await fetchGoals()
  } catch (err: any) {
    console.error('Failed to add deposit:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

async function completeGoal(goal: Goal) {
  uni.showModal({
    title: '完成目标',
    content: `确定要标记「${goal.name}」为已完成吗？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/goals/${goal.id}/complete`)
        uni.showToast({ title: '目标已完成', icon: 'success' })
        await fetchGoals()
      } catch (err: any) {
        console.error('Failed to complete goal:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

// Computed: active goals
const activeGoals = computed(() => goals.value.filter((g) => g.status === 'active'))
const completedGoals = computed(() => goals.value.filter((g) => g.status === 'completed'))

// Computed: total saved
const totalSaved = computed(() =>
  goals.value.reduce((sum, g) => sum + g.current_amount_cents, 0)
)

onMounted(() => {
  fetchGoals()
})
</script>

<template>
  <view class="hc-page">
    <!-- Summary bar -->
    <view class="summary-bar">
      <view class="summary-item">
        <text class="summary-label">总储蓄</text>
        <text class="summary-value">{{ formatAmount(totalSaved) }}</text>
      </view>
      <view class="summary-item">
        <text class="summary-label">进行中</text>
        <text class="summary-value">{{ activeGoals.length }}</text>
      </view>
      <view class="summary-item">
        <text class="summary-label">已完成</text>
        <text class="summary-value">{{ completedGoals.length }}</text>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchGoals">重试</button>
    </view>

    <!-- Goal list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="goals.length === 0" class="hc-empty">
        <text>暂无储蓄目标</text>
        <button class="hc-empty-btn" @click="openAddDialog">创建目标</button>
      </view>

      <!-- Active goals -->
      <view v-if="activeGoals.length > 0" class="section">
        <text class="section-title">进行中</text>
        <view v-for="goal in activeGoals" :key="goal.id" class="goal-item">
          <view class="goal-header">
            <text class="goal-name">{{ goal.name }}</text>
            <text class="goal-target">{{ formatAmount(goal.target_amount_cents) }}</text>
          </view>

          <view class="goal-progress">
            <view class="progress-info">
              <text class="progress-current">{{ formatAmount(goal.current_amount_cents) }}</text>
              <text class="progress-percent">
                {{ ((goal.current_amount_cents / goal.target_amount_cents) * 100).toFixed(0) }}%
              </text>
            </view>
            <view class="progress-bar-bg">
              <view
                class="progress-bar-fill"
                :style="{ width: `${Math.min((goal.current_amount_cents / goal.target_amount_cents) * 100, 100)}%` }"
              />
            </view>
          </view>

          <view class="goal-footer">
            <text v-if="goal.deadline" class="goal-deadline">截止: {{ formatDate(goal.deadline) }}</text>
            <text v-if="goal.description" class="goal-desc">{{ goal.description }}</text>
          </view>

          <view class="goal-actions">
            <text class="goal-action goal-deposit" @click="openDepositDialog(goal)">存入</text>
            <text class="goal-action goal-complete" @click="completeGoal(goal)">完成</text>
          </view>
        </view>
      </view>

      <!-- Completed goals -->
      <view v-if="completedGoals.length > 0" class="section">
        <text class="section-title">已完成</text>
        <view v-for="goal in completedGoals" :key="goal.id" class="goal-item goal-completed">
          <view class="goal-header">
            <text class="goal-name">{{ goal.name }}</text>
            <text class="goal-badge">已完成</text>
          </view>
          <view class="goal-summary">
            <text class="goal-achieved">{{ formatAmount(goal.current_amount_cents) }}</text>
          </view>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add goal dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">创建储蓄目标</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">目标名称</text>
            <input v-model="formName" class="form-input" placeholder="例如：旅行基金" />
          </view>

          <view class="form-row">
            <text class="form-label">目标金额</text>
            <input v-model="formTargetAmount" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">截止日期（可选）</text>
            <picker
              mode="date"
              :value="formDeadline"
              @change="(e: any) => formDeadline = e.detail.value"
            >
              <view class="form-picker">
                <text>{{ formDeadline || '请选择' }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">描述（可选）</text>
            <textarea
              v-model="formDescription"
              class="form-textarea"
              placeholder="添加描述..."
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

    <!-- Deposit dialog -->
    <view v-if="showDepositDialog && selectedGoal" class="dialog-mask" @click="showDepositDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">存入资金</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">存入金额</text>
            <input v-model="formDepositAmount" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">存入日期</text>
            <picker
              mode="date"
              :value="formDepositDate"
              @change="(e: any) => formDepositDate = e.detail.value"
            >
              <view class="form-picker">
                <text>{{ formDepositDate }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">备注（可选）</text>
            <textarea
              v-model="formDepositNote"
              class="form-textarea"
              placeholder="添加备注..."
              maxlength="200"
            />
          </view>
        </view>

        <view class="dialog-actions">
          <button class="dialog-btn dialog-cancel" @click="showDepositDialog = false">取消</button>
          <button class="dialog-btn dialog-confirm" @click="handleDeposit">确定</button>
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

.section {
  margin-bottom: 32rpx;
}

.section-title {
  display: block;
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 16rpx;
  padding-left: 8rpx;
}

.goal-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.goal-completed {
  opacity: 0.7;
}

.goal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16rpx;
}

.goal-name {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.goal-target {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.goal-badge {
  padding: 4rpx 12rpx;
  background-color: var(--color-success);
  color: var(--color-white);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.goal-progress {
  margin-bottom: 12rpx;
}

.progress-info {
  display: flex;
  justify-content: space-between;
  margin-bottom: 8rpx;
}

.progress-current,
.progress-percent {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.progress-bar-bg {
  height: 12rpx;
  background-color: var(--bg-tertiary);
  border-radius: 6rpx;
  overflow: hidden;
}

.progress-bar-fill {
  height: 100%;
  background-color: var(--color-success);
  border-radius: 6rpx;
  transition: width 0.3s ease;
}

.goal-footer {
  margin-bottom: 12rpx;
}

.goal-deadline,
.goal-desc {
  display: block;
  font-size: 24rpx;
  color: var(--text-tertiary);
  margin-bottom: 4rpx;
}

.goal-actions {
  display: flex;
  gap: 24rpx;
  justify-content: flex-end;
}

.goal-action {
  font-size: 26rpx;
}

.goal-deposit {
  color: var(--color-success);
}

.goal-complete {
  color: var(--color-primary);
}

.goal-summary {
  text-align: center;
  padding: 16rpx 0;
}

.goal-achieved {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--color-success);
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
