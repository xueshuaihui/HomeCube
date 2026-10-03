<script setup lang="ts">
// pages/finance/transaction/create —— 记账表单页
//
// 实现：
//   · 三种类型切换：支出/收入/转账
//   · 金额输入（支持小数点，存储时转为分）
//   · 分类选择（二级分类）
//   · 账户选择
//   · 日期时间选择
//   · 备注输入
//   · 附件上传（最多3张）
//   · 幂等提交（client_request_id）

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { request } from '@/utils/request'
import { parseAmountToCents } from '@/utils/format'

const { t } = useI18n({ useScope: 'global' })
const router = useRouter()

type TransactionType = 'expense' | 'income' | 'transfer'

interface Category {
  id: string
  name: string
  icon?: string
  parent_id?: string
}

interface Account {
  id: string
  name: string
  type: string
  balance_cents: number
}

const txType = ref<TransactionType>('expense')
const amountInput = ref('')
const categoryId = ref('')
const accountId = ref('')
const toAccountId = ref('') // for transfer
const occurredAt = ref(new Date().toISOString())
const remark = ref('')
const attachments = ref<string[]>([])
const submitting = ref(false)

const categories = ref<Category[]>([])
const accounts = ref<Account[]>([])

// Computed: amount in cents
const amountCents = computed(() => {
  if (!amountInput.value) return 0
  return parseAmountToCents(amountInput.value)
})

// Load categories and accounts
async function loadFormData() {
  try {
    // Load categories based on transaction type
    const catResponse = await request.get('/api/finance/categories', {
      params: { type: txType.value },
    })
    categories.value = catResponse.data.items || []

    // Load accounts
    const accResponse = await request.get('/api/finance/accounts')
    accounts.value = accResponse.data.items || []

    // Set default account (first one)
    if (accounts.value.length > 0 && !accountId.value) {
      accountId.value = accounts.value[0].id
    }
  } catch (err: any) {
    console.error('Failed to load form data:', err)
  }
}

// Handle type change
function handleTypeChange(type: TransactionType) {
  txType.value = type
  categoryId.value = ''
  loadFormData()
}

// Submit transaction
async function handleSubmit() {
  // Validation
  if (!amountInput.value || amountCents.value <= 0) {
    uni.showToast({ title: '请输入有效金额', icon: 'none' })
    return
  }

  if (!categoryId.value) {
    uni.showToast({ title: '请选择分类', icon: 'none' })
    return
  }

  if (!accountId.value) {
    uni.showToast({ title: '请选择账户', icon: 'none' })
    return
  }

  if (txType.value === 'transfer' && !toAccountId.value) {
    uni.showToast({ title: '请选择转入账户', icon: 'none' })
    return
  }

  submitting.value = true

  try {
    const payload: any = {
      type: txType.value,
      amount_cents: amountCents.value,
      category_id: categoryId.value,
      account_id: accountId.value,
      occurred_at: occurredAt.value,
      remark: remark.value,
      attachments: attachments.value,
      client_request_id: `${Date.now()}-${Math.random().toString(36).slice(2)}`, // Simple idempotency key
    }

    if (txType.value === 'transfer') {
      payload.to_account_id = toAccountId.value
    }

    await request.post('/api/finance/transactions', payload)

    uni.showToast({ title: '记账成功', icon: 'success' })

    // Navigate back to flow list
    setTimeout(() => {
      router.back()
    }, 1500)
  } catch (err: any) {
    console.error('Failed to create transaction:', err)
    uni.showToast({ title: err.message || '记账失败', icon: 'none' })
  } finally {
    submitting.value = false
  }
}

// Choose image attachment
function chooseImage() {
  if (attachments.value.length >= 3) {
    uni.showToast({ title: '最多上传3张图片', icon: 'none' })
    return
  }

  uni.chooseImage({
    count: 3 - attachments.value.length,
    sizeType: ['compressed'],
    sourceType: ['album', 'camera'],
    success: (res) => {
      // TODO: Upload to server and get URL
      // For now, just store temp paths
      res.tempFilePaths.forEach((path) => {
        attachments.value.push(path)
      })
    },
  })
}

// Remove attachment
function removeAttachment(index: number) {
  attachments.value.splice(index, 1)
}

onMounted(() => {
  loadFormData()
})
</script>

