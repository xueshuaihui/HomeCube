import { defineConfig } from 'vite'
import uniPlugin from '@dcloudio/vite-plugin-uni'

// @dcloudio/vite-plugin-uni 的入口是 CJS（node_modules/@dcloudio/vite-plugin-uni/package.json
// 只有 main: dist/index.js，无 exports 字段）。本工程 package.json 带 "type": "module"，
// 直接 import 默认导出拿到的是整个 module.exports 对象（实测 uni build 报
// 「uni is not a function」），插件工厂在它的 .default 上。两种形状都收，不赌互操作行为。
const uni = (uniPlugin as unknown as { default?: typeof uniPlugin }).default ?? uniPlugin

// uni-app(Vue3 + Vite) 的工程约定：页面路由表在 src/pages.json（由 uni 插件读取），
// 构建目标由 uni 命令的 UNI_PLATFORM 决定（本卡只出 H5，见 package.json 的 build:h5）。
export default defineConfig({
  plugins: [uni()]
})
