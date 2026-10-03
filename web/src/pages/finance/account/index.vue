<script setup lang="ts">
// pages/finance/account/index —— 账户管理页（§5.1 财务面末位「账户与设置」的账户维护部分，
// 由流水页工具栏「账户管理」进入；settings 页本身本期未建，见本轮上报的未实现项）。
//
// 口径全部对齐 svc-finance 的路由表与 JSON tag（`cmd/svc-finance/main.go` +
// `internal/model/finance.go`），不猜字段名：
//   · 列表  GET  /api/finance/accounts?family_id=   → 裸回 `{items:[FinanceAccount]}`
//     （family_id 是 ListAccounts 的必填 query，缺它即 400）
//   · 新建  POST /api/finance/accounts              body `{family_id, name, type}`
//     （服务端 CreateAccount 只收这三个字段，余额恒为 0，故表单不放「初始余额」）
//   · 归档  PUT  /api/finance/accounts/:id/archive  （余额非 0 时服务端回 409 + error 文案）
//   服务端**没有** `PUT /accounts/:id` 与 `DELETE /accounts/:id` 两条路由，因此本页不提供
//   「编辑 / 删除」两个动作 —— 挂上去就是必定 404 的按钮。
//   · 余额字段是 `balance`（整数分；契约 Account 里叫 `balance_cents`，已作为定版冲突上报）
//   · 类型枚举取契约的 cash / bank / third_party / credit_card

import { ref, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

/** 服务端 model.FinanceAccount 的 JSON 形状。`balance` 单位：整数分。 */
interface Account {
  id: string
  family_id: string
  name: string
  type: string
  balance: number
  is_archived: boolean
  archived_at?: string | null
  version: number
  created_at: string
  updated_at: string
}

/** 契约 `/accounts` 200 体。 */
interface AccountListBody {
  items?: Account[]
}

/** 账户类型枚举与文案（顺序即 picker 顺序）。 */
const ACCOUNT_TYPES = ['cash', 'bank', 'third_party', 'credit_card']
const TYPE_LABELS: Record<string, string> = {
  cash: '现金',
  bank: '银行卡',
  third_party: '第三方支付',
  credit_card: '信用卡',
}

const accounts = ref<Account[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)

// Form state
const formName = ref('')
const formType = ref('cash')

const homeStore = useHomeStore()

/** family_id 读 shell store 的会话快照：分包不自存一份、不在本页重拉 `/families`。 */
async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

// Fetch accounts
async function fetchAccounts() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      accounts.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    // 请求层 resolve 的就是裸响应体：直接读 items。
    const body = await request.get<AccountListBody>('/api/finance/accounts', {
      params: { family_id: familyId },
    })
    accounts.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch accounts:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Open add dialog
function openAddDialog() {
  formName.value = ''
  formType.value = 'cash'
  showAddDialog.value = true
}

// Submit account（服务端只有创建与归档，没有更新，因此这里只有「新建」一条路）
async function handleSubmit() {
  if (!formName.value.trim()) {
    uni.showToast({ title: '请输入账户名称', icon: 'none' })
    return
  }

  const familyId = await resolveSessionFamilyId()
  if (!familyId) {
    uni.showToast({ title: error.value || '缺少家庭，无法新建账户', icon: 'none' })
    return
  }

  try {
    await request.post('/api/finance/accounts', {
      family_id: familyId,
      name: formName.value.trim(),
      type: formType.value,
    })
    uni.showToast({ title: '添加成功', icon: 'success' })

    showAddDialog.value = false
    await fetchAccounts()
  } catch (err: any) {
    console.error('Failed to save account:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

// Archive account（4.5.9 的停用口径：余额非 0 时服务端以 409 拒绝）
function archiveAccount(account: Account) {
  uni.showModal({
    title: '确认归档',
    content: `确定要归档账户「${account.name}」吗？归档后不再出现在记账与流水的账户名里。`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/accounts/${account.id}/archive`)
        uni.showToast({ title: '归档成功', icon: 'success' })
        await fetchAccounts()
      } catch (err: any) {
        console.error('Failed to archive account:', err)
        uni.showToast({ title: err.message || '归档失败', icon: 'none' })
      }
    },
  })
}

// Get account type label
function getTypeLabel(type: string): string {
  return TYPE_LABELS[type] || type
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
            <text v-if="account.is_archived" class="acc-badge">已归档</text>
          </view>
          <text class="acc-balance">{{ formatAmount(account.balance) }}</text>
        </view>
        <view class="acc-footer">
          <text class="acc-type">{{ getTypeLabel(account.type) }}</text>
          <view class="acc-actions">
            <text v-if="!account.is_archived" class="acc-action" @click="archiveAccount(account)">归档</text>
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
        <text class="dialog-title">添加账户</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">账户名称</text>
            <input v-model="formName" class="form-input" placeholder="例如：工商银行" />
          </view>

          <view class="form-row">
            <text class="form-label">账户类型</text>
            <picker
              mode="selector"
              :range="ACCOUNT_TYPES.map(getTypeLabel)"
              :value="ACCOUNT_TYPES.indexOf(formType)"
              @change="(e: any) => formType = ACCOUNT_TYPES[e.detail.value]"
            >
              <view class="form-picker">
                <text>{{ getTypeLabel(formType) }}</text>
              </view>
            </picker>
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
