<script setup lang="ts">
// pages/finance/category/index —— 分类管理页（§5.1 财务面末位「账户与设置」的分类维护部分，
// 由流水页工具栏「分类管理」进入；`finance/settings/index` 本身本期未建，见本轮上报）。
//
// 口径全部对齐 svc-finance 的路由表与 JSON tag（`cmd/svc-finance/main.go`、
// `internal/model/finance.go`、迁移 `finance_0001_base_schema`），不猜字段名：
//   · 列表  GET  /api/finance/categories?family_id=  → 裸回 `{items:[FinanceCategory]}`
//     （family_id 是 ListCategories 的必填 query，缺它即 400）
//   · 新建  POST /api/finance/categories             body `{family_id, name, icon, sort_order}`
//   · 停用  PUT  /api/finance/categories/:id/deactivate（服务端唯一的下线动作）
//   服务端**没有** `PUT /categories/:id` 与 `DELETE /categories/:id` 两条路由，因此本页不提供
//   「编辑 / 删除」两个动作 —— 挂上去就是必定 404 的按钮。
//   · `finance_category` 表**没有** type 与 parent_id 两列（契约 Category 里两者都有，
//     已作为定版冲突上报），所以这里是一个平铺列表：按不存在的列分「支出/收入」两组，
//     两组都会永远空着。

import { ref, onMounted } from 'vue'
import { request } from '@/utils/request'
import { useHomeStore } from '@/stores/home'

/** 服务端 model.FinanceCategory 的 JSON 形状。 */
interface Category {
  id: string
  family_id?: string
  name: string
  icon?: string
  is_active: boolean
  sort_order: number
  version: number
  created_at: string
  updated_at: string
}

/** 契约 `/categories` 的 200 体。 */
interface CategoryListBody {
  items?: Category[]
}

