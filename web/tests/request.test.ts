// 请求层的可离线断言部分：单 base URL 之下的 /api/{code}/* 拼装（17.7 第 5 条）。
// 跑法：npm run check:request（node 直接剥类型，不需要 vite；真机联调在 S5/S7 之后）。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { apiUrl, ApiError } from '../src/api/request.ts'

test('默认同源：base 为空串，请求只落在 /api/{code}/ 之下', () => {
  assert.equal(apiUrl('finance', '/transactions'), '/api/finance/transactions')
  assert.equal(apiUrl('homeos', '/home/summary'), '/api/homeos/home/summary')
})

test('注入单一 base URL 后仍是同一个 host，前缀按 code 拼', () => {
  assert.equal(apiUrl('finance', '/transactions', undefined, 'https://hc.example.com'), 'https://hc.example.com/api/finance/transactions')
  // base 末尾多写的 / 被收掉，不产生 //api
  assert.equal(apiUrl('finance', '/transactions', undefined, 'https://hc.example.com/'), 'https://hc.example.com/api/finance/transactions')
})

test('query 拼接：跳过 undefined、按 URL 编码、保持传入顺序', () => {
  assert.equal(
    apiUrl('finance', '/transactions', { period: '2026-Q3', account_id: 7, q: 'a b', skip: undefined }),
    '/api/finance/transactions?period=2026-Q3&account_id=7&q=a%20b'
  )
  assert.equal(apiUrl('homeos', '/search', {}), '/api/homeos/search')
})

test('拒绝自带 /api 前缀（这是「客户端持有各服务地址」的开端）', () => {
  assert.throws(() => apiUrl('finance', '/api/finance/transactions'), ApiError)
})

test('拒绝不以 / 开头的路径', () => {
  assert.throws(() => apiUrl('finance', 'transactions'), ApiError)
})

test('code 只校验形状，不在客户端持有任何 code 清单', () => {
  // 未出生域的 code 同样能拼出前缀（真正拦它的门禁是 pages.json 三查与后端路由表），
  // 这里不出现一份合法 code 的枚举表，正是 §1.3「一个 code 的唯一真源在 registry」。
  assert.equal(apiUrl('finance_report', '/x'), '/api/finance_report/x')
  assert.throws(() => apiUrl('Finance', '/x'), ApiError)
  assert.throws(() => apiUrl('', '/x'), ApiError)
  assert.throws(() => apiUrl('/finance', '/x'), ApiError)
})
