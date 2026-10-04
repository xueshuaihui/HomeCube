<script setup lang="ts">
// pages/finance/transaction/create —— 记账表单页
//
// 实现：
//   · 两种类型可切换：支出/收入。转账**不在本表单提供**：服务端 CreateTransactionRequest
//     只有一个 `account_id`，没有契约里的 `target_account_id` 列（只有 `transfer_group_id`），
//     一次调用落不成对的两行、也不做符号归一 → 单选的「转账」会写成一条半笔脏数据，
//     因此此处不给这个入口，转账写入待服务端补成对接口后再接（已上报）。
//   · 金额输入（支持小数点，提交前转整数分，全链路无浮点）
//   · 分类选择、账户选择、日期时间选择、备注
//   · 幂等提交（client_request_id）
//   · 读接口一律带服务端必填的 family_id（handler/finance.go 的 List* 缺它即 400），
//     且请求层 resolve 的就是裸响应体，直接读 `items`
//   · **本页没有附件位**：记账的凭证图片要先有上传口才能落。服务端 P1 未提供
//     `POST /api/finance/transactions/{id}/attachments` 或任何对象存储上传接口
//     （svc-finance 只有 transactions/categories/accounts/budgets/bills 这几组路由，
//     `homeos_media_jobs` 表也未出生），客户端把 `uni.chooseImage` 的 `tempFilePaths`
//     （H5 下就是 `blob:` URL）塞进 POST 载荷，落库的是一条指向浏览器内存的地址的垃圾。
//     因此这里既不发这个字段、也不放一个点了没用的选择器 —— 服务端缺口已上报。
//
// 字段名口径（服务端 JSON tag，不猜、不造）：
//   · 备注 = `description`（`CreateTransactionRequest.Description`）
//   · 账户余额 = `balance`（整数分，`model.FinanceAccount.Balance`）
//   · 分类与账户都**没有** type / parent_id 列（迁移 finance_0001_base_schema 里没有这两列），
//     因此本表单不按类型过滤分类，只在客户端剔掉停用分类与已归档账户。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request } from '@/utils/request'
import { parseAmountToCents, newIdempotencyUUID } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

type TransactionType = 'expense' | 'income' | 'transfer'

/** 服务端 model.FinanceCategory 的 JSON 形状。 */
interface Category {
  id: string
  family_id?: string
  name: string
  icon?: string
  is_active?: boolean
  sort_order?: number
}

/** 服务端 model.FinanceAccount 的 JSON 形状（`balance` 就是整数分）。 */
interface Account {
  id: string
  family_id?: string
  name: string
  type: string
  balance: number
  is_archived?: boolean
}

const txType = ref<TransactionType>('expense')
const amountInput = ref('')
const categoryId = ref('')
const accountId = ref('')
const occurredAt = ref(new Date().toISOString())
const remark = ref('')
const submitting = ref(false)
const loadError = ref('')

const categories = ref<Category[]>([])
const accounts = ref<Account[]>([])

// Computed: amount in cents
const amountCents = computed(() => {
  if (!amountInput.value) return 0
  return parseAmountToCents(amountInput.value)
})

/** family_id 只读 shell store 的那一份会话快照，分包不自存、不在本页重拉 `/families`。 */
async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

// Load categories and accounts
async function loadFormData() {
  loadError.value = ''
  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      loadError.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    // Load categories（服务端只按 family_id / is_active 过滤，分类没有收支类型列）
    const catBody = await request.get<{ items?: Category[] }>('/api/finance/categories', {
      params: { family_id: familyId },
    })
    categories.value = (Array.isArray(catBody?.items) ? catBody.items : []).filter(
      (c) => c && c.id && c.is_active !== false
    )

    // Load accounts（已归档账户不参与记账，4.5.9）
    const accBody = await request.get<{ items?: Account[] }>('/api/finance/accounts', {
      params: { family_id: familyId },
    })
    accounts.value = (Array.isArray(accBody?.items) ? accBody.items : []).filter(
      (a) => a && a.id && a.is_archived !== true
    )

    // Set default account (first one)
    if (accounts.value.length > 0 && !accountId.value) {
      accountId.value = accounts.value[0].id
    }
    if (accounts.value.length === 0) loadError.value = '还没有账户，请先在流水页的「账户管理」新建'
  } catch (err: any) {
    // 失败只落本页错误态（第八章：错误可读可重试），不打 console 当行为
    loadError.value = err?.message || '分类或账户加载失败'
  }
}

// Handle type change：分类不按类型分（服务端无该列），因此切类型不重取列表。
function handleTypeChange(type: TransactionType) {
  txType.value = type
  categoryId.value = ''
}

// Submit transaction
async function handleSubmit() {  // Validation
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

  const familyId = await resolveSessionFamilyId()
  if (!familyId) {
    uni.showToast({ title: loadError.value || '缺少家庭，无法记账', icon: 'none' })
    return
  }

  submitting.value = true

  try {
    const payload = {
      // CreateTransactionRequest 的 family_id 是 binding:"required"，缺它服务端直接 400
      family_id: familyId,
      type: txType.value,
      amount_cents: amountCents.value,
      category_id: categoryId.value,
      account_id: accountId.value,
      occurred_at: occurredAt.value,
      description: remark.value,
      // 幂等键必须是 **UUID**：finance.finance_transaction.client_request_id 的列类型是
      // uuid（迁移 finance_0001_base_schema），而旧写法 `${Date.now()}-${random}` 产出的是
      // 「1791049719700-jybfx232uc9」这种带短横线的非 UUID 串，PostgreSQL 直接报
      // `invalid input syntax for type uuid`（SQLSTATE 22P02），**每一次记账都 500**。
      // 契约 finance.yaml:169-171 只写 `type: string` 没标 format:uuid，是契约侧的缺口；
      // 这里按数据库列的实际类型生成合法 UUID（RFC 4122 v4）。
      client_request_id: newIdempotencyUUID(),
    }

    await request.post('/api/finance/transactions', payload)

    uni.showToast({ title: '记账成功', icon: 'success' })

    // 本页由 flow 列表 navigateTo 压栈进入，保存后出栈回到流水列表（§2.3 第 2 行）
    setTimeout(() => {
      uni.navigateBack()
    }, 1500)
  } catch (err: any) {
    uni.showToast({ title: err?.message || '记账失败', icon: 'none' })
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  loadFormData()
})
</script>

<template>
  <view class="hc-page">
    <scroll-view class="hc-scroll" scroll-y>
      <!-- Transaction type selector（只有支出/收入：见文件头「转账」说明） -->
      <view class="type-selector">
        <view
          v-for="type in ['expense', 'income']"
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
        <text v-if="loadError" class="form-error">{{ loadError }}</text>
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
        <text v-if="categories.length === 0" class="form-hint">
          本家庭还没有可用分类，可在流水页的「分类管理」新建
        </text>
      </view>

      <!-- Account selector -->
      <view class="form-section">
        <text class="section-label">账户</text>
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

/* 表单提示：错误与空态都只用令牌色 */
.form-error {
  display: block;
  margin-top: 8rpx;
  font-size: 24rpx;
  color: var(--color-error);
}

.form-hint {
  display: block;
  margin-top: 16rpx;
  font-size: 24rpx;
  color: var(--text-secondary);
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
