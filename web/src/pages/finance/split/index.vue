<script setup lang="ts">
// pages/finance/split/index —— AA分账页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 分账列表：展示所有AA分账记录
//   · 创建分账：发起新的AA分摊
//   · 添加参与者：邀请家庭成员参与分摊
//   · 标记支付：标记某人已支付其份额
//   · 结清分账：所有人支付完成后结清
//   · 对接 GET /api/finance/splits?family_id=
//     POST /api/finance/splits
//     POST /api/finance/splits/:id/participants
//     PUT /api/finance/splits/:id/participants/:pid/pay
//     PUT /api/finance/splits/:id/settle

import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'
import { formatAmount, formatDate } from '@/utils/format'
import { useHomeStore } from '@/stores/home'

interface SplitParticipant {
  member_id: string
  member_name: string
  share_cents: number
  paid_cents: number
  is_paid: boolean
}

interface Split {
  id: string
  family_id: string
  title: string
  total_amount_cents: number
  payer_member_id?: string
  payer_name?: string
  description?: string
  participants: SplitParticipant[]
  status: 'active' | 'settled'
  created_at: string
  updated_at: string
}

const splits = ref<Split[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)
const selectedSplit = ref<Split | null>(null)
const showParticipantDialog = ref(false)

// Form state for new split
const formTitle = ref('')
const formTotalAmount = ref('')
const formPayerId = ref('')
const formDescription = ref('')
const formParticipants = ref<Array<{ member_id: string; member_name: string }>>([])

// Form state for adding participant
const formNewParticipantId = ref('')

const homeStore = useHomeStore()
const familyMembers = ref<Array<{ member_id: string; name: string }>>([])

async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

async function loadFamilyMembers() {
  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) return

    // 从家庭信息中获取成员列表
    if (homeStore.family?.members) {
      familyMembers.value = homeStore.family.members.map((m) => ({
        member_id: m.member_id,
        name: m.name,
      }))
    }
  } catch (err) {
    console.error('Failed to load family members:', err)
  }
}