<template>
  <view class="hc-page">
    <scroll-view class="hc-scroll" scroll-y>
      <!-- Transaction type selector -->
      <view class="type-selector">
        <view
          v-for="type in ['expense', 'income', 'transfer']"
          :key="type"
          class="type-btn"
          :class="{ active: txType === type }"
          @click="handleTypeChange(type as TransactionType)"
        >
          <text>{{ t(`finance.tx_type.${type}`) }}</text>
        </view>
      </view>

      <!-- Amount input -->
      <view class="form-section">
        <text class="section-label">金额</text>
        <view class="amount-input-wrapper">
          <text class="currency-symbol">¥</text>
          <input
            v-model="amountInput"
            class="amount-input"
            type="digit"
            placeholder="0.00"
            placeholder-class="amount-placeholder"
          />
        </view>
      </view>

      <!-- Category selector -->
      <view class="form-section">
        <text class="section-label">分类</text>
        <view class="category-grid">
          <view
            v-for="cat in categories"
            :key="cat.id"
            class="category-item"
            :class="{ selected: categoryId === cat.id }"
            @click="categoryId = cat.id"
          >
            <view class="cat-icon">{{ cat.name.charAt(0) }}</view>
            <text class="cat-name">{{ cat.name }}</text>
          </view>
        </view>
      </view>

      <!-- Account selector -->
      <view class="form-section">
        <text class="section-label">{{ txType === 'transfer' ? '转出账户' : '账户' }}</text>
        <picker
          mode="selector"
          :range="accounts.map(a => a.name)"
          :value="accounts.findIndex(a => a.id === accountId)"
          @change="(e: any) => accountId = accounts[e.detail.value]?.id"
        >
          <view class="picker-value">
            <text>{{ accounts.find(a => a.id === accountId)?.name || '请选择账户' }}</text>
          </view>
        </picker>
      </view>

      <!-- To account selector (for transfer) -->
      <view v-if="txType === 'transfer'" class="form-section">
        <text class="section-label">转入账户</text>
        <picker
          mode="selector"
          :range="accounts.filter(a => a.id !== accountId).map(a => a.name)"
          @change="(e: any) => {
            const filtered = accounts.filter(a => a.id !== accountId)
            toAccountId = filtered[e.detail.value]?.id
          }"
        >
          <view class="picker-value">
            <text>{{ accounts.find(a => a.id === toAccountId)?.name || '请选择转入账户' }}</text>
          </view>
        </picker>
      </view>

      <!-- Date time picker -->
      <view class="form-section">
        <text class="section-label">时间</text>
        <picker
          mode="date"
          :value="occurredAt.slice(0, 10)"
          @change="(e: any) => {
            const date = new Date(e.detail.value + 'T' + occurredAt.slice(11, 19))
            occurredAt = date.toISOString()
          }"
        >
          <view class="picker-value">
            <text>{{ occurredAt.slice(0, 10) }}</text>
          </view>
        </picker>
      </view>

      <!-- Remark input -->
      <view class="form-section">
        <text class="section-label">备注</text>
        <textarea
          v-model="remark"
          class="remark-input"
          placeholder="添加备注..."
          maxlength="200"
        />
      </view>

      <!-- Attachments -->
      <view class="form-section">
        <text class="section-label">附件（最多3张）</text>
        <view class="attachment-grid">
          <view
            v-for="(img, idx) in attachments"
            :key="idx"
            class="attachment-item"
          >
            <image :src="img" mode="aspectFill" class="attachment-img" />
            <view class="attachment-remove" @click="removeAttachment(idx)">
              <text>×</text>
            </view>
          </view>
          <view v-if="attachments.length < 3" class="attachment-add" @click="chooseImage">
            <text>＋</text>
          </view>
        </view>
      </view>
    </scroll-view>

    <!-- Submit button -->
    <view class="submit-bar">
      <button
        class="submit-btn"
        :disabled="submitting"
        @click="handleSubmit"
      >
        {{ submitting ? '提交中...' : '保存' }}
      </button>
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

.hc-scroll {
  flex: 1;
  padding: 24rpx;
  padding-bottom: 140rpx; /* Space for submit button */
}

/* Type selector */
.type-selector {
  display: flex;
  gap: 16rpx;
  margin-bottom: 32rpx;
}

.type-btn {
  flex: 1;
  padding: 24rpx 0;
  text-align: center;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  font-size: 28rpx;
  color: var(--text-secondary);
}

.type-btn.active {
  background-color: var(--color-primary);
  color: var(--color-white);
  font-weight: 600;
}

/* Form sections */
.form-section {
  margin-bottom: 32rpx;
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.section-label {
  display: block;
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 16rpx;
}

/* Amount input */
.amount-input-wrapper {
  display: flex;
  align-items: center;
  gap: 16rpx;
}

.currency-symbol {
  font-size: 48rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.amount-input {
  flex: 1;
  font-size: 48rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.amount-placeholder {
  color: var(--text-tertiary);
}

/* Category grid */
.category-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16rpx;
}

.category-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
  padding: 16rpx 8rpx;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
}

.category-item.selected {
  background-color: var(--color-primary-light);
  color: var(--color-white);
}

.cat-icon {
  width: 64rpx;
  height: 64rpx;
  border-radius: 50%;
  background-color: var(--bg-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28rpx;
}

.category-item.selected .cat-icon {
  background-color: var(--overlay-light);
}

.cat-name {
  font-size: 24rpx;
  text-align: center;
}

/* Picker value */
.picker-value {
  padding: 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

/* Remark input */
.remark-input {
  width: 100%;
  min-height: 120rpx;
  padding: 16rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

/* Attachment grid */
.attachment-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16rpx;
}

.attachment-item {
  position: relative;
  aspect-ratio: 1;
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.attachment-img {
  width: 100%;
  height: 100%;
}

.attachment-remove {
  position: absolute;
  top: 8rpx;
  right: 8rpx;
  width: 40rpx;
  height: 40rpx;
  border-radius: 50%;
  background-color: var(--overlay-dark);
  color: var(--color-white);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28rpx;
}

.attachment-add {
  aspect-ratio: 1;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 48rpx;
  color: var(--text-tertiary);
}

/* Submit bar */
.submit-bar {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  padding: 24rpx;
  padding-bottom: calc(24rpx + env(safe-area-inset-bottom));
  background-color: var(--bg-primary);
  border-top: 1rpx solid var(--divider-color);
}

.submit-btn {
  width: 100%;
  padding: 28rpx 0;
  background-color: var(--color-primary);
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 32rpx;
  font-weight: 600;
}

.submit-btn:disabled {
  opacity: 0.6;
}
</style>
