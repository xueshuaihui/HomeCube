<template>
  <view class="create-family-page">
    <view class="page-header">
      <text class="title">创建家庭</text>
      <text class="subtitle">开始记录您家庭的温暖时光</text>
    </view>
    
    <view class="form-container">
      <view class="input-group">
        <text class="label">家庭名称</text>
        <input 
          v-model="familyName" 
          placeholder="例如：幸福小家" 
          class="input-field"
        />
      </view>
      
      <view class="input-group">
        <text class="label">时区</text>
        <picker @change="onTimezoneChange" :value="timezoneIndex" :range="timezoneList">
          <view class="picker-field">
            <text>{{ selectedTimezone }}</text>
            <text class="arrow">▼</text>
          </view>
        </picker>
      </view>
      
      <view class="input-group">
        <text class="label">货币</text>
        <picker @change="onCurrencyChange" :value="currencyIndex" :range="currencyList">
          <view class="picker-field">
            <text>{{ selectedCurrency }}</text>
            <text class="arrow">▼</text>
          </view>
        </picker>
      </view>
      
      <button @click="handleCreate" :disabled="submitting" class="create-btn">创建家庭</button>
    </view>
  </view>
</template>

<script setup lang="ts">
// pages/homeos/family/create —— 建家引导第①步（§4.1 表 ① 行、PRD 3.4.1、契约 `POST /families`）
//
// 三条口径，全部与 auth/login.vue 同源：
//   1. **请求走 `utils/request`**：本页此前直连 `uni.request`，于是拿不到 401 刷新重试、
//      Authorization 头还得自己拼。现在只有 `request.post` 一条路，token 的读与写只剩
//      `access_token` / `refresh_token` 两个键（就是 request.ts 读写的那两个键）。
//   2. **2xx 的回执必须落盘**：`POST /families` 是自举端点 —— 进这一扇门时手里只有
//      onboarding 作用域的 token（`scope:"onboarding"`、`family_id:null`，中间件对它的放行
//      清单只有 `POST /families`、`/auth/refresh`、`/auth/logout`、`GET /auth/me`）。
//      svc-homeos 的 `CreateFamilyResponse`（handler family.go:132）因此把**新家庭那一对**
//      token 一起回给你。不落盘就跳转，引导第②步的第一个 `GET /family/modules` 带的还是
//      onboarding token ⇒ 403 `insufficient_scope`，强制选面这一步就无从做起。
//   3. **建家成功 = 强制选面还没做完 = 换到引导第②步**（§4.1 表 ① 行「提交后 `redirectTo`
//      `homeos/family/modules` 的引导态完成强制选面」、§6.11 第一条流）：本页此前 `reLaunch` 进首页，
//      于是新建的家庭带着 0 面直接落在首页空矩阵上，17.8 的强制选面永远没被兑现。
//      `redirectTo` 而不是 `navigateTo`：这一步不可跳过、也不该留在栈里被返回手势退回一张已提交的表单
//      （§2.3「一级页 → 二级页压栈」讲的是常规二级页，创建链这一跳是文档点名的重置式替换）。
//      顺序恒为「写 token → 重建会话快照 → 跳转」，不可颠倒。

import { ref, computed } from 'vue'
import { request, unwrapBody, RequestError } from '@/utils/request'
import { useHomeStore } from '@/stores/home'

/** 2xx 体 = svc-homeos 的 `CreateFamilyResponse`；只取它登记了的那几个字段，不猜额外的。 */
interface CreateFamilyResponse {
  id?: string
  family_id?: string
  name?: string
  role?: string
  pver?: number
  access_token?: string
  refresh_token?: string
  expires_in?: number
  message?: string
}

const homeStore = useHomeStore()

const familyName = ref('')
const timezoneList = ['Asia/Shanghai (UTC+8)', 'America/New_York (UTC-5)', 'Europe/London (UTC+0)']
const timezoneIndex = ref(0)
const currencyList = ['CNY (人民币)', 'USD (美元)', 'EUR (欧元)', 'JPY (日元)']
const currencyIndex = ref(0)
/** 提交锁：POST 回来到 redirectTo 生效之间还有「写 token + 重建快照」这一段异步窗口，这期间再点一次就是第二个家庭。 */
const submitting = ref(false)

const selectedTimezone = computed(() => timezoneList[timezoneIndex.value])
const selectedCurrency = computed(() => currencyList[currencyIndex.value])

function onTimezoneChange(e: any) {
  timezoneIndex.value = Number(e.detail.value)
}

function onCurrencyChange(e: any) {
  currencyIndex.value = Number(e.detail.value)
}

/** 失败文案：优先服务端 message/error，其次网络层原因（同 login.vue 的 describeError 口径）。 */
function describeError(err: unknown): string {
  if (err instanceof RequestError) {
    const body = err.body as { message?: string; error?: string } | undefined
    return body?.message || body?.error || err.message || '创建失败'
  }
  return (err as Error)?.message || '网络错误'
}