async function fetchSplits() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      splits.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const body = await request.get<{ items?: Split[] }>('/api/finance/split-settlements', {
      params: { family_id: familyId },
    })
    splits.value = Array.isArray(body?.items) ? body.items : []
    await loadFamilyMembers()
  } catch (err: any) {
    console.error('Failed to fetch splits:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openAddDialog() {
  selectedSplit.value = null
  formTitle.value = ''
  formTotalAmount.value = ''
  formPayerId.value = ''
  formDescription.value = ''
  formParticipants.value = []
  showAddDialog.value = true
}

async function handleSubmit() {
  if (!formTitle.value.trim()) {
    uni.showToast({ title: '请输入分账标题', icon: 'none' })
    return
  }

  const totalAmount = Number.parseFloat(formTotalAmount.value)
  if (!totalAmount || totalAmount <= 0) {
    uni.showToast({ title: '请输入有效金额', icon: 'none' })
    return
  }

  if (formParticipants.value.length === 0) {
    uni.showToast({ title: '请至少添加一个参与者', icon: 'none' })
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
      title: formTitle.value.trim(),
      total_amount_cents: Math.round(totalAmount * 100),
      participant_ids: formParticipants.value.map((p) => p.member_id),
    }

    if (formPayerId.value) payload.payer_member_id = formPayerId.value
    if (formDescription.value) payload.description = formDescription.value.trim()

    await request.post('/api/finance/split-settlements', payload)
    uni.showToast({ title: '创建成功', icon: 'success' })
    showAddDialog.value = false
    await fetchSplits()
  } catch (err: any) {
    console.error('Failed to create split:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

function addParticipant(memberId: string, memberName: string) {
  if (formParticipants.value.find((p) => p.member_id === memberId)) {
    uni.showToast({ title: '该成员已添加', icon: 'none' })
    return
  }
  formParticipants.value.push({ member_id: memberId, member_name: memberName })
}

function removeParticipant(memberId: string) {
  formParticipants.value = formParticipants.value.filter((p) => p.member_id !== memberId)
}

async function markParticipantPaid(split: Split, participant: SplitParticipant) {
  uni.showModal({
    title: '确认支付',
    content: `确定要标记「${participant.member_name}」已支付吗？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/splits/${split.id}/participants/${participant.member_id}/pay`)
        uni.showToast({ title: '已标记为支付', icon: 'success' })
        await fetchSplits()
      } catch (err: any) {
        console.error('Failed to mark as paid:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

async function settleSplit(split: Split) {
  uni.showModal({
    title: '结清分账',
    content: `确定要结清分账「${split.title}」吗？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/splits/${split.id}/settle`)
        uni.showToast({ title: '已结清', icon: 'success' })
        await fetchSplits()
      } catch (err: any) {
        console.error('Failed to settle split:', err)
        uni.showToast({ title: err.message || '操作失败', icon: 'none' })
      }
    },
  })
}

// Computed: active and settled splits
const activeSplits = computed(() => splits.value.filter((s) => s.status === 'active'))
const settledSplits = computed(() => splits.value.filter((s) => s.status === 'settled'))

onMounted(() => {
  fetchSplits()
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
      <button @click="fetchSplits">重试</button>
    </view>

    <!-- Split list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="splits.length === 0" class="hc-empty">
        <text>暂无分账记录</text>
        <button class="hc-empty-btn" @click="openAddDialog">创建分账</button>
      </view>

      <!-- Active splits -->
      <view v-if="activeSplits.length > 0" class="section">
        <text class="section-title">进行中</text>
        <view v-for="split in activeSplits" :key="split.id" class="split-item">
          <view class="split-header">
            <text class="split-title">{{ split.title }}</text>
            <text class="split-amount">{{ formatAmount(split.total_amount_cents) }}</text>
          </view>

          <view v-if="split.payer_name" class="split-payer">
            <text>付款人: {{ split.payer_name }}</text>
          </view>

          <view v-if="split.description" class="split-desc">
            <text>{{ split.description }}</text>
          </view>

          <view class="split-participants">
            <text class="participants-label">参与者:</text>
            <view v-for="p in split.participants" :key="p.member_id" class="participant-row">
              <text class="participant-name">{{ p.member_name }}</text>
              <text class="participant-share">{{ formatAmount(p.share_cents) }}</text>
              <text class="participant-status" :class="p.is_paid ? 'status-paid' : 'status-unpaid'">
                {{ p.is_paid ? '已付' : '未付' }}
              </text>
              <text
                v-if="!p.is_paid"
                class="participant-action"
                @click="markParticipantPaid(split, p)"
              >
                标记支付
              </text>
            </view>
          </view>

          <view class="split-actions">
            <text class="split-action split-settle" @click="settleSplit(split)">结清</text>
          </view>
        </view>
      </view>

      <!-- Settled splits -->
      <view v-if="settledSplits.length > 0" class="section">
        <text class="section-title">已结清</text>
        <view v-for="split in settledSplits" :key="split.id" class="split-item split-settled">
          <view class="split-header">
            <text class="split-title">{{ split.title }}</text>
            <text class="split-badge">已结清</text>
          </view>
          <view class="split-summary">
            <text>总额: {{ formatAmount(split.total_amount_cents) }}</text>
            <text>参与者: {{ split.participants.length }}人</text>
          </view>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add split dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">创建分账</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">分账标题</text>
            <input v-model="formTitle" class="form-input" placeholder="例如：聚餐费用" />
          </view>

          <view class="form-row">
            <text class="form-label">总金额</text>
            <input v-model="formTotalAmount" class="form-input" type="digit" placeholder="0.00" />
          </view>

          <view class="form-row">
            <text class="form-label">付款人（可选）</text>
            <picker
              mode="selector"
              :range="familyMembers.map(m => m.name)"
              :value="familyMembers.findIndex(m => m.member_id === formPayerId)"
              @change="(e: any) => formPayerId = familyMembers[e.detail.value]?.member_id"
            >
              <view class="form-picker">
                <text>{{ familyMembers.find(m => m.member_id === formPayerId)?.name || '请选择' }}</text>
              </view>
            </picker>
          </view>

          <view class="form-row">
            <text class="form-label">参与者</text>
            <view class="participant-list">
              <view
                v-for="p in formParticipants"
                :key="p.member_id"
                class="participant-tag"
              >
                <text>{{ p.member_name }}</text>
                <text class="tag-remove" @click="removeParticipant(p.member_id)">×</text>
              </view>
            </view>
            <picker
              mode="selector"
              :range="familyMembers.filter(m => !formParticipants.find(p => p.member_id === m.member_id)).map(m => m.name)"
              @change="(e: any) => {
                const filtered = familyMembers.filter(m => !formParticipants.find(p => p.member_id === m.member_id))
                const selected = filtered[e.detail.value]
                if (selected) addParticipant(selected.member_id, selected.name)
              }"
            >
              <view class="form-picker">
                <text>添加参与者</text>
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

.split-item {
  padding: 24rpx;
  margin-bottom: 16rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.split-settled {
  opacity: 0.7;
}

.split-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.split-title {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.split-amount {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.split-badge {
  padding: 4rpx 12rpx;
  background-color: var(--color-success);
  color: var(--color-white);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.split-payer {
  margin-bottom: 8rpx;
  font-size: 24rpx;
  color: var(--text-secondary);
}

.split-desc {
  margin-bottom: 12rpx;
  font-size: 26rpx;
  color: var(--text-secondary);
  line-height: 1.5;
}

.split-participants {
  margin-bottom: 12rpx;
}

.participants-label {
  display: block;
  font-size: 24rpx;
  color: var(--text-tertiary);
  margin-bottom: 8rpx;
}

.participant-row {
  display: flex;
  align-items: center;
  gap: 12rpx;
  padding: 8rpx 0;
  border-bottom: 1rpx solid var(--divider-color);
}

.participant-row:last-child {
  border-bottom: none;
}

.participant-name {
  flex: 1;
  font-size: 26rpx;
  color: var(--text-primary);
}

.participant-share {
  font-size: 26rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.participant-status {
  font-size: 24rpx;
  padding: 4rpx 8rpx;
  border-radius: var(--radius-sm);
}

.status-paid {
  color: var(--color-success);
  background-color: var(--color-success-light);
}

.status-unpaid {
  color: var(--color-warning);
  background-color: var(--color-warning-light);
}

.participant-action {
  font-size: 24rpx;
  color: var(--color-primary);
}

.split-actions {
  display: flex;
  justify-content: flex-end;
}

.split-action {
  font-size: 26rpx;
}

.split-settle {
  color: var(--color-success);
}

.split-summary {
  display: flex;
  gap: 24rpx;
  font-size: 24rpx;
  color: var(--text-secondary);
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

.participant-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8rpx;
  margin-bottom: 12rpx;
}

.participant-tag {
  display: flex;
  align-items: center;
  gap: 8rpx;
  padding: 8rpx 16rpx;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  border-radius: var(--radius-sm);
  font-size: 24rpx;
}

.tag-remove {
  font-size: 28rpx;
  font-weight: bold;
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
