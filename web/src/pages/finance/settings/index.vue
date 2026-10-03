<script setup lang="ts">
// pages/finance/settings/index —— 财务设置页（PRD 4.6 财务面内二级 Tab）
//
// 实现：
//   · 基础设置：默认账本、货币单位、小数位数
//   · 分类管理入口：跳转到分类管理页
//   · 账户管理入口：跳转到账户管理页
//   · 数据导出：导出财务数据
//   · 高级设置：预算预警阈值、自动记账开关等
//   · 对接 GET /api/finance/settings?family_id=
//     PUT /api/finance/settings

import { ref, onMounted } from 'vue'
import { request } from '@/utils/request'
import { useHomeStore } from '@/stores/home'

interface FinanceSettings {
  family_id: string
  default_ledger_id?: string
  currency_unit: string
  decimal_places: number
  budget_alert_threshold: number
  auto_categorize_enabled: boolean
  receipt_ocr_enabled: boolean
  voice_input_enabled: boolean
  updated_at: string
}

const settings = ref<FinanceSettings | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const saving = ref(false)

const homeStore = useHomeStore()

async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

async function fetchSettings() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    const body = await request.get<FinanceSettings>('/api/finance/settings', {
      params: { family_id: familyId },
    })
    settings.value = body
  } catch (err: any) {
    console.error('Failed to fetch settings:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function saveSetting(key: string, value: any) {
  if (!settings.value) return

  saving.value = true
  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      uni.showToast({ title: '缺少家庭信息', icon: 'none' })
      return
    }

    await request.put('/api/finance/settings', {
      family_id: familyId,
      [key]: value,
    })

    // 更新本地状态
    if (settings.value) {
      (settings.value as any)[key] = value
    }

    uni.showToast({ title: '保存成功', icon: 'success' })
  } catch (err: any) {
    console.error('Failed to save setting:', err)
    uni.showToast({ title: err.message || '保存失败', icon: 'none' })
  } finally {
    saving.value = false
  }
}

function goToCategories() {
  uni.navigateTo({ url: '/pages/finance/category/index' })
}

function goToAccounts() {
  uni.navigateTo({ url: '/pages/finance/account/index' })
}

function exportData() {
  uni.showActionSheet({
    itemList: ['导出为 CSV', '导出为 Excel'],
    success: async (res) => {
      const format = res.tapIndex === 0 ? 'csv' : 'excel'
      try {
        const familyId = await resolveSessionFamilyId()
        if (!familyId) {
          uni.showToast({ title: '缺少家庭信息', icon: 'none' })
          return
        }

        // 调用导出接口
        uni.showToast({ title: '正在生成导出文件...', icon: 'loading' })
        // TODO: 实际实现需要处理文件下载
        uni.showToast({ title: '导出功能开发中', icon: 'none' })
      } catch (err: any) {
        console.error('Failed to export data:', err)
        uni.showToast({ title: err.message || '导出失败', icon: 'none' })
      }
    },
  })
}

