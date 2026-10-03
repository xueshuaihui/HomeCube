<script setup lang="ts">
// pages/homeos/legal/agreement —— 用户协议页面
import { ref, onMounted } from 'vue'
import { request } from '@/utils/request'

interface AgreementData {
  title: string
  content: string
  version: string
  updated_at: string
}

const agreement = ref<AgreementData | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

async function fetchAgreement() {
  loading.value = true
  error.value = null

  try {
    const response = await request.get('/api/homeos/legal/user-agreement')
    agreement.value = response.data
  } catch (err: any) {
    console.error('Failed to fetch user agreement:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchAgreement()
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
        <button @click="fetchAgreement">重试</button>
      </view>

      <view v-else-if="agreement" class="agreement-content">
        <text class="agreement-title">{{ agreement.title }}</text>
        <text class="agreement-version">版本：{{ agreement.version }}</text>
        <text class="agreement-updated">更新日期：{{ agreement.updated_at }}</text>
        <text class="agreement-text">{{ agreement.content }}</text>
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

.agreement-content {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
}

.agreement-title {
  display: block;
  font-size: 36rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24rpx;
  text-align: center;
}

.agreement-version,
.agreement-updated {
  display: block;
  font-size: 24rpx;
  color: var(--text-secondary);
  margin-bottom: 8rpx;
  text-align: center;
}

.agreement-text {
  display: block;
  font-size: 28rpx;
  color: var(--text-primary);
  line-height: 1.8;
  margin-top: 24rpx;
  white-space: pre-wrap;
}
</style>
