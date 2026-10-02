import { createSSRApp } from 'vue'
import App from './App.vue'
import { i18n } from './i18n'

// uni-app(Vue3) 的应用入口约定：导出 createApp()，由 uni 运行时调用。
export function createApp() {
  const app = createSSRApp(App)
  app.use(i18n)
  return { app }
}
