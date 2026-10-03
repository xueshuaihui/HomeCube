<script setup lang="ts">
// pages/homeos/quick-add/index —— 全局「＋」快速添加页面
//
// 实现 PRD 17.4 规格：
//   · 四种输入形态：文字 / 语音 / 拍照 / 模板
//   · P1-P5 期间目标面由用户手选（仅本家庭已启用且服务已出生的面）
//   · 手选路径 ≤2 步
//   · 取消即返回进入前的页面
//   · 未提交的草稿不入任何业务对象

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { request } from '@/utils/request'

const { t } = useI18n({ useScope: 'global' })
const router = useRouter()

type InputMode = 'text' | 'voice' | 'photo' | 'template'
type FaceCode = 'finance' | 'purchase' | 'diet' | 'trip' | 'kin' | 'growth'

interface FaceInfo {
  code: FaceCode
  name: string
  enabled: boolean
}

interface QuickAddDraft {
  input_mode: InputMode
  content: string
  face_code?: FaceCode
  attachments?: string[]
}

const inputMode = ref<InputMode>('text')
const textInput = ref('')
const voiceRecording = ref(false)
const voiceDuration = ref(0)
const photoPaths = ref<string[]>([])
const selectedTemplate = ref('')
const selectedFace = ref<FaceCode | ''>('')
const availableFaces = ref<FaceInfo[]>([])
const submitting = ref(false)

// Computed: can submit
const canSubmit = computed(() => {
  if (!selectedFace.value) return false

  switch (inputMode.value) {
    case 'text':
      return textInput.value.trim().length > 0
    case 'voice':
      return voiceDuration.value > 0
    case 'photo':
      return photoPaths.value.length > 0
    case 'template':
      return selectedTemplate.value.length > 0
    default:
      return false
  }
})

// Load available faces (enabled and service born)
async function loadAvailableFaces() {
  try {
    // Fetch from home summary which contains faces[]
    const response = await request.get('/api/homeos/home/summary', {
      params: { period: 'month' },
    })

    availableFaces.value = (response.data.faces || []).map((f: any) => ({
      code: f.code,
      name: f.name,
      enabled: f.available,
    }))

    // Auto-select finance if only one face available
    if (availableFaces.value.length === 1) {
      selectedFace.value = availableFaces.value[0].code as FaceCode
    }
  } catch (err: any) {
    console.error('Failed to load available faces:', err)
    // Fallback to all faces
    availableFaces.value = [
      { code: 'finance', name: '财务', enabled: true },
      { code: 'purchase', name: '采购', enabled: true },
      { code: 'diet', name: '饮食', enabled: false },
      { code: 'trip', name: '出行', enabled: false },
      { code: 'kin', name: '家人', enabled: false },
      { code: 'growth', name: '成长', enabled: false },
    ]
  }
}

// Handle input mode change
function handleModeChange(mode: InputMode) {
  inputMode.value = mode
}

// Start/stop voice recording
function toggleVoiceRecording() {
  if (voiceRecording.value) {
    // Stop recording
    voiceRecording.value = false
    uni.showToast({ title: `录音 ${voiceDuration.value} 秒`, icon: 'none' })
  } else {
    // Start recording
    voiceRecording.value = true
    voiceDuration.value = 0

    // Simulate recording duration counter
    const timer = setInterval(() => {
      voiceDuration.value++
      if (voiceDuration.value >= 60) {
        clearInterval(timer)
        voiceRecording.value = false
      }
    }, 1000)

    // In real implementation, use uni.getRecorderManager()
    uni.showToast({ title: '开始录音...', icon: 'none' })
  }
}

// Take photo
function takePhoto() {
  uni.chooseImage({
    count: 1,
    sourceType: ['camera'],
    success: (res) => {
      photoPaths.value = res.tempFilePaths
      uni.showToast({ title: '拍照成功', icon: 'success' })
    },
    fail: () => {
      uni.showToast({ title: '拍照失败', icon: 'none' })
    },
  })
}

