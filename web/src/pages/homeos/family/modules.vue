<script setup lang="ts">
// pages/homeos/family/modules —— 面目录与开通（§4.6 row ① 治理页 + §4.1 row ① 建家引导态；PRD 17.8）
//
// **一页两用途**：§4.6 账法条写死了「`homeos/family/modules` 是一页两用途（治理页 + 创建引导态），
// 不另开第二条路由」。两态由入口参数 `?mode=guide` 区分，取值口径与
// `pages/homeos/legal/detail` 的 `?type=` 同源（查询参数不构成第四段路径，§2.5 通用落地规则）。
//   · **引导态**（`family/create` 提交 201 后 `redirectTo` 进来，§4.1 row ①、§6.11）：
//     只有「开通」这一个方向，**整页不渲染任何停用位**；「继续」在 0 项时不可用并给出原因文案
//     （PRD 17.8「至少启用 1 个面才能完成创建，该步骤不可跳过」+ 18.2#9「引导不可跳过」行）。
//   · **治理态**（从「我的」进来，§4.6 row ①）：管理员可写、其余角色只读且**看不到**开关（§七
//     「按钮不渲染，不是置灰」）；停用走**二次确认弹层**（§2.2 判定表把「停用」列在成弹层那一行），
//     弹层正文就是 PRD 17.8 第 2 条那句承诺：入口消失、数据不删、统计口径不变。
//
// 四条数据纪律：
//   1. **面清单不在本页**。目录、`born`、`enabled`、`visible`、`availability` 全部来自
//      `GET /api/homeos/family/modules`（服务端把 registry × `homeos_family_module` × 服务出生 ×
//      角色 `scope=module` 合成一份，17.8 定版 ⑯）。本页出现任何一个面 code，就是 22.5 第 4 道
//      同源②「硬编码面清单」；新增一面在客户端只是多一行，不改本页。
//   2. **四个降级词不互换**（定版 ㉙）：`!born` → 「即将上线」+ 无任何控件；
//      `enabled ∧ availability === 'unavailable'` → 「已启用」再挂一条「暂不可用」；
//      `!visible` → 「本角色不可见」。这三个词都只活在开通页与深链落地（§八），首页不显（17.2）。
//   3. **乐观锁的 `version` 只认服务端那一份**：读写都走 `stores/home.ts` 的 `loadModules()` /
//      `setModuleEnabled()`，`modulesVersion` 恒等于最近一次**成功**的 GET/PUT 回执里的 version，
//      本页不自增、不假设。409 一律按 §6.5 的「用最新」分支处置 —— store 已重拉列表、开关回位，
//      本页只把原因说清并让用户照着新状态再决定，**绝不盲目重发写请求**。
//   4. **角色 = shell 会话快照那一份**（`homeStore.role`，唯一源 `GET /families`，经 `ensureSession()`）；
//      本页不再调 `/families` 自己推导，也不写默认值猜一个「member」（15.1 未知即最小权限）。

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useHomeStore, type FamilyModule } from '@/stores/home'

type FaceState = 'on' | 'open' | 'unborn'

const { t } = useI18n({ useScope: 'global' })
const homeStore = useHomeStore()

// ---------------------------------------------------------------- 模式判定（入口参数）
/**
 * `?mode=guide` = 引导态；缺省与任何非 `guide` 取值都是治理态。
 * 取当前页 `options` 的写法与 `legal/detail.vue` 一致（uni-app 无 vue-router，页面参数只有这一处来源）。
 */
const mode = computed<'guide' | 'manage'>(() => {
  const pages = getCurrentPages()
  const currentPage = pages[pages.length - 1] as any
  return currentPage?.options?.mode === 'guide' ? 'guide' : 'manage'
})
const isGuide = computed(() => mode.value === 'guide')

// ---------------------------------------------------------------- 页面状态（第八章同一套态位）
const booting = ref(true)
const errorText = ref('')
const errorCode = ref('')
/** 正在写入的那一面的 code：同一时刻只允许一条 PUT（乐观锁的 version 不能被两次并发写各取一次）。 */
const writingCode = ref('')
const continuing = ref(false)

const modules = computed<FamilyModule[]>(() => homeStore.modules)
const isAdmin = computed(() => homeStore.isAdmin)
const enabledCount = computed(() => modules.value.filter((m) => m.enabled).length)
/** 引导态的「继续」门槛：至少 1 面（PRD 17.8 第 4 条的另一端，与治理态「停用有下限」同一条约束）。 */
const canContinue = computed(() => enabledCount.value > 0)

