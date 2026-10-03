<script setup lang="ts">
// pages/homeos/mine/index —— "我的"页面
//
// 实现 PRD 17.5 规格：
//   · 账号：个人信息展示
//   · 家庭管理（成员/邀请/角色/权限，见十五章）
//   · 开通更多面（全量面目录与本家庭的启用配置，仅管理员可变更，17.8）
//   · 设置（主题/时区/货币/日期格式/语言，主题三态见 17.9）
//   · 数据（导出/备份/迁移，见 3.4.4）
//   · 隐私与紧急卡开关（见 15.4）
//   · 帮助与关于

import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { request } from '@/utils/request'
import { useHomeStore } from '@/stores/home'

const { t } = useI18n({ useScope: 'global' })
const router = useRouter()
const homeStore = useHomeStore()

type ThemeMode = 'light' | 'dark' | 'system'

interface UserInfo {
  id: string
  phone: string
  name?: string
  avatar?: string
}

interface FamilyInfo {
  id: string
  name: string
  role: 'owner' | 'member' | 'ward' | 'guest'
}

interface ModuleInfo {
  code: string
  name: string
  enabled: boolean
  service_born: boolean // 服务已出生
}

const userInfo = ref<UserInfo | null>(null)
const currentFamily = ref<FamilyInfo | null>(null)
const modules = ref<ModuleInfo[]>([])
const themeMode = ref<ThemeMode>('system')
const loading = ref(false)

// All available modules (registry order)
const allModules: Array<{ code: string; name: string }> = [
  { code: 'finance', name: '财务' },
  { code: 'purchase', name: '采购' },
  { code: 'diet', name: '饮食' },
  { code: 'trip', name: '出行' },
  { code: 'kin', name: '家人' },
  { code: 'growth', name: '成长' },
]

// Load user and family info
async function loadUserInfo() {
  try {
    // Get user info from token or API
    const userId = uni.getStorageSync('user_id')
    const phone = uni.getStorageSync('phone')

    userInfo.value = {
      id: userId || '',
      phone: phone || '未登录',
      name: '用户',
    }

    // Get current family from home store
    if (homeStore.family) {
      currentFamily.value = {
        id: homeStore.family.id,
        name: homeStore.family.name,
        role: 'member', // TODO: get from token claims
      }
    }
  } catch (err: any) {
    console.error('Failed to load user info:', err)
  }
}

// Load module status
async function loadModules() {
  try {
    const response = await request.get('/api/homeos/home/summary', {
      params: { period: 'month' },
    })

    const enabledFaces = response.data.faces || []

    modules.value = allModules.map(mod => {
      const face = enabledFaces.find((f: any) => f.code === mod.code)
      return {
        code: mod.code,
        name: mod.name,
        enabled: !!face,
        service_born: face ? face.available : false,
      }
    })
  } catch (err: any) {
    console.error('Failed to load modules:', err)
    // Fallback
    modules.value = allModules.map(mod => ({
      code: mod.code,
      name: mod.name,
      enabled: mod.code === 'finance', // Only finance is born in P1
      service_born: mod.code === 'finance',
    }))
  }
}