const categories = ref<Category[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const showAddDialog = ref(false)

// Form state
const formName = ref('')
const formIcon = ref('')
const formSortOrder = ref('')

const homeStore = useHomeStore()

/** family_id 读 shell store 的会话快照：分包不自存一份、不在本页重拉 `/families`。 */
async function resolveSessionFamilyId(): Promise<string> {
  if (homeStore.sessionFamilyId) return homeStore.sessionFamilyId
  try {
    await homeStore.ensureSession()
  } catch {
    return ''
  }
  return homeStore.sessionFamilyId || ''
}

// Fetch categories
async function fetchCategories() {
  loading.value = true
  error.value = null

  try {
    const familyId = await resolveSessionFamilyId()
    if (!familyId) {
      categories.value = []
      error.value = '还没有可用的家庭，请先在首页创建或加入家庭'
      return
    }

    // 请求层 resolve 的就是裸响应体：直接读 items。
    const body = await request.get<CategoryListBody>('/api/finance/categories', {
      params: { family_id: familyId },
    })
    categories.value = Array.isArray(body?.items) ? body.items : []
  } catch (err: any) {
    console.error('Failed to fetch categories:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Open add dialog
function openAddDialog() {
  formName.value = ''
  formIcon.value = ''
  formSortOrder.value = ''
  showAddDialog.value = true
}

// Submit category（服务端只有创建与停用、没有更新，因此这里只有「新建」一条路）
async function handleSubmit() {
  if (!formName.value.trim()) {
    uni.showToast({ title: '请输入分类名称', icon: 'none' })
    return
  }

  const familyId = await resolveSessionFamilyId()
  if (!familyId) {
    uni.showToast({ title: error.value || '缺少家庭，无法新建分类', icon: 'none' })
    return
  }

  try {
    await request.post('/api/finance/categories', {
      family_id: familyId,
      name: formName.value.trim(),
      icon: formIcon.value.trim(),
      sort_order: Number.parseInt(formSortOrder.value, 10) || 0,
    })
    uni.showToast({ title: '添加成功', icon: 'success' })

    showAddDialog.value = false
    await fetchCategories()
  } catch (err: any) {
    console.error('Failed to save category:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

// 停用分类：服务端 DeactivateCategory 的口径是「不影响历史流水」，
// 停用的分类不再进记账表单的分类选择（本页仍列出、标「已停用」）。
function deactivateCategory(category: Category) {
  uni.showModal({
    title: '确认停用',
    content: `确定要停用分类「${category.name}」吗？停用后不再出现在记账的分类选择里。`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await request.put(`/api/finance/categories/${category.id}/deactivate`)
        uni.showToast({ title: '已停用', icon: 'success' })
        await fetchCategories()
      } catch (err: any) {
        console.error('Failed to deactivate category:', err)
        uni.showToast({ title: err.message || '停用失败', icon: 'none' })
      }
    },
  })
}

onMounted(() => {
  fetchCategories()
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
      <button @click="fetchCategories()">重试</button>
    </view>

    <!-- Category list：分类表没有收支类型与父子两列，故为平铺列表 -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="categories.length === 0" class="hc-empty">
        <text>暂无分类</text>
        <button class="hc-empty-btn" @click="openAddDialog">添加分类</button>
      </view>

      <view v-for="cat in categories" :key="cat.id" class="cat-row">
        <view class="cat-info">
          <text v-if="cat.icon" class="cat-icon">{{ cat.icon }}</text>
          <text class="cat-name">{{ cat.name }}</text>
          <text v-if="!cat.is_active" class="cat-badge">已停用</text>
        </view>
        <view class="cat-actions">
          <text v-if="cat.is_active" class="cat-action" @click="deactivateCategory(cat)">停用</text>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">添加分类</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">分类名称</text>
            <input v-model="formName" class="form-input" placeholder="例如：餐饮" />
          </view>

          <view class="form-row">
            <text class="form-label">图标（可选，单个字符）</text>
            <input v-model="formIcon" class="form-input" placeholder="例如：餐" maxlength="2" />
          </view>

          <view class="form-row">
            <text class="form-label">排序（可选，数字越小越靠前）</text>
            <input v-model="formSortOrder" class="form-input" type="number" placeholder="0" />
          </view>
        </view>

        <view class="dialog-actions">
          <button class="dialog-btn dialog-cancel" @click="showAddDialog = false">取消</button>
          <button class="dialog-btn dialog-confirm" @click="handleSubmit">确定</button>
        </view>
      </view>
    </view>
  </view>
</template>

<style scoped>
.hc-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: var(--bg-secondary);
}

/* Loading and empty states */
.hc-loading,
.hc-error,
.hc-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 96rpx 48rpx;
  gap: 24rpx;
}

.hc-error button,
.hc-empty-btn {
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

/* Category row */
.cat-row {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  margin-bottom: 8rpx;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.cat-info {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.cat-name {
  font-size: 28rpx;
  color: var(--text-primary);
}

.cat-icon {
  font-size: 28rpx;
  color: var(--text-secondary);
}

.cat-badge {
  padding: 4rpx 12rpx;
  background-color: var(--bg-secondary);
  color: var(--text-tertiary);
  font-size: 20rpx;
  border-radius: var(--radius-sm);
}

.cat-actions {
  display: flex;
  gap: 24rpx;
}

.cat-action {
  font-size: 26rpx;
  color: var(--color-primary);
}

/* FAB */
.fab {
  position: fixed;
  right: 32rpx;
  bottom: 140rpx;
  width: 96rpx;
  height: 96rpx;
  border-radius: 50%;
  background-color: var(--color-primary);
  color: var(--color-white);
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: var(--shadow-lg);
  z-index: 50;
}

.fab-icon {
  font-size: 48rpx;
  font-weight: 300;
}

/* Dialog */
.dialog-mask {
  position: fixed;
  inset: 0;
  background-color: var(--overlay-dark);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.dialog-content {
  width: 600rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg);
  padding: 32rpx;
}

.dialog-title {
  display: block;
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 32rpx;
  text-align: center;
}

.dialog-form {
  margin-bottom: 32rpx;
}

.form-row {
  margin-bottom: 24rpx;
}

.form-label {
  display: block;
  font-size: 28rpx;
  color: var(--text-secondary);
  margin-bottom: 8rpx;
}

.form-input {
  width: 100%;
  padding: 16rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 28rpx;
  color: var(--text-primary);
}

.dialog-actions {
  display: flex;
  gap: 16rpx;
}

.dialog-btn {
  flex: 1;
  padding: 24rpx 0;
  border-radius: var(--radius-md);
  font-size: 30rpx;
}

.dialog-cancel {
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
}

.dialog-confirm {
  background-color: var(--color-primary);
  color: var(--color-white);
}
</style>