const headSub = computed(() => t('homeos.modules.enabled_count', { count: enabledCount.value }))

/** 区标题右侧那一句：引导态讲清这一步，治理态按角色讲清可写性（非管理员不渲染开关）。 */
const writeHint = computed(() => {
  if (isGuide.value) return isAdmin.value ? t('homeos.modules.guide_section_hint') : t('homeos.modules.guide_admin_only')
  return isAdmin.value ? t('homeos.modules.admin_hint') : t('homeos.modules.readonly_hint')
})

// ---------------------------------------------------------------- 三态与标签
/**
 * 三态（§4.6 row ①「全量面清单三态」）：已启用 / 可开通 / 即将上线。
 * `!born` 优先级最高：未出生的面不可能有启用行（服务端 `CreateFamily` 与 `PUT family/modules`
 * 两处都拒未登记或未出生的 code），所以它恒是只读项。
 */
function faceState(mod: FamilyModule): FaceState {
  if (!mod.born) return 'unborn'
  return mod.enabled ? 'on' : 'open'
}

function stateLabel(mod: FamilyModule): string {
  return t(`homeos.modules.state_${faceState(mod)}`)
}

/** 「暂不可用」= 面已启用、服务可达性这一趟不行（㉙ 第三个词），与「即将上线」「未启用」都不同层。 */
function showUnavailable(mod: FamilyModule): boolean {
  return mod.born && mod.enabled && mod.availability === 'unavailable'
}

/** 图标位：registry 的 `icon` 是图标集**名称**（PRD 17.6 禁 emoji），客户端没有图标字体组件，
 *  与首页 C 区格子同一处径 —— 取面名首字占这个圆位，不自造第二套缩写符号。 */
function iconChar(mod: FamilyModule): string {
  return (mod.name || mod.code).charAt(0)
}

// ---------------------------------------------------------------- 控件可见性（§2.2 / §七）
/** 治理态 + 管理员 + 已出生的面才有开关；引导态整页不渲染停用位。 */
function showSwitch(mod: FamilyModule): boolean {
  return !isGuide.value && isAdmin.value && mod.born
}

/** 引导态的唯一写方向：开通，且只给还没挂载的已出生面。 */
function showOpenAction(mod: FamilyModule): boolean {
  return isGuide.value && isAdmin.value && mod.born && !mod.enabled
}

// ---------------------------------------------------------------- 取数
function classifyError(err: any): { text: string; code: string } {
  const msg: string = err?.message || t('homeos.modules.load_failed')
  // 第八章「失败：可重试 + 错误码可见（用于对账与客服），文案不带堆栈」。
  const status = typeof err?.status === 'number' && err.status > 0 ? String(err.status) : ''
  const hit = status || msg.match(/HTTP\s+(\d{3})/)?.[1] || 'NETWORK'
  return { text: msg, code: hit }
}

async function load(): Promise<void> {
  booting.value = true
  errorText.value = ''
  errorCode.value = ''
  try {
    // 角色只由 shell store 取那一份；建家刚 redirect 过来时 create 页已把 201 的 role 写进同一份快照，
    // ensureSession 因此不会重复打 /families（sessionLoaded 已置则直接返回）。
    await homeStore.ensureSession()
    await homeStore.loadModules()
  } catch (err: any) {
    const { text, code } = classifyError(err)
    errorText.value = text
    errorCode.value = code
  } finally {
    booting.value = false
  }
}

/** 409 之后重拉列表是 store 已经在做的事；这里只负责把开关的视觉态拉回服务端那份真值。 */
async function reloadCatalog(): Promise<void> {
  try {
    await homeStore.loadModules()
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  }
}

// ---------------------------------------------------------------- 写（开通 / 停用）
/**
 * `<switch>` 一旦被用户拨动，它的视觉态就和 `mod.enabled` 脱钩了（H5 下 `:checked` 变化不重绘已存在的
 * 原生开关）。停用需要二次确认、确认可能取消，写请求也可能被 409 打回 —— 这几种情况下都必须让开关
 * 回到服务端那一份状态，所以给它挂一个 epoch 作 key：bump 一次就重建一个反映真值的开关。
 */
const switchEpoch = ref(0)

