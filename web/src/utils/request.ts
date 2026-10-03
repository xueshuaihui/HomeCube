// HTTP request wrapper with token management

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  params?: Record<string, any>
  data?: any
  header?: Record<string, string>
}

/**
 * 只在 `unwrapBody` 里出现：工程早期的响应形状 `{code,message,data}`。
 * 请求层的**出口契约不是它** —— 见下方 `request` 的返回类型。
 */
interface ResponseData<T = any> {
  code?: number
  message?: string
  data: T
}

/**
 * 带 HTTP 状态码的请求错误：调用方需要区分 409（乐观锁冲突）与 403/400 之类，
 * 单靠 message 里的「HTTP 409」字符串判状态等于把状态码再解析一遍。
 */
export class RequestError extends Error {
  readonly status: number
  readonly body: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'RequestError'
    this.status = status
    this.body = body
  }
}

/**
 * 取响应体。
 *
 * 冻结契约（`server/contracts/openapi/*.yaml`）里 200 的 schema **就是业务对象本身**
 * （`home/summary` 直接是 `{family, due_today, faces, dynamics, unread}`），
 * 服务端 handler 也是 `c.JSON(200, XxxResponse{...})` 裸回，不套信封；
 * 但工程早期的请求层类型把响应声明成了 `{code,message,data}`。两种形状都收、
 * 由调用方按契约字段取值，而不是赌某一种信封（同 family/invite 页的 unwrap 口径）。
 */
export function unwrapBody<T = any>(res: any): T {
  if (res && typeof res === 'object' && 'data' in res) {
    const inner = (res as ResponseData).data
    // 只有 inner 真是业务对象时才剥一层；`{data: null}` 之类不剥，避免把空响应当成对象根。
    if (inner && typeof inner === 'object') return inner as T
  }
  return res as T
}

/**
 * Get stored access token
 */
function getAccessToken(): string | null {
  try {
    const token = uni.getStorageSync('access_token')
    return token || null
  } catch (e) {
    return null
  }
}

/**
 * Refresh access token using refresh token
 */
async function refreshAccessToken(): Promise<boolean> {
  try {
    const refreshToken = uni.getStorageSync('refresh_token')
    if (!refreshToken) return false

    const baseUrl = import.meta.env?.VITE_API_BASE_URL || ''
    const response = await new Promise<any>((resolve, reject) => {
      uni.request({
        url: `${baseUrl}/api/homeos/auth/refresh`,
        method: 'POST',
        header: {
          'Content-Type': 'application/json',
        },
        data: {
          refresh_token: refreshToken,
        },
        success: resolve,
        fail: reject,
      })
    })

    // 契约与服务端一致地**裸回** `{access_token, refresh_token}`（svc-homeos handler
    // `c.JSON(200, RefreshResponse{...})`）；早前的实现只认 `{data:{access_token}}`，
    // 于是 refresh 永远「失败」→ 清 token → 弹回登录页。两种形状都收。
    const body = unwrapBody<any>(response?.statusCode === 200 ? response.data : null)
    if (body?.access_token) {
      uni.setStorageSync('access_token', body.access_token)
      if (body.refresh_token) {
        uni.setStorageSync('refresh_token', body.refresh_token)
      }
      return true
    }

    return false
  } catch (e) {
    console.error('Token refresh failed:', e)
    return false
  }
}

/**
 * Main request function
 *
 * **出口契约（唯一的信封判定处）**：resolve 出来的是 `res.data`，即**裸响应体本身**。
 * 冻结契约 `server/contracts/openapi/*.yaml` 里 200 的 schema 就是业务对象
 * （列表是 `{items, next_cursor}`、详情是对象本体），服务端 handler 也一律
 * `c.JSON(200, gin.H{"items": ...})` 裸回，不套 `{code,message,data}`。
 * 因此调用方**直接读 `body.items`**，不得再 `.data` 剥一层 —— 那读到的是 `undefined`
 * （旧代码这么写过，于是财务列表恒空、记账保存被自己卡死）。
 * 需要兼容早期两种形状的历史调用点用 `unwrapBody`，它对本契约是恒等变换。
 */
export async function request<T = any>(
  url: string,
  options: RequestOptions = {}
): Promise<T> {
  const {
    method = 'GET',
    params,
    data,
    header = {},
  } = options

  // Build URL with query params
  let fullUrl = url
  if (params) {
    const queryString = Object.entries(params)
      .filter(([_, v]) => v !== undefined && v !== null)
      .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
      .join('&')
    if (queryString) {
      fullUrl += (url.includes('?') ? '&' : '?') + queryString
    }
  }

  // Get base URL from env
  const baseUrl = import.meta.env?.VITE_API_BASE_URL || ''
  const normalizedBase = baseUrl.replace(/\/+$/, '')
  const requestUrl = fullUrl.startsWith('http') ? fullUrl : `${normalizedBase}${fullUrl}`

  // Get access token
  const accessToken = getAccessToken()

  // Build headers
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...header,
  }

  if (accessToken) {
    headers['Authorization'] = `Bearer ${accessToken}`
  }

  // Make request
  return new Promise((resolve, reject) => {
    uni.request({
      url: requestUrl,
      method,
      header: headers,
      data: data || undefined,
      success: async (res) => {
        const statusCode = res.statusCode

        // Handle 401 Unauthorized - try to refresh token
        if (statusCode === 401) {
          const refreshed = await refreshAccessToken()
          if (refreshed) {
            // Retry with new token
            const newToken = getAccessToken()
            if (newToken) {
              headers['Authorization'] = `Bearer ${newToken}`
              uni.request({
                url: requestUrl,
                method,
                header: headers,
                data: data || undefined,
                success: (retryRes) => {
                  if (retryRes.statusCode >= 200 && retryRes.statusCode < 300) {
                    resolve(retryRes.data as T)
                  } else {
                    reject(
                      new RequestError(
                        retryRes.statusCode,
                        (retryRes.data as any)?.message || `Request failed: HTTP ${retryRes.statusCode}`,
                        retryRes.data
                      )
                    )
                  }
                },
                fail: (err) => reject(new Error(err.errMsg)),
              })
              return
            }
          }

          // Refresh failed or no refresh token - redirect to login
          uni.removeStorageSync('access_token')
          uni.removeStorageSync('refresh_token')
          uni.redirectTo({ url: '/pages/homeos/auth/login' })
          reject(new Error('Unauthorized'))
          return
        }

        // Handle other status codes
        if (statusCode >= 200 && statusCode < 300) {
          resolve(res.data as T)
        } else {
          const body = res.data as { code?: string; message?: string; error?: string } | undefined
          const errorMsg =
            body?.message || body?.error || `Request failed: HTTP ${statusCode}`
          reject(new RequestError(statusCode, errorMsg, res.data))
        }
      },
      fail: (err) => {
        reject(new RequestError(0, err.errMsg || 'Network error'))
      },
    })
  })
}

// Convenience methods
request.get = <T = any>(url: string, options?: Omit<RequestOptions, 'method' | 'data'>) =>
  request<T>(url, { ...options, method: 'GET' })

request.post = <T = any>(url: string, data?: any, options?: Omit<RequestOptions, 'method' | 'data'>) =>
  request<T>(url, { ...options, method: 'POST', data })

request.put = <T = any>(url: string, data?: any, options?: Omit<RequestOptions, 'method' | 'data'>) =>
  request<T>(url, { ...options, method: 'PUT', data })

request.delete = <T = any>(url: string, options?: Omit<RequestOptions, 'method' | 'data'>) =>
  request<T>(url, { ...options, method: 'DELETE' })
