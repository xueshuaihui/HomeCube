<script setup lang="ts">
// pages/finance/account/index —— 账户管理页
//
// 实现：
//   · 账户列表展示（名称、类型、余额）
//   · 添加账户
//   · 编辑账户
//   · 删除账户

import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'

const { t } = useI18n({ useScope: 'global' })

interface Account {
  id: string
  family_id: string
  name: string
  type: string // cash/bank/card/virtual/investment
  balance_cents: number
  icon?: string
  remark?: string
  is_default: boolean
}

const accounts = ref<Account[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)
const editingAccount = ref<Account | null>(null)

// Form state
const formName = ref('')
const formType = ref('cash')
const formBalance = ref('')
const formRemark = ref('')
const formIsDefault = ref(false)

// Fetch accounts
async function fetchAccounts() {
  loading.value = true
  error.value = null

  try {
    const response = await request.get('/api/finance/accounts')
    accounts.value = response.data.items || []
  } catch (err: any) {
    console.error('Failed to fetch accounts:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Open add dialog
function openAddDialog() {
  editingAccount.value = null
  formName.value = ''
  formType.value = 'cash'
  formBalance.value = ''
  formRemark.value = ''
  formIsDefault.value = false
  showAddDialog.value = true
}

// Open edit dialog
function openEditDialog(account: Account) {
  editingAccount.value = account
  formName.value = account.name
  formType.value = account.type
  formBalance.value = (account.balance_cents / 100).toFixed(2)
  formRemark.value = account.remark || ''
  formIsDefault.value = account.is_default
  showAddDialog.value = true
}

// Submit account (add or update)
async function handleSubmit() {
  if (!formName.value.trim()) {
    uni.showToast({ title: '请输入账户名称', icon: 'none' })
    return
  }

  try {
    const payload = {
      name: formName.value.trim(),
      type: formType.value,
      balance_cents: Math.round(parseFloat(formBalance.value || '0') * 100),
      remark: formRemark.value.trim(),
      is_default: formIsDefault.value,
    }

    if (editingAccount.value) {
      await request.put(`/api/finance/accounts/${editingAccount.value.id}`, payload)
      uni.showToast({ title: '更新成功', icon: 'success' })
    } else {
      await request.post('/api/finance/accounts', payload)
      uni.showToast({ title: '添加成功', icon: 'success' })
    }

    showAddDialog.value = false
    await fetchAccounts()
  } catch (err: any) {
    console.error('Failed to save account:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

// Delete account
async function handleDelete(account: Account) {
  uni.showModal({
    title: '确认删除',
    content: `确定要删除账户"${account.name}"吗？`,
    success: async (res) => {
      if (res.confirm) {
        try {
          await request.delete(`/api/finance/accounts/${account.id}`)
          uni.showToast({ title: '删除成功', icon: 'success' })
          await fetchAccounts()
        } catch (err: any) {
          console.error('Failed to delete account:', err)
          uni.showToast({ title: err.message || '删除失败', icon: 'none' })
        }
      }
    },
  })
}

// Get account type label
function getTypeLabel(type: string): string {
  const map: Record<string, string> = {
    cash: '现金',
    bank: '银行卡',
    card: '信用卡',
    virtual: '虚拟账户',
    investment: '投资账户',
  }
  return map[type] || type
}

onMounted(() => {
  fetchAccounts()
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
      <button @click="fetchAccounts">重试</button>
    </view>

    <!-- Account list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="accounts.length === 0" class="hc-empty">
        <text>暂无账户</text>
        <button class="hc-empty-btn" @click="openAddDialog">添加账户</button>
      </view>

      <view v-for="account in accounts" :key="account.id" class="acc-item">
        <view class="acc-header">
          <view class="acc-info">
            <text class="acc-name">{{ account.name }}</text>
            <text v-if="account.is_default" class="acc-badge">默认</text>
          </view>
          <text class="acc-balance">{{ formatAmount(account.balance_cents) }}</text>
        </view>
        <view class="acc-footer">
          <text class="acc-type">{{ getTypeLabel(account.type) }}</text>
          <view class="acc-actions">
            <text class="acc-action" @click="openEditDialog(account)">编辑</text>
            <text class="acc-action acc-delete" @click="handleDelete(account)">删除</text>
          </view>
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
        <text class="dialog-title">{{ editingAccount ? '编辑账户' : '添加账户' }}</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">账户名称</text>
            <input v-model="formName" class="form-input" placeholder="例如：工商银行" />
          </view>

          <view class="form-row">
            <text class="form-label">账户类型</text>
            <picker
              mode="selector"
              :range="['现金', '银行卡', '信用卡', '虚拟账户', '投资账户']"
              :value="['cash', 'bank', 'card', 'virtual', 'investment'].indexOf(formType)"
              @change="(e: any) => formType = ['cash', 'bank', 'card', 'virtual', 'investment'][e.detail.value]"
            >
              <view class="form-picker">
                <text>{{ getTypeLabel(formType) }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">初始余额</text>
            <input v-model="formBalance" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">备注</text>
            <textarea v-model="formRemark" class="form-textarea" placeholder="选填" maxlength="200" />
          </view>

          <view class="form-row form-checkbox">
            <checkbox :checked="formIsDefault" @click="formIsDefault = !formIsDefault" />
            <text>设为默认账户</text>
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

/* Account item */
.acc-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.acc-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.acc-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.acc-name {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.acc-badge {
  padding: 4rpx 12rpx;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.acc-balance {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.acc-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.acc-type {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.acc-actions {
  display: flex;
  gap: 24rpx;
}

.acc-action {
  font-size: 26rpx;
  color: var(--color-primary);
}

.acc-delete {
  color: var(--color-error);
}

/* FAB */
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

/* Dialog */
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
.form-textarea,
.form-picker {
  width: 100%;
  padding: 16rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.form-textarea {
  min-height: 120rpx;
}

.form-checkbox {
  display: flex;
  align-items: center;
  gap: 12rpx;
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
