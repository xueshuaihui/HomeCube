<script setup lang="ts">
// pages/homeos/legal/detail —— 法律文本页面（隐私政策/用户协议）
// 通过查询参数 type=privacy 或 type=agreement 区分
import { ref, computed, onMounted } from 'vue'
import { request } from '@/utils/request'

interface LegalData {
  title: string
  content: string
  version: string
  updated_at: string
}

const data = ref<LegalData | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

// Get type from URL query params
const type = computed(() => {
  // In uni-app, get query params from current page options
  const pages = getCurrentPages()
  const currentPage = pages[pages.length - 1] as any
  return currentPage.options?.type || 'privacy'
})

const pageTitle = computed(() => type.value === 'agreement' ? '用户协议' : '隐私政策')

async function fetchData() {
  loading.value = true
  error.value = null

  try {
    const endpoint = type.value === 'agreement'
      ? '/api/homeos/legal/user-agreement'
      : '/api/homeos/legal/privacy-policy'

    const response = await request.get<LegalData>(endpoint)
    // 请求层 resolve 的就是裸响应体（svc-homeos `GetPrivacyPolicy` / `GetUserAgreement`
    // 直接 `c.JSON(200, gin.H{"title","content","version","updated_at"})`），不再 `.data`。
    data.value = response
  } catch (err: any) {
    console.error('Failed to fetch legal document:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchData()
})
</script>

<template>
  <view class="hc-page">
    <scroll-view class="hc-scroll" scroll-y>
      <view v-if="loading" class="hc-loading">
        <text>加载中...</text>
      </view>

      <view v-else-if="error" class="hc-error">
        <text>{{ error }}</text>
        <button @click="fetchData">重试</button>
      </view>

      <view v-else-if="data" class="legal-content">
        <text class="legal-title">{{ data.title }}</text>
        <text class="legal-version">版本：{{ data.version }}</text>
        <text class="legal-updated">更新日期：{{ data.updated_at }}</text>
        <text class="legal-text">{{ data.content }}</text>
      </view>
    </scroll-view>
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
  padding: 32rpx;
}

.hc-loading,
.hc-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 96rpx 48rpx;
  gap: 24rpx;
}

.hc-error button {
  margin-top: 16rpx;
  padding: 16rpx 48rpx;
  background-color: var(--color-primary);
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.legal-content {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
}

.legal-title {
  display: block;
  font-size: 36rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24rpx;
  text-align: center;
}

.legal-version,
.legal-updated {
  display: block;
  font-size: 24rpx;
  color: var(--text-secondary);
  margin-bottom: 8rpx;
  text-align: center;
}

.legal-text {
  display: block;
  font-size: 28rpx;
  color: var(--text-primary);
  line-height: 1.8;
  margin-top: 24rpx;
  white-space: pre-wrap;
}
</style>
