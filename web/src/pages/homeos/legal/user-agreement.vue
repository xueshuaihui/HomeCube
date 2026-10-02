<template>
  <view class="container">
    <view class="header">
      <text class="title">用户协议</text>
    </view>
    <view class="content">
      <text class="placeholder-text">【占位】本文档尚未定版，商用前将由法务部门提供正式文本。</text>
      <text class="version-info">当前版本：P1-M2 收口期</text>
      <text class="update-info">最后更新：2026-10-03</text>
    </view>
  </view>
</template>

<script>
export default {
  data() {
    return {}
  },
  onLoad() {
    // Placeholder page - no logic needed yet
  }
}
</script>

<style scoped>
.container {
  padding: 40rpx;
  min-height: 100vh;
  background-color: #f5f5f5;
}

.header {
  margin-bottom: 40rpx;
  text-align: center;
}

.title {
  font-size: 48rpx;
  font-weight: bold;
  color: #333;
}

.content {
  background-color: #fff;
  padding: 40rpx;
  border-radius: 16rpx;
  box-shadow: 0 2rpx 8rpx rgba(0, 0, 0, 0.1);
}

.placeholder-text {
  font-size: 32rpx;
  color: #666;
  line-height: 1.8;
  display: block;
  margin-bottom: 20rpx;
}

.version-info,
.update-info {
  font-size: 28rpx;
  color: #999;
  display: block;
  margin-top: 10rpx;
}
</style>
