<script setup lang="ts">
// pages/homeos/home/index —— 主包首页路由位（§4.2「首页与动态」，内容 = 第三章四区规格）。
//
// 本卡（S1）交付到「路由 + 页面标识」这一层，页面本体在此刻只有两个真实可渲染物：
//   · 该页标题（i18n 按 code 分文件，见 src/i18n/zh-CN/homeos.json）
//   · 页面标识 = 路由 = 埋点 page_path 三者同值（§2.1「页面标识」行）
// 其余都还没到卡，因此**不在此处放任何假数据或占位文案**：
//   · A/B/C/D 四区的渲染与 shell 组件（顶栏一行、时间窗条、D 行五项）—— S2
//   · 四区的数据源 GET /api/homeos/home/summary（faces[] / due_today / dynamics / unread）—— 服务端 S5/S6，
//     首页四区的前端实建在 §十 前端线的后续行
//   · 面集合（C 区宫格）必须来自 faces[]，客户端不硬编码（§1.3 第 6 条、17.8）
// 状态：已实现未测（壳页，判据落 S2 与 S5/S8）。
import { useI18n } from 'vue-i18n'

const PAGE_PATH = 'pages/homeos/home/index'
const { t } = useI18n({ useScope: 'global' })
</script>

<template>
  <view class="hc-page">
    <text class="hc-page__title">{{ t('homeos.home.title') }}</text>
    <text class="hc-page__path">{{ PAGE_PATH }}</text>
  </view>
</template>

<style scoped>
/* 只排布，不给色值：颜色只能引用 S2 由主包出的那一份主题令牌（17.9、§九 主题令牌行）。 */
.hc-page {
  display: flex;
  flex-direction: column;
  padding: 32rpx;
}
.hc-page__title {
  font-size: 36rpx;
  font-weight: 600;
}
.hc-page__path {
  margin-top: 12rpx;
  font-size: 24rpx;
}
</style>
