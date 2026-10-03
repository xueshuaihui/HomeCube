<script setup lang="ts">
// pages/homeos/mine/index —— "我的"页面
import { useI18n } from 'vue-i18n'

const { t } = useI18n({ useScope: 'global' })
</script>

<template>
  <view class="hc-page">
    <text class="hc-page__title">{{ t('homeos.nav.mine') }}</text>
    <text class="hc-placeholder">"我的"页面 - 开发中</text>
  </view>
</template>

<style scoped>
.hc-page {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48rpx;
  min-height: 100vh;
}

.hc-page__title {
  font-size: 36rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 24rpx;
}

.hc-placeholder {
  font-size: 28rpx;
  color: var(--text-tertiary);
}
</style>