onMounted(() => {
  fetchSettings()
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
      <button @click="fetchSettings">重试</button>
    </view>

    <!-- Settings content -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <!-- Basic settings -->
      <view class="section">
        <text class="section-title">基础设置</text>

        <view class="setting-item">
          <text class="setting-label">货币单位</text>
          <picker
            mode="selector"
            :range="['CNY (¥)', 'USD ($)', 'EUR (€)', 'JPY (¥)']"
            :value="settings?.currency_unit === 'CNY' ? 0 : settings?.currency_unit === 'USD' ? 1 : settings?.currency_unit === 'EUR' ? 2 : 3"
            @change="(e: any) => {
              const units = ['CNY', 'USD', 'EUR', 'JPY']
              saveSetting('currency_unit', units[e.detail.value])
            }"
          >
            <view class="setting-value">
              <text>{{ settings?.currency_unit === 'CNY' ? 'CNY (¥)' : settings?.currency_unit === 'USD' ? 'USD ($)' : settings?.currency_unit === 'EUR' ? 'EUR (€)' : 'JPY (¥)' }}</text>
            </view>
          </picker>
        </view>

        <view class="setting-item">
          <text class="setting-label">小数位数</text>
          <picker
            mode="selector"
            :range="['0', '1', '2']"
            :value="settings?.decimal_places ?? 2"
            @change="(e: any) => saveSetting('decimal_places', Number.parseInt(e.detail.value))"
          >
            <view class="setting-value">
              <text>{{ settings?.decimal_places ?? 2 }}</text>
            </view>
          </picker>
        </view>
      </view>

      <!-- Management entries -->
      <view class="section">
        <text class="section-title">管理入口</text>

        <view class="setting-item setting-link" @click="goToCategories">
          <text class="setting-label">分类管理</text>
          <text class="setting-arrow">›</text>
        </view>

        <view class="setting-item setting-link" @click="goToAccounts">
          <text class="setting-label">账户管理</text>
          <text class="setting-arrow">›</text>
        </view>
      </view>

      <!-- Alert settings -->
      <view class="section">
        <text class="section-title">预警设置</text>

        <view class="setting-item">
          <text class="setting-label">预算预警阈值 (%)</text>
          <picker
            mode="selector"
            :range="['50', '60', '70', '80', '90']"
            :value="[50, 60, 70, 80, 90].indexOf(settings?.budget_alert_threshold ?? 80)"
            @change="(e: any) => {
              const thresholds = [50, 60, 70, 80, 90]
              saveSetting('budget_alert_threshold', thresholds[e.detail.value])
            }"
          >
            <view class="setting-value">
              <text>{{ settings?.budget_alert_threshold ?? 80 }}%</text>
            </view>
          </picker>
        </view>
      </view>

      <!-- Feature toggles -->
      <view class="section">
        <text class="section-title">功能开关</text>

        <view class="setting-item">
          <text class="setting-label">自动分类</text>
          <switch
            :checked="settings?.auto_categorize_enabled ?? false"
            @change="(e: any) => saveSetting('auto_categorize_enabled', e.detail.value)"
            color="var(--color-primary)"
          />
        </view>

        <view class="setting-item">
          <text class="setting-label">小票 OCR</text>
          <switch
            :checked="settings?.receipt_ocr_enabled ?? false"
            @change="(e: any) => saveSetting('receipt_ocr_enabled', e.detail.value)"
            color="var(--color-primary)"
          />
        </view>

        <view class="setting-item">
          <text class="setting-label">语音输入</text>
          <switch
            :checked="settings?.voice_input_enabled ?? false"
            @change="(e: any) => saveSetting('voice_input_enabled', e.detail.value)"
            color="var(--color-primary)"
          />
        </view>
      </view>

      <!-- Data export -->
      <view class="section">
        <text class="section-title">数据导出</text>

        <view class="setting-item setting-link" @click="exportData">
          <text class="setting-label">导出财务数据</text>
          <text class="setting-arrow">›</text>
        </view>
      </view>

      <!-- About -->
      <view class="section">
        <text class="section-title">关于</text>

        <view class="about-item">
          <text class="about-label">版本</text>
          <text class="about-value">1.0.0</text>
        </view>
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

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

.section {
  margin-bottom: 32rpx;
}

.section-title {
  display: block;
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 16rpx;
  padding-left: 8rpx;
}

.setting-item {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-sm);
  margin-bottom: 8rpx;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.setting-link {
  cursor: pointer;
}

.setting-link:active {
  background-color: var(--bg-tertiary);
}

.setting-label {
  font-size: 28rpx;
  color: var(--text-primary);
}

.setting-value {
  font-size: 28rpx;
  color: var(--text-secondary);
}

.setting-arrow {
  font-size: 32rpx;
  color: var(--text-tertiary);
}

.about-item {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-sm);
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.about-label {
  font-size: 28rpx;
  color: var(--text-primary);
}

.about-value {
  font-size: 28rpx;
  color: var(--text-secondary);
}
</style>