/**
 * 一条 PUT 的唯一出口：`homeStore.setModuleEnabled()`。
 * version 由 store 持（最近一次成功回执），409 由 store 重拉列表（§6.5「用最新」，不盲重试）。
 */
async function writeModule(mod: FamilyModule, enabled: boolean): Promise<void> {
  if (!isAdmin.value) {
    // §七：写入口对无权的角色根本不渲染，走到这里说明快照刚变过；不猜、不发 doomed 请求，重拉列表。
    uni.showToast({ title: t('homeos.modules.admin_only_hint'), icon: 'none' })
    await reloadCatalog()
    return
  }
  if (!mod.born) {
    // 未出生的面对客户端只读（服务端对两个方向都回 400 face_not_born）。
    uni.showToast({ title: t('homeos.modules.unborn_hint'), icon: 'none' })
    return
  }
  if (writingCode.value) return

  writingCode.value = mod.code
  try {
    const result = await homeStore.setModuleEnabled(mod.code, enabled)
    if (result.ok) {
      // store 只在已有 summary 快照时才顺手重拉首页集合；冷启动直接落本页时快照是空的，
      // 那一份 `faces[]` 就得由这里补上，否则 §6.11 的「≤2 秒内五处同步」在这一条路径上落空
      // （引导态不需要：它的收束由 continueGuide 那一次统一同步，见下）。
      if (!isGuide.value && !homeStore.family) {
        try {
          await homeStore.fetchSummary()
        } catch {
          // 不阻断：首页 onMounted 自己会再打一次 home/summary（§3.1 首屏单请求）
        }
      }
      uni.showToast({
        title: enabled
          ? t('homeos.modules.enable_ok', { name: mod.name })
          : t('homeos.modules.disable_ok', { name: mod.name }),
        icon: 'success',
      })
    } else if (result.conflict) {
      // 409 有两种：version_conflict 与 cannot_disable_last_module，服务端 message 各自说清了原因，
      // 列表也已被 store 重拉过 —— 这里只转述原因，不重发写请求（17.8 乐观锁不接受静默覆盖）。
      uni.showToast({ title: result.message || t('homeos.modules.conflict'), icon: 'none', duration: 3000 })
    } else {
      uni.showToast({ title: result.message || t('homeos.modules.write_failed'), icon: 'none' })
    }
  } catch (err: any) {
    const { text } = classifyError(err)
    uni.showToast({ title: text, icon: 'none' })
  } finally {
    writingCode.value = ''
    switchEpoch.value += 1
  }
}

function onSwitch(mod: FamilyModule, e: any): void {
  const next = !!e?.detail?.value
  if (next) {
    // 开通不是破坏性动作，直接写；停用必须先过弹层（§2.2）。
    void writeModule(mod, true)
    return
  }
  askDisable(mod)
}

function enableFace(mod: FamilyModule): void {
  void writeModule(mod, true)
}

// ---------------------------------------------------------------- 停用二次确认弹层（不是页面）
const disableTarget = ref<FamilyModule | null>(null)

/**
 * 「停用有下限」（PRD 17.8 第 4 条）：只剩最后一个已启用面时服务端拒绝。
 * 已启用数来自 GET 的清单（不是本页自己数面名册），命中时弹层给出原因并锁死确认位，
 * 不发一条注定失败的 PUT；真发生并发导致计数过期时，仍由上面那条 409 分支兜住。
 */
const isLastEnabledFace = computed(
  () => !!disableTarget.value && disableTarget.value.enabled && enabledCount.value <= 1
)

function askDisable(mod: FamilyModule): void {
  if (isGuide.value || !isAdmin.value) return
  disableTarget.value = mod
}

function cancelDisable(): void {
  disableTarget.value = null
  // 用户没确认，这一趟就没有写过：把开关的视觉态拨回真值。
  switchEpoch.value += 1
}

function confirmDisable(): void {
  const target = disableTarget.value
  if (!target || isLastEnabledFace.value) return
  disableTarget.value = null
  void writeModule(target, false)
}

// ---------------------------------------------------------------- 收束
/**
 * 引导态完成 = 家庭创建完成 = 无栈替换进首页（§6.11「至少勾选 1 个面 → 确认 → 家庭创建完成，
 * 无栈替换进 home/index（矩阵即所选集合）」；§2.3「登录成功 / 家庭切换 = 重置栈」同一类语义）。
 * 走之前把 shell 的面集合再同步一次：`loadModules()` 拿家庭口径的启用集，`fetchSummary()` 拿
 * 首页矩阵与其余四个消费位共用的 `faces[]`（17.8 五处同源）。
 */
