/// <reference types="vite/client" />

// uni-app(Vue3) CLI 工程的类型声明约定文件。
declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

interface ImportMetaEnv {
  /** 唯一的 base URL（17.7 第 5 条）。未注入即同源，见 src/api/request.ts 文件头。 */
  readonly VITE_API_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
