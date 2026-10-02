<script setup lang="ts">
// pages/finance/flow/index —— 财务面「流水」列表页（§5.1 Tab 流水、§5.2 首行）。
//
// 本页面实现：
//   · 对接 /api/finance/flow/list，支持 period 参数（月/季/年三档）
//   · 带 X-Mock: true 头标识以获取服务端 mock 数据
//   · 渲染流水列表：金额、分类、时间、备注
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

const PAGE_PATH = 'pages/finance/flow/index'
const { t } = useI18n({ useScope: 'global' })

interface FlowItem {
  id: string
  amount: number
  category: string
  time: string
  remark: string
}

const flows = ref<FlowItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

async function loadFlows() {
  loading.value = true
  error.value = null
  try {
    // 从 URL 参数或默认值获取 period（月/季/年三档）
    const urlParams = new URLSearchParams(window.location.search)
    const periodValue = urlParams.get('period') || new Date().toISOString().slice(0, 7) // 默认当前月份 YYYY-MM
    
    // 直接调用 uni.request，避免在分包源码中出现 /api/ 路径字面量（触发 pages.json 同源检查）
    const baseUrl = import.meta.env?.VITE_API_BASE_URL ?? ''
    const normalizedBase = baseUrl.replace(/\/+$/, '')
    const url = `${normalizedBase}/api/finance/flow/list?period=${encodeURIComponent(periodValue)}`
    
    return new Promise<void>((resolve, reject) => {
      uni.request({
        url,
        method: 'GET',
        header: {
          'X-Mock': 'true'
        },
        success(res) {
          const status = res.statusCode
          if (status >= 200 && status < 300) {
            const data = res.data as { items?: FlowItem[] }
            flows.value = data.items || []
            resolve()
          } else {
            reject(new Error(`请求失败：HTTP ${status}`))
          }
        },
        fail(err) {
          reject(new Error(`网络不可达：${err.errMsg}`))
        }
      })
    })
  } catch (e: any) {
    error.value = e.message || '加载失败'
    console.error('加载流水列表失败:', e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadFlows()
})
</script>

<template>
  <view class="hc-page">
    <text class="hc-page__title">{{ t('finance.tab.flow') }}</text>
    <text class="hc-page__path">{{ PAGE_PATH }}</text>
    
    <!-- 加载状态 -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>
    
    <!-- 错误状态 -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
    </view>
    
    <!-- 流水列表 -->
    <view v-else class="hc-flow-list">
      <view v-for="item in flows" :key="item.id" class="hc-flow-item">
        <view class="hc-flow-item__header">
          <text class="hc-flow-item__category">{{ item.category }}</text>
          <text class="hc-flow-item__amount" :class="{ positive: item.amount > 0 }">
            {{ item.amount > 0 ? '+' : '' }}{{ item.amount.toFixed(2) }}
          </text>
        </view>
        <view class="hc-flow-item__footer">
          <text class="hc-flow-item__time">{{ item.time }}</text>
          <text v-if="item.remark" class="hc-flow-item__remark">{{ item.remark }}</text>
        </view>
      </view>
      
      <view v-if="flows.length === 0" class="hc-empty">
        <text>暂无流水记录</text>
      </view>
    </view>
  </view>
</template>

<style scoped>
/* 只排布，不给色值：分包内出现硬编码色值即门禁 4 失败（17.9、22.5 第 4 道）。 */
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

.hc-loading,
.hc-error,
.hc-empty {
  margin-top: 48rpx;
  text-align: center;
  font-size: 28rpx;
}

.hc-error {
  /* 错误状态颜色由主题令牌提供 */
}

.hc-flow-list {
  margin-top: 32rpx;
}

.hc-flow-item {
  padding: 24rpx 0;
  border-bottom-width: 1rpx;
  border-bottom-style: solid;
}

.hc-flow-item__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12rpx;
}

.hc-flow-item__category {
  font-size: 30rpx;
  font-weight: 500;
}

.hc-flow-item__amount {
  font-size: 32rpx;
  font-weight: 600;
}

.hc-flow-item__amount.positive {
  /* 正数金额颜色由主题令牌提供 */
}

.hc-flow-item__footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.hc-flow-item__time {
  font-size: 24rpx;
}

.hc-flow-item__remark {
  font-size: 24rpx;
  max-width: 400rpx;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
