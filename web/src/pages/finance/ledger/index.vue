<script setup lang="ts">
// pages/finance/ledger/index —— 账本管理页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 账本列表：展示所有账本及其统计信息
//   · 创建账本：新建独立账本
//   · 切换账本：设置默认账本
//   · 删除账本：清空并删除账本（需确认）
//   · 对接 GET /api/finance/ledgers?family_id=
//     POST /api/finance/ledgers
//     PUT /api/finance/ledgers/:id
//     DELETE /api/finance/ledgers/:id

import { ref, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface Ledger {
  id: string
  family_id: string
  name: string
  description?: string
  is_default: boolean
  transaction_count: number
  total_income_cents: number
  total_expense_cents: number
  created_at: string
  updated_at: string
}

const ledgers = ref<Ledger[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)

// Form state
const formName = ref('')
const formDescription = ref('')

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

async function fetchLedgers() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      ledgers.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const body = await request.get<{ items?: Ledger[] }>('/api/finance/ledgers', {
      params: { family_id: familyId },
    })
    ledgers.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch ledgers:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  formName.value = ''
  formDescription.value = ''
  showAddDialog.value = true
}

async function handleSubmit() {
  if (!formName.value.trim()) {
    uni.showToast({ title: '请输入账本名称', icon: 'none' })
    return
  }

  const familyId = await resolveSessionFamilyId()
  if (!familyId) {
    uni.showToast({ title: '缺少家庭信息', icon: 'none' })
    return
  }

  try {
    await request.post('/api/finance/ledgers', {
      family_id: familyId,
      name: formName.value.trim(),
      description: formDescription.value.trim(),
    })
    uni.showToast({ title: '创建成功', icon: 'success' })
    showAddDialog.value = false
    await fetchLedgers()
  } catch (err: any) {
    console.error('Failed to create ledger:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

async function setDefaultLedger(ledger: Ledger) {
  if (ledger.is_default) return

  uni.showModal({
    title: '设为默认账本',
    content: `确定要将「${ledger.name}」设为默认账本吗？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/ledgers/${ledger.id}`, {
          is_default: true,
        })
        uni.showToast({ title: '设置成功', icon: 'success' })
        await fetchLedgers()
      } catch (err: any) {
        console.error('Failed to set default ledger:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

function deleteLedger(ledger: Ledger) {
  if (ledger.is_default) {
    uni.showToast({ title: '不能删除默认账本', icon: 'none' })
    return
  }

  uni.showModal({
    title: '删除账本',
    content: `确定要删除账本「${ledger.name}」吗？该账本下的所有流水记录将被移至默认账本。`,
    confirmText: '删除',
    confirmColor: 'var(--color-error)',
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.delete(`/api/finance/ledgers/${ledger.id}`)
        uni.showToast({ title: '删除成功', icon: 'success' })
        await fetchLedgers()
      } catch (err: any) {
        console.error('Failed to delete ledger:', err)
        uni.showToast({ title: err.message || '删除失败', icon: 'none' })
      }
    },
  })
}

onMounted(() => {
  fetchLedgers()
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
      <button @click="fetchLedgers">重试</button>
    </view>

    <!-- Ledger list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="ledgers.length === 0" class="hc-empty">
        <text>暂无账本</text>
        <button class="hc-empty-btn" @click="openAddDialog">创建账本</button>
      </view>

      <view v-for="ledger in ledgers" :key="ledger.id" class="ledger-item">
        <view class="ledger-header">
          <view class="ledger-info">
            <text class="ledger-name">{{ ledger.name }}</text>
            <text v-if="ledger.is_default" class="ledger-badge">默认</text>
          </view>
          <view class="ledger-actions">
            <text
              v-if="!ledger.is_default"
              class="ledger-action"
              @click="setDefaultLedger(ledger)"
            >
              设为默认
            </text>
            <text
              v-if="!ledger.is_default"
              class="ledger-action ledger-delete"
              @click="deleteLedger(ledger)"
            >
              删除
            </text>
          </view>
        </view>

        <view v-if="ledger.description" class="ledger-desc">
          <text>{{ ledger.description }}</text>
        </view>

        <view class="ledger-stats">
          <view class="stat-item">
            <text class="stat-label">流水</text>
            <text class="stat-value">{{ ledger.transaction_count }}笔</text>
          </view>
          <view class="stat-item">
            <text class="stat-label">收入</text>
            <text class="stat-value stat-income">{{ formatAmount(ledger.total_income_cents) }}</text>
          </view>
          <view class="stat-item">
            <text class="stat-label">支出</text>
            <text class="stat-value stat-expense">{{ formatAmount(Math.abs(ledger.total_expense_cents)) }}</text>
          </view>
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
        <text class="dialog-title">创建账本</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">账本名称</text>
            <input v-model="formName" class="form-input" placeholder="例如：旅行账本" />
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

.ledger-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.ledger-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.ledger-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.ledger-name {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.ledger-badge {
  padding: 4rpx 12rpx;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.ledger-actions {
  display: flex;
  gap: 24rpx;
}

.ledger-action {
  font-size: 26rpx;
  color: var(--color-primary);
}

.ledger-delete {
  color: var(--color-error);
}

.ledger-desc {
  margin-bottom: 16rpx;
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
}

.ledger-stats {
  display: flex;
  gap: 24rpx;
  padding-top: 16rpx;
  border-top: 1rpx solid var(--divider-color);
}

.stat-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4rpx;
}

.stat-label {
  font-size: 24rpx;
  color: var(--text-tertiary);
}

.stat-value {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.stat-income {
  color: var(--color-success);
}

.stat-expense {
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

.form-input {
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