async function continueGuide(): Promise<void> {
  if (!canContinue.value || continuing.value) return
  continuing.value = true
  try {
    try {
      await homeStore.loadModules()
      await homeStore.fetchSummary()
    } catch {
      // 同步失败不阻断这一步：首页 onMounted 自己会再打一次 home/summary（§3.1 首屏单请求），
      // 面集合因此仍有一份、且不是本页自造的。
    }
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  } finally {
    continuing.value = false
  }
}

/** 治理态返回：一级页 → 二级页是压栈进来的，返回即出栈（§2.3）。 */
function goBack(): void {
  if (getCurrentPages().length > 1) {
    uni.navigateBack()
  } else {
    uni.reLaunch({ url: '/pages/homeos/home/index' })
  }
}

onMounted(load)
</script>

<template>
  <view class="hc-page">
    <view class="page-head">
      <!-- 引导态不放返回位：这一步不可跳过（PRD 17.8），且 redirectTo 之后它就是栈里唯一一页 -->
      <text v-if="!isGuide" class="head-back" @click="goBack">‹</text>
      <view class="head-titles">
        <text class="head-title">
          {{ isGuide ? t('homeos.modules.guide_title') : t('homeos.modules.title') }}
        </text>
        <text class="head-sub">{{ headSub }}</text>
      </view>
      <text class="head-placeholder" />
    </view>

    <!-- 加载中：骨架屏，不用 spinner 遮内容（第八章） -->
    <view v-if="booting" class="hc-skeleton">
      <view class="sk-row" />
      <view class="sk-row" />
      <view class="sk-row" />
    </view>

    <!-- 失败：可重试 + 错误码可见（第八章）。目录读不到时「继续」也不可用，引导不会带着空清单进首页。 -->
    <view v-else-if="errorText" class="hc-state-error">
      <text class="state-title">{{ t('homeos.modules.load_failed') }}</text>
      <text class="state-hint">{{ errorText }}</text>
      <text class="state-code">{{ t('homeos.modules.error_code') }}：{{ errorCode }}</text>
      <button class="state-btn" @click="load">{{ t('homeos.modules.retry') }}</button>
    </view>

    <scroll-view v-else class="hc-scroll" scroll-y>
      <view v-if="isGuide" class="guide-note">
        <text class="guide-note-title">{{ t('homeos.modules.guide_hint_title') }}</text>
        <text class="guide-note-body">{{ t('homeos.modules.guide_note') }}</text>
      </view>

      <!-- 空态：有权且请求成功但零结果 —— 给引导动作，不写「暂无数据」了事（第八章） -->
      <view v-if="modules.length === 0" class="hc-state-empty">
        <text class="state-title">{{ t('homeos.modules.empty_title') }}</text>
        <text class="state-hint">{{ t('homeos.modules.empty_hint') }}</text>
        <button class="state-btn" @click="load">{{ t('homeos.modules.retry') }}</button>
      </view>

      <template v-else>
        <view class="section-head">
          <text class="section-title">{{ t('homeos.modules.section_catalog') }}</text>
          <text class="section-hint">{{ writeHint }}</text>
        </view>

        <view class="module-list">
          <view v-for="mod in modules" :key="mod.code" class="module-row">
            <view class="module-icon" :class="{ dim: !mod.born }">
              <text>{{ iconChar(mod) }}</text>
            </view>

            <view class="module-main">
              <view class="module-line1">
                <text class="module-name">{{ mod.name }}</text>
                <text class="state-badge" :class="`state-${faceState(mod)}`">{{ stateLabel(mod) }}</text>
              </view>
              <view class="module-line2">
                <text v-if="showUnavailable(mod)" class="module-tag">{{ t('homeos.modules.tag_unavailable') }}</text>
                <text v-if="mod.born && !mod.visible" class="module-tag">
                  {{ t('homeos.modules.tag_hidden') }}
                </text>
                <text v-if="isGuide && mod.enabled" class="module-tag">
                  {{ t('homeos.modules.tag_picked') }}
                </text>
              </view>
            </view>

            <!-- 控件位三选一：开关（治理态·管理员·已出生）/ 开通（引导态·管理员·未挂载）/ 纯文态 -->
            <switch
              v-if="showSwitch(mod)"
              :key="`${mod.code}-${switchEpoch}`"
              class="module-switch"
              :checked="mod.enabled"
              :disabled="writingCode === mod.code"
              @change="(e: any) => onSwitch(mod, e)"
            />
            <text v-else-if="showOpenAction(mod)" class="open-btn" @click="enableFace(mod)">
              {{ t('homeos.modules.enable') }}
            </text>
            <text v-else class="module-state">{{ stateLabel(mod) }}</text>
          </view>
        </view>

        <view class="footnote">
          <text>{{ t('homeos.modules.footnote') }}</text>
        </view>
      </template>
    </scroll-view>

    <!-- 引导态底部收束位：0 项时不可用，并给出不可用的原因（18.2#9「引导不可跳过」判据） -->
    <view v-if="isGuide && !booting && !errorText" class="guide-foot">
      <text class="foot-count">{{ t('homeos.modules.selected_count', { count: enabledCount }) }}</text>
      <text v-if="enabledCount === 0" class="foot-reason">{{ t('homeos.modules.need_one_reason') }}</text>
      <button class="continue-btn" :disabled="!canContinue || continuing" @click="continueGuide">
        {{ continuing ? t('homeos.modules.continuing') : t('homeos.modules.continue') }}
      </button>
    </view>

    <!-- 停用二次确认弹层（§2.2：停用成弹层不成页；文案即 PRD 17.8 第 2、3 条的承诺） -->
    <view v-if="disableTarget" class="sheet-mask" @click="cancelDisable">
      <view class="sheet" @click.stop>
        <text class="sheet-title">{{ t('homeos.modules.disable_confirm_title') }}</text>
        <text class="state-hint">
          {{ t('homeos.modules.disable_confirm_body', { name: disableTarget.name }) }}
        </text>
        <text class="state-hint">{{ t('homeos.modules.disable_confirm_note') }}</text>
        <view v-if="isLastEnabledFace" class="locked-note">
          <text>{{ t('homeos.modules.last_face_reason') }}</text>
        </view>

        <view class="dialog-actions">
          <button class="dialog-btn" @click="cancelDisable">{{ t('homeos.modules.cancel') }}</button>
          <button
            class="dialog-btn danger-btn"
            :disabled="isLastEnabledFace || writingCode !== ''"
            @click="confirmDisable"
          >
            {{ t('homeos.modules.confirm_disable') }}
          </button>
        </view>
      </view>
    </view>
  </view>