async function handleCreate() {
  if (!familyName.value.trim()) {
    uni.showToast({ title: '请输入家庭名称', icon: 'none' })
    return
  }
  // 建家要有 onboarding 会话；存储键就是 request.ts 每次读的那个 `access_token`。
  if (!uni.getStorageSync('access_token')) {
    uni.redirectTo({ url: '/pages/homeos/auth/login' })
    return
  }
  if (submitting.value) return
  submitting.value = true

  try {
    // 入参字段名按 svc-homeos 的 `CreateFamilyRequest`；Authorization 头由请求层拼。
    const res = await request.post<unknown>('/api/homeos/families', {
      name: familyName.value.trim(),
      timezone: selectedTimezone.value.split(' ')[0],
      currency: selectedCurrency.value.split(' ')[0],
    })
    const body = unwrapBody<CreateFamilyResponse>(res)

    if (!body?.access_token) {
      // 成功却没有家庭态 token（旧服务端构建 / 契约变更）：不静默进首页 ——
      // 「带着 onboarding token 落在首页」正是第 2 条要关的缺陷本身，不是可接受的回落。
      submitting.value = false
      uni.showToast({ title: '家庭已创建，但未取得家庭会话，请重新登录', icon: 'none' })
      return
    }

    // 单点存储口径：键名与 utils/request.ts 读写的完全一致（与 login.vue、family-join.vue 同一套）。
    // `expires_in` 不落盘 —— 请求层没有过期键，多写一个没人读的键就是第二套约定；
    // 过期处置走它已有的那条路：401 → /auth/refresh → 回写同两个键。
    uni.setStorageSync('access_token', body.access_token)
    if (body.refresh_token) uni.setStorageSync('refresh_token', body.refresh_token)

    // 会话快照跟着换：建家前这份快照里家庭与角色都是空的（onboarding 作用域没有家庭），
    // 首页与治理页读的就是这一份（stores/home.ts 的 `sessionFamilyId` / `role`）。
    homeStore.clearData()
    homeStore.sessionFamilyId = body.family_id || body.id || ''
    if (body.role) homeStore.role = body.role as any

    // 不再打「创建成功」的 toast 再延时 reLaunch：201 的 message 说的就是「强制选面尚未完成」，
    // 把这一跳的目的地（引导第②步）讲清的活交给 modules 页的页头与说明块，不在这里抢一句反话。
    // 成功路径不放 `submitting`：页面被 redirectTo 换掉，锁随页面一起销毁。
    // §4.1 row ①：提交后 redirectTo `homeos/family/modules` 的引导态完成强制选面（不是进首页）。
    uni.redirectTo({ url: '/pages/homeos/family/modules?mode=guide' })
  } catch (err: any) {
    console.error('Create family failed:', err)
    submitting.value = false
    uni.showToast({ title: describeError(err), icon: 'none' })
  }
}
</script>

<style scoped>
/* 色值一律引用 shell 的那一份主题令牌（17.9、§2.4 同源⑤：写死色值即门禁失败）。
   结构、尺寸与圆角保持原样，只把三组灰阶与两处底色换成令牌，暗态因此自动跟随。 */
.create-family-page {
  min-height: 100vh;
  background: var(--bg-secondary);
  padding: 40rpx;
}

.page-header {
  text-align: center;
  margin-bottom: 60rpx;
}

.title {
  font-size: 40rpx;
  font-weight: bold;
  color: var(--text-primary);
  display: block;
  margin-bottom: 10rpx;
}

.subtitle {
  font-size: 28rpx;
  color: var(--text-tertiary);
}

.form-container {
  background: var(--bg-primary);
  border-radius: 20rpx;
  padding: 40rpx;
  box-shadow: var(--shadow-lg);
}

.input-group {
  margin-bottom: 30rpx;
}

.label {
  font-size: 28rpx;
  color: var(--text-primary);
  margin-bottom: 10rpx;
  display: block;
}

.input-field {
  width: 100%;
  height: 80rpx;
  border: 2rpx solid var(--border-color);
  border-radius: 10rpx;
  padding: 0 20rpx;
  font-size: 28rpx;
  box-sizing: border-box;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.picker-field {
  width: 100%;
  height: 80rpx;
  border: 2rpx solid var(--border-color);
  border-radius: 10rpx;
  padding: 0 20rpx;
  font-size: 28rpx;
  display: flex;
  justify-content: space-between;
  align-items: center;
  box-sizing: border-box;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.arrow {
  font-size: 20rpx;
  color: var(--text-tertiary);
}

.create-btn {
  width: 100%;
  height: 90rpx;
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-dark) 100%);
  color: var(--color-on-primary);
  border: none;
  border-radius: 10rpx;
  font-size: 32rpx;
  font-weight: bold;
  margin-top: 40rpx;
}
</style>
