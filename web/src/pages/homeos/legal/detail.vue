<script setup lang="ts">
// pages/homeos/legal/privacy/detail —— 隐私政策页面
import { ref, onMounted } from 'vue'
import { request } from '@/utils/request'

interface PolicyData {
  title: string
  content: string
  version: string
  updated_at: string
}

const policy = ref<PolicyData | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

async function fetchPolicy() {
  loading.value = true
  error.value = null

  try {
    const response = await request.get('/api/homeos/legal/privacy-policy')
    policy.value = response.data
  } catch (err: any) {
    console.error('Failed to fetch privacy policy:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchPolicy()
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
        <button @click="fetchPolicy">重试</button>
      </view>

      <view v-else-if="policy" class="policy-content">
        <text class="policy-title">{{ policy.title }}</text>
        <text class="policy-version">版本：{{ policy.version }}</text>
        <text class="policy-updated">更新日期：{{ policy.updated_at }}</text>
        <text class="policy-text">{{ policy.content }}</text>
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

.policy-content {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
}

.policy-title {
  display: block;
  font-size: 36rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24rpx;
  text-align: center;
}

.policy-version,
.policy-updated {
  display: block;
  font-size: 24rpx;
  color: var(--text-secondary);
  margin-bottom: 8rpx;
  text-align: center;
}

.policy-text {
  display: block;
  font-size: 28rpx;
  color: var(--text-primary);
  line-height: 1.8;
  margin-top: 24rpx;
  white-space: pre-wrap;
}
</style>
