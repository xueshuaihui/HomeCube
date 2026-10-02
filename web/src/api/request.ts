// 单 base URL 请求层（PRD 17.7 第 5 条、docs/p1-tech-plan.md §九「单一 base URL」行）
//
// 形状：客户端只持有**一个** baseURL —— `https://{host}`，请求一律按 `/api/{code}/*` 拼前缀，
// 由 Nginx 按前缀反代到对应服务（§九 原文「由 Nginx 前缀反代；客户端不持有各服务地址、
// 不做服务发现」）。
//
// 因此本文件里**不得**出现下列三样东西（出现即 22.5 第 4 道失败）：
//   1. 服务地址表 / 端口表（code → host:port 的映射）；
//   2. 服务发现或任何按面挑选 host 的分支；
//   3. code 清单 —— 一个 code 的唯一真源是 server/packages/registry（§1.3），
//      这里把 code 当不透明字符串收，正是为了不造第二份表。
//
// 默认同源：`VITE_API_BASE_URL` 未注入时 base 取空串，请求即打到当前 origin 的 `/api/...`，
// 与 §九「单一 base URL = https://{host}/api/{code}/*」在部署形态（Nginx 同域反代）下等价。
//
// 鉴权头与 refresh（15.6、§四）**不在本卡**：S3 交付身份域后才接 token 中间件，
// 此处不预埋空的请求拦截器（那是桩）。

/** 本卡用到的方法集合（服务端 OpenAPI 由 S1-C/S7 交付，届时按契约收口）。 */
export type ApiMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

/** 网络层与业务层共用的错误形状：HTTP 状态 + 服务端返回体里的错误码与文案。 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly data: unknown

  constructor(status: number, code: string, message: string, data?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.data = data
  }
}

/** 只校验 code 的**形状**（registry 里 code 是小写标识符），不校验它是否在某个清单里。 */
function assertCodeShape(code: string): void {
  if (!/^[a-z][a-z0-9_]*$/.test(code)) {
    throw new ApiError(0, 'invalid_code', `非法的域 code 形状：${JSON.stringify(code)}`)
  }
}

function assertPathShape(path: string): void {
  if (!path.startsWith('/')) {
    throw new ApiError(0, 'invalid_path', `接口路径必须以 / 开头：${JSON.stringify(path)}`)
  }
  if (path.startsWith('/api/')) {
    // 前缀由本层拼，调用方再带一次就是两个 /api，正是「客户端持有各服务地址」的开端。
    throw new ApiError(0, 'invalid_path', `接口路径不得自带 /api 前缀（由请求层按 code 拼）：${path}`)
  }
}

function buildQuery(query?: Record<string, string | number | boolean | undefined>) {
  if (!query) return ''
  const parts = Object.entries(query)
    .filter(([, v]) => v !== undefined)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
  return parts.length ? `?${parts.join('&')}` : ''
}

/**
 * 拼出**唯一** base URL 之下的请求地址：`${base}/api/${code}${path}${query}`。
 * `base` 默认取模块级常量，参数化只为让单测能断言各种 base 下的拼接结果。
 */
export function apiUrl(
  code: string,
  path: string,
  query?: Record<string, string | number | boolean | undefined>,
  base: string = import.meta.env?.VITE_API_BASE_URL ?? ''
): string {
  assertCodeShape(code)
  assertPathShape(path)
  // 末尾多余的 / 一律收掉：单 base URL 只应有一个 host，不该拼出 //api。
  const normalized = base.replace(/\/+$/, '')
  return `${normalized}/api/${code}${path}${buildQuery(query)}`
}

export interface RequestOptions {
  method?: ApiMethod
  query?: Record<string, string | number | boolean | undefined>
  data?: unknown
  header?: Record<string, string>
}

/**
 * 唯一出口：按面 code 发一个请求。返回服务端 JSON；非 2xx 与网络失败一律抛 ApiError。
 * 出站形状固定为 `/api/{code}/*`（17.7 第 4 条：分包与路由域一一对应）。
 */
export function request<T = unknown>(code: string, path: string, options: RequestOptions = {}): Promise<T> {
  const url = apiUrl(code, path, options.query)
  return new Promise<T>((resolve, reject) => {
    uni.request({
      url,
      method: options.method ?? 'GET',
      data: options.data as UniApp.RequestOptions['data'],
      header: options.header,
      success(res) {
        const status = res.statusCode
        if (status >= 200 && status < 300) {
          resolve(res.data as T)
          return
        }
        const body = res.data as { code?: string; message?: string } | undefined
        reject(
          new ApiError(
            status,
            body?.code ?? 'http_error',
            body?.message ?? `请求失败：HTTP ${status} ${url}`,
            res.data
          )
        )
      },
      fail(err) {
        reject(new ApiError(0, 'network_error', `网络不可达：${url}（${err.errMsg}）`))
      }
    })
  })
}