// Choose from album
function chooseFromAlbum() {
  uni.chooseImage({
    count: 1,
    sourceType: ['album'],
    success: (res) => {
      photoPaths.value = res.tempFilePaths
      uni.showToast({ title: '选择成功', icon: 'success' })
    },
  })
}

// Select template
function selectTemplate(template: string) {
  selectedTemplate.value = template
  textInput.value = template
}

// Submit quick add
async function handleSubmit() {
  if (!canSubmit.value) {
    uni.showToast({ title: '请填写完整信息', icon: 'none' })
    return
  }

  submitting.value = true

  try {
    const payload: any = {
      input_mode: inputMode.value,
      face_code: selectedFace.value,
      client_request_id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
    }

    switch (inputMode.value) {
      case 'text':
        payload.content = textInput.value.trim()
        break
      case 'voice':
        payload.duration_seconds = voiceDuration.value
        // TODO: Upload audio file and get URL
        break
      case 'photo':
        payload.attachments = photoPaths.value
        // TODO: Upload photos and get URLs
        break
      case 'template':
        payload.content = selectedTemplate.value
        payload.template_id = selectedTemplate.value
        break
    }

    // Route to appropriate service based on face_code
    let apiUrl = ''
    switch (selectedFace.value) {
      case 'finance':
        apiUrl = '/api/finance/transactions'
        // Transform to transaction format
        payload.type = 'expense'
        payload.amount_cents = 0 // TODO: Parse amount from content
        break
      case 'purchase':
        apiUrl = '/api/purchase/items'
        break
      default:
        // For other faces, save as todo in homeos
        apiUrl = '/api/homeos/todos'
        payload.title = payload.content || '快速添加'
        break
    }

    await request.post(apiUrl, payload)

    uni.showToast({ title: '添加成功', icon: 'success' })

    // Navigate back
    setTimeout(() => {
      router.back()
    }, 1500)
  } catch (err: any) {
    console.error('Failed to submit quick add:', err)
    uni.showToast({ title: err.message || '添加失败', icon: 'none' })
  } finally {
    submitting.value = false
  }
}

// Cancel and go back
function handleCancel() {
  router.back()
}

// Common templates
const templates = [
  '早餐',
  '午餐',
  '晚餐',
  '交通费',
  '购物',
  '娱乐',
  '医疗',
  '教育',
]

onMounted(() => {
  loadAvailableFaces()
})
</script>

<template>
  <view class="hc-page">
    <!-- Header -->
    <view class="page-header">
      <text class="header-title">快速添加</text>
      <text class="header-cancel" @click="handleCancel">取消</text>
    </view>

    <scroll-view class="hc-scroll" scroll-y>
      <!-- Face selector -->
      <view class="form-section">
        <text class="section-label">添加到</text>
        <view class="face-selector">
          <view
            v-for="face in availableFaces"
            :key="face.code"
            class="face-item"
            :class="{
              selected: selectedFace === face.code,
              disabled: !face.enabled,
            }"
            @click="face.enabled && (selectedFace = face.code as FaceCode)"
          >
            <text>{{ face.name }}</text>
            <text v-if="!face.enabled" class="face-badge">未启用</text>
          </view>
        </view>
      </view>

      <!-- Input mode selector -->
      <view class="form-section">
        <text class="section-label">输入方式</text>
        <view class="mode-selector">
          <view
            v-for="mode in ['text', 'voice', 'photo', 'template']"
            :key="mode"
            class="mode-item"
            :class="{ active: inputMode === mode }"
            @click="handleModeChange(mode as InputMode)"
          >
            <text class="mode-icon">{{
              mode === 'text' ? '✍️' : mode === 'voice' ? '🎤' : mode === 'photo' ? '📷' : '📋'
            }}</text>
            <text class="mode-label">{{ t(`quickadd.mode.${mode}`) }}</text>
          </view>
        </view>
      </view>

      <!-- Text input -->
      <view v-if="inputMode === 'text'" class="form-section">
        <text class="section-label">内容</text>
        <textarea
          v-model="textInput"
          class="text-input"
          placeholder="描述你要添加的内容..."
          maxlength="500"
        />
      </view>

      <!-- Voice input -->
      <view v-if="inputMode === 'voice'" class="form-section">
        <text class="section-label">语音输入</text>
        <view class="voice-recorder">
          <button
            class="record-btn"
            :class="{ recording: voiceRecording }"
            @click="toggleVoiceRecording"
          >
            <text>{{ voiceRecording ? '停止录音' : '按住说话' }}</text>
          </button>
          <text v-if="voiceDuration > 0" class="voice-duration">已录 {{ voiceDuration }} 秒</text>
        </view>
      </view>

      <!-- Photo input -->
      <view v-if="inputMode === 'photo'" class="form-section">
        <text class="section-label">拍照</text>
        <view class="photo-actions">
          <button class="photo-btn" @click="takePhoto">拍照</button>
          <button class="photo-btn" @click="chooseFromAlbum">从相册选择</button>
        </view>
        <view v-if="photoPaths.length > 0" class="photo-preview">
          <image
            v-for="(path, idx) in photoPaths"
            :key="idx"
            :src="path"
            mode="aspectFill"
            class="preview-img"
          />
        </view>
      </view>

      <!-- Template input -->
      <view v-if="inputMode === 'template'" class="form-section">
        <text class="section-label">常用模板</text>
        <view class="template-grid">
          <view
            v-for="tpl in templates"
            :key="tpl"
            class="template-item"
            :class="{ selected: selectedTemplate === tpl }"
            @click="selectTemplate(tpl)"
          >
            <text>{{ tpl }}</text>
          </view>
        </view>
      </view>
    </scroll-view>

    <!-- Submit button -->
    <view class="submit-bar">
      <button
        class="submit-btn"
        :disabled="!canSubmit || submitting"
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