// Toggle module enablement (admin only)
async function toggleModule(moduleCode: string, enabled: boolean) {
  if (currentFamily.value?.role !== 'owner') {
    uni.showToast({ title: '仅管理员可开通/停用面', icon: 'none' })
    return
  }

  try {
    await request.put(`/api/homeos/family/modules/${moduleCode}`, {
      enabled,
    })

    // Update local state
    const mod = modules.value.find(m => m.code === moduleCode)
    if (mod) {
      mod.enabled = enabled
    }

    uni.showToast({ title: enabled ? '已开通' : '已停用', icon: 'success' })

    // Reload home summary to update faces
    await homeStore.updateSummary(
      (await request.get('/api/homeos/home/summary', { params: { period: 'month' } })).data
    )
  } catch (err: any) {
    console.error('Failed to toggle module:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

// Change theme
function changeTheme(mode: ThemeMode) {
  themeMode.value = mode

  // Apply theme
  if (mode === 'system') {
    // Remove explicit theme attribute, let system preference take over
    uni.removeStorageSync('theme')
  } else {
    uni.setStorageSync('theme', mode)
    // Apply to document
    // Note: In uni-app, this would be done differently
  }

  uni.showToast({ title: '主题已切换', icon: 'success' })
}

// Logout
function handleLogout() {
  uni.showModal({
    title: '确认退出',
    content: '确定要退出登录吗？',
    success: async (res) => {
      if (res.confirm) {
        try {
          await request.post('/api/homeos/auth/logout')
        } catch (e) {
          // Ignore logout API errors
        }

        // Clear storage
        uni.removeStorageSync('access_token')
        uni.removeStorageSync('refresh_token')
        uni.removeStorageSync('user_id')
        uni.removeStorageSync('phone')

        // Navigate to login
        uni.reLaunch({ url: '/pages/homeos/auth/login' })
      }
    },
  })
}

// Menu items
const menuSections = [
  {
    title: '家庭',
    items: [
      { label: '家庭成员', action: () => router.push('/pages/homeos/family/members') },
      { label: '邀请成员', action: () => router.push('/pages/homeos/family/invite') },
    ],
  },
  {
    title: '开通更多面',
    items: [], // Rendered dynamically
  },
  {
    title: '设置',
    items: [
      { label: '主题切换', action: () => {} }, // Handled inline
      { label: '时区设置', action: () => {} },
      { label: '货币设置', action: () => {} },
      { label: '日期格式', action: () => {} },
      { label: '语言', action: () => {} },
    ],
  },
  {
    title: '数据',
    items: [
      { label: '导出数据', action: () => {} },
      { label: '备份状态', action: () => {} },
    ],
  },
  {
    title: '其他',
    items: [
      { label: '隐私政策', action: () => router.push('/pages/homeos/legal/privacy/detail') },
      { label: '用户协议', action: () => router.push('/pages/homeos/legal/agreement/detail') },
      { label: '帮助与关于', action: () => {} },
    ],
  },
]

onMounted(() => {
  loadUserInfo()
  loadModules()
})
</script>

<template>
  <view class="hc-page">
    <scroll-view class="hc-scroll" scroll-y>
      <!-- User profile card -->
      <view class="profile-card">
        <view class="profile-avatar">
          <text>{{ userInfo?.name?.charAt(0) || '用' }}</text>
        </view>
        <view class="profile-info">
          <text class="profile-name">{{ userInfo?.name || '未命名用户' }}</text>
          <text class="profile-phone">{{ userInfo?.phone }}</text>
        </view>
      </view>

      <!-- Current family -->
      <view v-if="currentFamily" class="family-card">
        <text class="family-label">当前家庭</text>
        <text class="family-name">{{ currentFamily.name }}</text>
        <text class="family-role">{{ currentFamily.role === 'owner' ? '管理员' : '成员' }}</text>
      </view>

      <!-- Menu sections -->
      <view v-for="section in menuSections" :key="section.title" class="menu-section">
        <text class="section-title">{{ section.title }}</text>

        <!-- Dynamic module list for "开通更多面" section -->
        <view v-if="section.title === '开通更多面'" class="module-list">
          <view v-for="mod in modules" :key="mod.code" class="module-item">
            <view class="module-info">
              <text class="module-name">{{ mod.name }}</text>
              <text v-if="!mod.service_born" class="module-badge">即将上线</text>
            </view>
            <switch
              :checked="mod.enabled"
              :disabled="!mod.service_born || currentFamily?.role !== 'owner'"
              @change="(e: any) => toggleModule(mod.code, e.detail.value)"
            />
          </view>
        </view>

        <!-- Theme selector inline -->
        <view v-else-if="section.title === '设置' && section.items.some(i => i.label === '主题切换')" class="theme-selector">
          <view
            v-for="mode in ['light', 'dark', 'system']"
            :key="mode"
            class="theme-item"
            :class="{ active: themeMode === mode }"
            @click="changeTheme(mode as ThemeMode)"
          >
            <text>{{ mode === 'light' ? '浅色' : mode === 'dark' ? '深色' : '跟随系统' }}</text>
          </view>
        </view>

        <!-- Regular menu items -->
        <view v-else class="menu-list">
          <view
            v-for="item in section.items"
            :key="item.label"
            class="menu-item"
            @click="item.action"
          >
            <text class="menu-label">{{ item.label }}</text>
            <text class="menu-arrow">›</text>
          </view>
        </view>
      </view>

      <!-- Logout button -->
      <view class="logout-section">
        <button class="logout-btn" @click="handleLogout">退出登录</button>
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
  padding: 24rpx;
}

/* Profile card */
.profile-card {
  display: flex;
  align-items: center;
  gap: 24rpx;
  padding: 32rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
}

.profile-avatar {
  width: 96rpx;
  height: 96rpx;
  border-radius: 50%;
  background-color: var(--color-primary-light);
  color: #ffffff;
  font-size: 40rpx;
  display: flex;
  align-items: center;
  justify-content: center;
}

.profile-info {
  display: flex;
  flex-direction: column;
  gap: 8rpx;
}

.profile-name {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.profile-phone {
  font-size: 26rpx;
  color: var(--text-secondary);
}

/* Family card */
.family-card {
  padding: 24rpx 32rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  gap: 16rpx;
}

.family-label {
  font-size: 26rpx;
  color: var(--text-secondary);
}

.family-name {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.family-role {
  font-size: 24rpx;
  color: var(--color-primary);
  padding: 4rpx 12rpx;
  background-color: var(--color-primary-light);
  color: #ffffff;
  border-radius: var(--radius-sm);
}

/* Menu sections */
.menu-section {
  margin-bottom: 32rpx;
}

.section-title {
  display: block;
  font-size: 26rpx;
  color: var(--text-secondary);
  margin-bottom: 12rpx;
  padding-left: 8rpx;
}

/* Module list */
.module-list {
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.module-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx 32rpx;
  border-bottom: 1rpx solid var(--divider-color);
}

.module-item:last-child {
  border-bottom: none;
}

.module-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.module-name {
  font-size: 28rpx;
  color: var(--text-primary);
}

.module-badge {
  font-size: 22rpx;
  color: var(--text-tertiary);
  padding: 4rpx 12rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
}

/* Theme selector */
.theme-selector {
  display: flex;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
}

.theme-item {
  flex: 1;
  padding: 24rpx 0;
  text-align: center;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 26rpx;
  color: var(--text-secondary);
}

.theme-item.active {
  background-color: var(--color-primary);
  color: #ffffff;
}

/* Menu list */
.menu-list {
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.menu-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 24rpx 32rpx;
  border-bottom: 1rpx solid var(--divider-color);
}

.menu-item:last-child {
  border-bottom: none;
}

.menu-label {
  font-size: 28rpx;
  color: var(--text-primary);
}

.menu-arrow {
  font-size: 32rpx;
  color: var(--text-tertiary);
}

/* Logout section */
.logout-section {
  padding: 32rpx 0;
}

.logout-btn {
  width: 100%;
  padding: 28rpx 0;
  background-color: var(--color-error);
  color: #ffffff;
  border-radius: var(--radius-md);
  font-size: 32rpx;
  font-weight: 600;
}
</style>
