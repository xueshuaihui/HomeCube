<script lang="ts">
// App.vue = uni-app 的应用级根组件（工程约定，必须存在）。
//
// 本卡（S1）只交付工程骨架，应用级不挂任何行为：
//   · 一级导航 shell（顶栏一行 / 时间窗条 / D 行）与状态组件库骨架 —— S2（§十 前端线 S2 行）
//   · 四份 shell 级 store（faces[] / period / unread / 主题令牌，PRD 17.7 第 8 条）—— S2
// 全局样式同理：主题令牌由主包出一份（亮/暗两套值）属 S2（§九「主题令牌」行、17.9），
// 此处不预置任何色值，避免与 S2 的令牌表并存出第二份真源。
export default { name: 'App' }
</script>

<style>
/* Per PRD 17.9 and 18.2#10: Theme tokens for light/dark two-state mode */
@import './styles/theme.css';
</style>