/* Header */
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.header-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.header-cancel {
  font-size: 28rpx;
  color: var(--text-secondary);
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
  padding-bottom: 140rpx;
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

/* Face selector */
.face-selector {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16rpx;
}

.face-item {
  padding: 24rpx 16rpx;
  text-align: center;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-primary);
  position: relative;
}

.face-item.selected {
  background-color: var(--color-primary);
  color: #ffffff;
}

.face-item.disabled {
  opacity: 0.5;
}

.face-badge {
  display: block;
  font-size: 20rpx;
  color: var(--text-tertiary);
  margin-top: 4rpx;
}

.face-item.selected .face-badge {
  color: rgba(255, 255, 255, 0.8);
}

/* Mode selector */
.mode-selector {
  display: flex;
  gap: 16rpx;
}

.mode-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8rpx;
  padding: 24rpx 8rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
}

.mode-item.active {
  background-color: var(--color-primary-light);
  color: #ffffff;
}

.mode-icon {
  font-size: 40rpx;
}

.mode-label {
  font-size: 24rpx;
}

/* Text input */
.text-input {
  width: 100%;
  min-height: 200rpx;
  padding: 16rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

/* Voice recorder */
.voice-recorder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16rpx;
  padding: 48rpx 0;
}

.record-btn {
  width: 240rpx;
  height: 240rpx;
  border-radius: 50%;
  background-color: var(--color-primary);
  color: #ffffff;
  font-size: 32rpx;
  display: flex;
  align-items: center;
  justify-content: center;
}

.record-btn.recording {
  background-color: var(--color-error);
  animation: pulse 1s infinite;
}

@keyframes pulse {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.05); }
}

.voice-duration {
  font-size: 28rpx;
  color: var(--text-secondary);
}

/* Photo actions */
.photo-actions {
  display: flex;
  gap: 16rpx;
  margin-bottom: 24rpx;
}

.photo-btn {
  flex: 1;
  padding: 24rpx 0;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.photo-preview {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16rpx;
}

.preview-img {
  aspect-ratio: 1;
  border-radius: var(--radius-sm);
}

/* Template grid */
.template-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16rpx;
}

.template-item {
  padding: 24rpx 8rpx;
  text-align: center;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-primary);
}

.template-item.selected {
  background-color: var(--color-primary);
  color: #ffffff;
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
  color: #ffffff;
  border-radius: var(--radius-md);
  font-size: 32rpx;
  font-weight: 600;
}

.submit-btn:disabled {
  opacity: 0.6;
}
</style>