</template>

<style scoped>
/* 色值一律引用 shell 的一份主题令牌（17.9、§2.4 同源⑤：写死色值即门禁失败）；亮/暗两态同一组件。 */
.hc-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: var(--bg-secondary);
}

.page-head {
  display: flex;
  align-items: center;
  gap: 16rpx;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-bottom: 1rpx solid var(--divider-color);
}

.head-back {
  font-size: 44rpx;
  line-height: 1;
  color: var(--text-secondary);
  padding: 0 8rpx;
}

.head-titles {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4rpx;
}

.head-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.head-sub {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.head-placeholder {
  width: 1rpx;
}

.hc-scroll {
  flex: 1;
  padding: 24rpx;
}

/* 骨架屏 */
.hc-skeleton {
  flex: 1;
  padding: 24rpx;
  display: flex;
  flex-direction: column;
  gap: 16rpx;
}

.sk-row {
  height: 128rpx;
  border-radius: var(--radius-md);
  background-color: var(--bg-tertiary);
}

/* 态位（第八章同一组件库） */
.hc-state-empty,
.hc-state-error {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16rpx;
  padding: 96rpx 48rpx;
}

.state-title {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.state-hint {
  font-size: 26rpx;
  color: var(--text-secondary);
  text-align: center;
  line-height: var(--line-height-normal);
}

.state-code {
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.state-btn {
  margin-top: 16rpx;
  padding: 16rpx 48rpx;
  background-color: var(--color-primary);
  color: var(--color-white);
  border-radius: var(--radius-md);
  font-size: 28rpx;
}

/* 引导态说明块 */
.guide-note {
  display: flex;
  flex-direction: column;
  gap: 8rpx;
  padding: 24rpx 28rpx;
  margin-bottom: 24rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-sm);
}

.guide-note-title {
  font-size: 28rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.guide-note-body {
  font-size: 24rpx;
  color: var(--text-secondary);
  line-height: var(--line-height-normal);
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 16rpx;
  padding: 0 8rpx 12rpx;
}

.section-title {
  font-size: 26rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.section-hint {
  font-size: 22rpx;
  color: var(--text-tertiary);
  text-align: right;
}

/* 面清单行 */
.module-list {
  background-color: var(--bg-primary);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.module-row {
  display: flex;
  align-items: center;
  gap: 20rpx;
  padding: 24rpx 28rpx;
  border-bottom: 1rpx solid var(--divider-color);
}

.module-row:last-child {
  border-bottom: none;
}

.module-icon {
  width: 72rpx;
  height: 72rpx;
  border-radius: 50%;
  background-color: var(--color-primary-light);
  color: var(--color-white);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 30rpx;
  font-weight: 600;
  flex-shrink: 0;
}

/* 未出生的面不成「可进」的样子：图标位降一级灰，但它只在这一页出现，不进首页（17.2） */
.module-icon.dim {
  background-color: var(--bg-tertiary);
  color: var(--text-tertiary);
}

.module-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 8rpx;
  overflow: hidden;
}

.module-line1 {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

.module-name {
  font-size: 30rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.state-badge {
  font-size: 20rpx;
  padding: 2rpx 12rpx;
  border-radius: var(--radius-sm);
  background-color: var(--bg-tertiary);
  color: var(--text-secondary);
}

.state-badge.state-on {
  background-color: var(--color-primary);
  color: var(--color-white);
}

.state-badge.state-unborn {
  background-color: var(--bg-tertiary);
  color: var(--text-tertiary);
}

.module-line2 {
  display: flex;
  align-items: center;
  gap: 12rpx;
  flex-wrap: wrap;
}

.module-tag {
  font-size: 22rpx;
  color: var(--text-tertiary);
}

.module-state {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.open-btn {
  font-size: 26rpx;
  color: var(--color-primary);
  padding: 8rpx 24rpx;
  border: 1rpx solid var(--color-primary);
  border-radius: var(--radius-sm);
}

.footnote {
  padding: 24rpx 8rpx 48rpx;
  font-size: 22rpx;
  color: var(--text-tertiary);
  line-height: var(--line-height-normal);
}

/* 引导态底部 */
.guide-foot {
  display: flex;
  flex-direction: column;
  gap: 8rpx;
  padding: 24rpx 32rpx;
  background-color: var(--bg-primary);
  border-top: 1rpx solid var(--divider-color);
}

.foot-count {
  font-size: 24rpx;
  color: var(--text-secondary);
}

.foot-reason {
  font-size: 22rpx;
  color: var(--color-error);
  line-height: var(--line-height-normal);
}

.continue-btn {
  margin-top: 8rpx;
  width: 100%;
  height: 88rpx;
  line-height: 88rpx;
  background-color: var(--color-primary);
  color: var(--color-white);
  border: none;
  border-radius: var(--radius-md);
  font-size: 30rpx;
  font-weight: 600;
}

.continue-btn[disabled] {
  background-color: var(--bg-tertiary);
  color: var(--text-tertiary);
}

/* 弹层 */
.sheet-mask {
  position: fixed;
  inset: 0;
  background-color: var(--overlay-dark);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  z-index: 100;
}

.sheet {
  width: 100%;
  max-height: 68vh;
  overflow-y: auto;
  padding: 32rpx;
  background-color: var(--bg-primary);
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  display: flex;
  flex-direction: column;
  gap: 20rpx;
}

.sheet-title {
  font-size: 32rpx;
  font-weight: 600;
  color: var(--text-primary);
}

.locked-note {
  padding: 20rpx 24rpx;
  background-color: var(--bg-tertiary);
  border-radius: var(--radius-sm);
  font-size: 24rpx;
  color: var(--text-secondary);
  line-height: var(--line-height-normal);
}

.dialog-actions {
  display: flex;
  gap: 16rpx;
}

.dialog-btn {
  flex: 1;
  padding: 24rpx 0;
  border-radius: var(--radius-md);
  font-size: 28rpx;
  background-color: var(--bg-tertiary);
  color: var(--text-primary);
}

.danger-btn {
  background-color: var(--color-error);
  color: var(--color-white);
}

.danger-btn[disabled] {
  background-color: var(--bg-tertiary);
  color: var(--text-tertiary);
}
</style>
