<script setup lang="ts">
// pages/finance/category/index —— 分类管理页
//
// 实现：
//   · 分类列表展示（按支出/收入分组）
//   · 添加分类
//   · 编辑分类
//   · 删除分类
//   · 二级分类支持

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { request } from '@/utils/request'

const { t } = useI18n({ useScope: 'global' })

interface Category {
  id: string
  family_id?: string // null means system preset
  name: string
  type: 'expense' | 'income'
  parent_id?: string
  icon?: string
  sort_order: number
}

const categories = ref<Category[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const activeTab = ref<'expense' | 'income'>('expense')
const showAddDialog = ref(false)
const editingCategory = ref<Category | null>(null)

// Form state
const formName = ref('')
const formType = ref<'expense' | 'income'>('expense')
const formParentId = ref<string | undefined>(undefined)

// Computed: filtered categories by type
const expenseCategories = computed(() =>
  categories.value.filter(c => c.type === 'expense')
)

const incomeCategories = computed(() =>
  categories.value.filter(c => c.type === 'income')
)

// Computed: current categories based on active tab
const currentCategories = computed(() =>
  activeTab.value === 'expense' ? expenseCategories.value : incomeCategories.value
)

// Computed: top-level categories for parent selector
const topLevelCategories = computed(() =>
  currentCategories.value.filter(c => !c.parent_id)
)

// Fetch categories
async function fetchCategories() {
  loading.value = true
  error.value = null

  try {
    const response = await request.get('/api/finance/categories')
    categories.value = response.data.items || []
  } catch (err: any) {
    console.error('Failed to fetch categories:', err)
    error.value = err.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// Open add dialog
function openAddDialog(parentId?: string) {
  editingCategory.value = null
  formName.value = ''
  formType.value = activeTab.value
  formParentId.value = parentId
  showAddDialog.value = true
}

// Open edit dialog
function openEditDialog(category: Category) {
  editingCategory.value = category
  formName.value = category.name
  formType.value = category.type
  formParentId.value = category.parent_id
  showAddDialog.value = true
}

// Submit category (add or update)
async function handleSubmit() {
  if (!formName.value.trim()) {
    uni.showToast({ title: '请输入分类名称', icon: 'none' })
    return
  }

  try {
    const payload = {
      name: formName.value.trim(),
      type: formType.value,
      parent_id: formParentId.value || null,
    }

    if (editingCategory.value) {
      await request.put(`/api/finance/categories/${editingCategory.value.id}`, payload)
      uni.showToast({ title: '更新成功', icon: 'success' })
    } else {
      await request.post('/api/finance/categories', payload)
      uni.showToast({ title: '添加成功', icon: 'success' })
    }

    showAddDialog.value = false
    await fetchCategories()
  } catch (err: any) {
    console.error('Failed to save category:', err)
    uni.showToast({ title: err.message || '操作失败', icon: 'none' })
  }
}

// Delete category
async function handleDelete(category: Category) {
  uni.showModal({
    title: '确认删除',
    content: `确定要删除分类"${category.name}"吗？`,
    success: async (res) => {
      if (res.confirm) {
        try {
          await request.delete(`/api/finance/categories/${category.id}`)
          uni.showToast({ title: '删除成功', icon: 'success' })
          await fetchCategories()
        } catch (err: any) {
          console.error('Failed to delete category:', err)
          uni.showToast({ title: err.message || '删除失败', icon: 'none' })
        }
      }
    },
  })
}

// Get sub-categories for a parent
function getSubCategories(parentId: string): Category[] {
  return currentCategories.value.filter(c => c.parent_id === parentId)
}

onMounted(() => {
  fetchCategories()
})
</script>

<template>
  <view class="hc-page">
    <!-- Tab selector -->
    <view class="tab-bar">
      <view
        class="tab-item"
        :class="{ active: activeTab === 'expense' }"
        @click="activeTab = 'expense'"
      >
        <text>支出分类</text>
      </view>
      <view
        class="tab-item"
        :class="{ active: activeTab === 'income' }"
        @click="activeTab = 'income'"
      >
        <text>收入分类</text>
      </view>
    </view>

    <!-- Loading state -->
    <view v-if="loading" class="hc-loading">
      <text>加载中...</text>
    </view>

    <!-- Error state -->
    <view v-else-if="error" class="hc-error">
      <text>{{ error }}</text>
      <button @click="fetchCategories">重试</button>
    </view>

    <!-- Category list -->
    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="currentCategories.length === 0" class="hc-empty">
        <text>暂无分类</text>
        <button class="hc-empty-btn" @click="openAddDialog">添加分类</button>
      </view>

      <!-- Render categories with hierarchy -->
      <view v-for="cat in topLevelCategories" :key="cat.id" class="cat-group">
        <view class="cat-parent">
          <view class="cat-info">
            <text class="cat-name">{{ cat.name }}</text>
            <text v-if="!cat.family_id" class="cat-badge">系统</text>
          </view>
          <view class="cat-actions">
            <text class="cat-action" @click="openAddDialog(cat.id)">添加子分类</text>
            <text class="cat-action" @click="openEditDialog(cat)">编辑</text>
            <text v-if="cat.family_id" class="cat-action cat-delete" @click="handleDelete(cat)">删除</text>
          </view>
        </view>

        <!-- Sub-categories -->
        <view v-for="sub in getSubCategories(cat.id)" :key="sub.id" class="cat-child">
          <view class="cat-info">
            <text class="cat-name">{{ sub.name }}</text>
          </view>
          <view class="cat-actions">
            <text class="cat-action" @click="openEditDialog(sub)">编辑</text>
            <text v-if="sub.family_id" class="cat-action cat-delete" @click="handleDelete(sub)">删除</text>
          </view>
        </view>
      </view>
    </scroll-view>

    <!-- Floating action button -->
    <view class="fab" @click="openAddDialog">
      <text class="fab-icon">＋</text>
    </view>

    <!-- Add/Edit dialog -->
    <view v-if="showAddDialog" class="dialog-mask" @click="showAddDialog = false">
      <view class="dialog-content" @click.stop>
        <text class="dialog-title">{{ editingCategory ? '编辑分类' : '添加分类' }}</text>

        <view class="dialog-form">
          <view class="form-row">
            <text class="form-label">分类名称</text>
            <input v-model="formName" class="form-input" placeholder="例如：餐饮" />
          </view>

          <view class="form-row">
            <text class="form-label">父分类（可选）</text>
            <picker
              mode="selector"
              :range="['无', ...topLevelCategories.map(c => c.name)]"
              :value="formParentId ? topLevelCategories.findIndex(c => c.id === formParentId) + 1 : 0"
              @change="(e: any) => {
                const idx = e.detail.value
                formParentId = idx === 0 ? undefined : topLevelCategories[idx - 1]?.id
              }"
            >
              <view class="form-picker">
                <text>{{ formParentId ? topLevelCategories.find(c => c.id === formParentId)?.name : '无（顶级分类）' }}</text>
              </view>
            </picker>
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

/* Tab bar */
.tab-bar {
  display: flex;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.tab-item {
  flex: 1;
  padding: 24rpx 0;
  text-align: center;
  font-size: 28rpx;
  color: var(--text-secondary);
}

.tab-item.active {
  color: var(--color-primary);
  font-weight: 600;
  border-bottom: 4rpx solid var(--color-primary);
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
  color: #ffffff;
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

/* Category group */
.cat-group {
  margin-bottom: 24rpx;
}

.cat-parent,
.cat-child {
  padding: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  margin-bottom: 8rpx;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.cat-child {
  margin-left: 32rpx;
  background-color: var(--bg-tertiary);
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

.cat-delete {
  color: var(--color-error);
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
  color: #ffffff;
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
  background-color: rgba(0, 0, 0, 0.5);
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

.form-input,
.form-picker {
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
  color: #ffffff;
}
</style>
