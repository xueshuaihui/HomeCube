// HTTP request wrapper with token management

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  params?: Record<string, any>
  data?: any
  header?: Record<string, string>
}

interface ResponseData<T = any> {
  code?: number
  message?: string
  data: T
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

    if (response.statusCode === 200 && response.data?.data?.access_token) {
      uni.setStorageSync('access_token', response.data.data.access_token)
      if (response.data.data.refresh_token) {
        uni.setStorageSync('refresh_token', response.data.data.refresh_token)
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
 */
export async function request<T = any>(
  url: string,
  options: RequestOptions = {}
): Promise<ResponseData<T>> {
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
                    resolve(retryRes.data as ResponseData<T>)
                  } else {
                    reject(new Error(`Request failed: HTTP ${retryRes.statusCode}`))
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
          resolve(res.data as ResponseData<T>)
        } else {
          const errorMsg = (res.data as any)?.message || `Request failed: HTTP ${statusCode}`
          reject(new Error(errorMsg))
        }
      },
      fail: (err) => {
        reject(new Error(err.errMsg || 'Network error'))
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
