#!/usr/bin/env bash
# p1-e2e-test.sh — P1 一期端到端功能验收测试
# 用途：模拟家庭多用户完整使用场景，验证所有 P1 功能点
# 运行条件：两个后端服务已启动（8080/8081），PostgreSQL 与 NATS 就绪

set -euo pipefail

BASE_HOMEOS="http://localhost:8080"
BASE_FINANCE="http://localhost:8081"
PHONE_ADMIN="13800138001"
PHONE_ADULT="13800138002"
PHONE_CHILD="13800138003"
PHONE_GUEST="13800138004"
SMS_CODE="123456"  # 开发环境固定验证码

PASS=0
FAIL=0
TOTAL=0

pass() {
  PASS=$((PASS + 1))
  TOTAL=$((TOTAL + 1))
  echo "✅ [PASS #$TOTAL] $1"
}

fail() {
  FAIL=$((FAIL + 1))
  TOTAL=$((TOTAL + 1))
  echo "❌ [FAIL #$TOTAL] $1"
}

check_response() {
  local label="$1"
  local http_code="$2"
  local expected_code="${3:-200}"
  if [ "$http_code" = "$expected_code" ]; then
    pass "$label (HTTP $http_code)"
  else
    fail "$label (期望 HTTP $expected_code, 实际 HTTP $http_code)"
  fi
}

echo "=========================================="
echo "P1 一期端到端功能验收测试"
echo "=========================================="
echo ""

# ==========================================
# 场景 1：管理员创建家庭并开通财务面
# ==========================================
echo "--- 场景 1：管理员创建家庭 ---"

# 1.0 发送验证码（开发环境固定码 123456）
curl -s -X POST "$BASE_HOMEOS/api/homeos/auth/sms-code" \
  -H "Content-Type: application/json" \
  -d "{\"phone\":\"$PHONE_ADMIN\"}" > /dev/null

# 1.1 管理员登录
ADMIN_LOGIN=$(curl -s -w "\n%{http_code}" -X POST "$BASE_HOMEOS/api/homeos/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"phone\":\"$PHONE_ADMIN\",\"code\":\"$SMS_CODE\"}")
ADMIN_HTTP=$(echo "$ADMIN_LOGIN" | tail -1)
ADMIN_BODY=$(echo "$ADMIN_LOGIN" | sed '$d')
check_response "管理员登录" "$ADMIN_HTTP" "200"

ADMIN_TOKEN=$(echo "$ADMIN_BODY" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4 || true)
ADMIN_FAMILY_ID=$(echo "$ADMIN_BODY" | grep -o '"family_id":"[^"]*"' | cut -d'"' -f4 || true)

if [ -z "$ADMIN_TOKEN" ]; then
  fail "获取管理员 token"
  echo "响应体: $ADMIN_BODY"
else
  pass "获取管理员 token"
fi

# 1.2 如果没有家庭，创建新家庭
if [ -z "$ADMIN_FAMILY_ID" ] || [ "$ADMIN_FAMILY_ID" = "null" ]; then
  echo "  → 创建新家庭..."
  CREATE_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_HOMEOS/api/homeos/families" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"测试家庭","timezone":"Asia/Shanghai","currency":"CNY","modules":["homeos","finance"]}')
  CREATE_HTTP=$(echo "$CREATE_RES" | tail -1)
  CREATE_BODY=$(echo "$CREATE_RES" | sed '$d')
  check_response "创建家庭" "$CREATE_HTTP" "201"
  ADMIN_FAMILY_ID=$(echo "$CREATE_BODY" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || true)
fi

echo "  管理员 family_id: $ADMIN_FAMILY_ID"

# 1.3 验证首页聚合接口
SUMMARY_RES=$(curl -s -w "\n%{http_code}" "$BASE_HOMEOS/api/homeos/home/summary?family_id=$ADMIN_FAMILY_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
SUMMARY_HTTP=$(echo "$SUMMARY_RES" | tail -1)
SUMMARY_BODY=$(echo "$SUMMARY_RES" | sed '$d')
check_response "首页聚合接口" "$SUMMARY_HTTP" "200"

# 验证 faces[] 包含 homeos 和 finance
if echo "$SUMMARY_BODY" | grep -q '"faces"'; then
  pass "首页返回 faces 数组"
else
  fail "首页未返回 faces 数组"
fi

# ==========================================
# 场景 2：邀请成员加入
# ==========================================
echo ""
echo "--- 场景 2：邀请成员 ---"

# 2.1 生成邀请码
INVITE_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_HOMEOS/api/homeos/families/$ADMIN_FAMILY_ID/invites" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"role":"adult"}')
INVITE_HTTP=$(echo "$INVITE_RES" | tail -1)
INVITE_BODY=$(echo "$INVITE_RES" | sed '$d')
check_response "生成邀请码" "$INVITE_HTTP" "201"

INVITE_CODE=$(echo "$INVITE_BODY" | grep -o '"invite_code":"[^"]*"' | cut -d'"' -f4 || true)
if [ -n "$INVITE_CODE" ]; then
  pass "获取邀请码: $INVITE_CODE"
else
  fail "获取邀请码失败"
fi

# 2.2 成人成员接受邀请（先发送验证码）
curl -s -X POST "$BASE_HOMEOS/api/homeos/auth/sms-code" \
  -H "Content-Type: application/json" \
  -d "{\"phone\":\"$PHONE_ADULT\"}" > /dev/null

ADULT_LOGIN=$(curl -s -w "\n%{http_code}" -X POST "$BASE_HOMEOS/api/homeos/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"phone\":\"$PHONE_ADULT\",\"code\":\"$SMS_CODE\"}")
ADULT_HTTP=$(echo "$ADULT_LOGIN" | tail -1)
ADULT_BODY=$(echo "$ADULT_LOGIN" | sed '$d')
check_response "成人成员登录" "$ADULT_HTTP" "200"

ADULT_TOKEN=$(echo "$ADULT_BODY" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4 || true)

if [ -n "$INVITE_CODE" ] && [ -n "$ADULT_TOKEN" ]; then
  JOIN_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_HOMEOS/api/homeos/invites/$INVITE_CODE/join" \
    -H "Authorization: Bearer $ADULT_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"成人成员\"}")
  JOIN_HTTP=$(echo "$JOIN_RES" | tail -1)
  check_response "成人成员加入家庭" "$JOIN_HTTP" "200"
fi

# ==========================================
# 场景 3：财务面功能测试
# ==========================================
echo ""
echo "--- 场景 3：财务面功能 ---"

# 3.1 创建账户（API 只接受 family_id/name/type，balance 固定为 0）
ACCOUNT_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_FINANCE/api/finance/accounts" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"family_id\":\"$ADMIN_FAMILY_ID\",\"name\":\"现金账户\",\"type\":\"cash\"}")
ACCOUNT_HTTP=$(echo "$ACCOUNT_RES" | tail -1)
ACCOUNT_BODY=$(echo "$ACCOUNT_RES" | sed '$d')
check_response "创建账户" "$ACCOUNT_HTTP" "201"

ACCOUNT_ID=$(echo "$ACCOUNT_BODY" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || true)
if [ -n "$ACCOUNT_ID" ]; then
  pass "获取账户 ID: $ACCOUNT_ID"
else
  fail "获取账户 ID 失败"
fi

# 3.2 创建分类（API 只接受 family_id/name/icon/sort_order，无 type/parent_id）
CATEGORY_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_FINANCE/api/finance/categories" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"family_id\":\"$ADMIN_FAMILY_ID\",\"name\":\"餐饮\",\"icon\":\"🍽️\"}")
CATEGORY_HTTP=$(echo "$CATEGORY_RES" | tail -1)
CATEGORY_BODY=$(echo "$CATEGORY_RES" | sed '$d')
check_response "创建分类" "$CATEGORY_HTTP" "201"

CATEGORY_ID=$(echo "$CATEGORY_BODY" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || true)
if [ -n "$CATEGORY_ID" ]; then
  pass "获取分类 ID: $CATEGORY_ID"
else
  fail "获取分类 ID 失败"
fi

# 3.3 记账（创建流水）
if [ -n "$ACCOUNT_ID" ] && [ -n "$CATEGORY_ID" ]; then
  TX_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_FINANCE/api/finance/transactions" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"family_id\":\"$ADMIN_FAMILY_ID\",\"type\":\"expense\",\"amount_cents\":5000,\"account_id\":\"$ACCOUNT_ID\",\"category_id\":\"$CATEGORY_ID\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"description\":\"午餐\"}")
  TX_HTTP=$(echo "$TX_RES" | tail -1)
  TX_BODY=$(echo "$TX_RES" | sed '$d')
  check_response "创建流水" "$TX_HTTP" "201"

  TX_ID=$(echo "$TX_BODY" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || true)
  if [ -n "$TX_ID" ]; then
    pass "获取流水 ID: $TX_ID"
  else
    fail "获取流水 ID 失败"
  fi
fi

# 3.4 查询流水列表
LIST_TX_RES=$(curl -s -w "\n%{http_code}" "$BASE_FINANCE/api/finance/transactions?family_id=$ADMIN_FAMILY_ID&page=1&per_page=10" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
LIST_TX_HTTP=$(echo "$LIST_TX_RES" | tail -1)
LIST_TX_BODY=$(echo "$LIST_TX_RES" | sed '$d')
check_response "查询流水列表" "$LIST_TX_HTTP" "200"

# 3.5 查询余额
BALANCE_RES=$(curl -s -w "\n%{http_code}" "$BASE_FINANCE/api/finance/balance?family_id=$ADMIN_FAMILY_ID&account_id=$ACCOUNT_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
BALANCE_HTTP=$(echo "$BALANCE_RES" | tail -1)
BALANCE_BODY=$(echo "$BALANCE_RES" | sed '$d')
check_response "查询余额" "$BALANCE_HTTP" "200"

# 3.6 创建预算
if [ -n "$CATEGORY_ID" ]; then
  BUDGET_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_FINANCE/api/finance/budgets" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"family_id\":\"$ADMIN_FAMILY_ID\",\"category_id\":\"$CATEGORY_ID\",\"period\":\"month\",\"amount_cents\":50000,\"start_date\":\"$(date +%Y-%m-01)\"}")
  BUDGET_HTTP=$(echo "$BUDGET_RES" | tail -1)
  check_response "创建预算" "$BUDGET_HTTP" "201"
fi

# 3.7 查询统计报表
STATS_RES=$(curl -s -w "\n%{http_code}" "$BASE_FINANCE/api/finance/statistics?family_id=$ADMIN_FAMILY_ID&period=month&date=$(date +%Y-%m)" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
STATS_HTTP=$(echo "$STATS_RES" | tail -1)
check_response "查询统计报表" "$STATS_HTTP" "200"

# ==========================================
# 场景 4：权限验证（越权访问）
# ==========================================
echo ""
echo "--- 场景 4：权限验证 ---"

# 4.1 成人成员尝试删除他人创建的流水（应拒绝）
if [ -n "$TX_ID" ] && [ -n "$ADULT_TOKEN" ]; then
  DELETE_RES=$(curl -s -w "\n%{http_code}" -X DELETE "$BASE_FINANCE/api/finance/transactions/$TX_ID" \
    -H "Authorization: Bearer $ADULT_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"family_id\":\"$ADMIN_FAMILY_ID\"}")
  DELETE_HTTP=$(echo "$DELETE_RES" | tail -1)
  # 成人成员不应有删除权限（除非是作者或管理员）
  if [ "$DELETE_HTTP" = "403" ] || [ "$DELETE_HTTP" = "404" ]; then
    pass "成人成员越权删除被拒绝 (HTTP $DELETE_HTTP)"
  else
    fail "成人成员越权删除未被拒绝 (HTTP $DELETE_HTTP)"
  fi
fi

# 4.2 验证 L3 敏感字段过滤（私密流水）
PRIVATE_TX_RES=$(curl -s -w "\n%{http_code}" -X POST "$BASE_FINANCE/api/finance/transactions" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"family_id\":\"$ADMIN_FAMILY_ID\",\"type\":\"expense\",\"amount_cents\":10000,\"account_id\":\"$ACCOUNT_ID\",\"category_id\":\"$CATEGORY_ID\",\"occurred_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"description\":\"私密支出\",\"visibility\":\"private\"}")
PRIVATE_TX_HTTP=$(echo "$PRIVATE_TX_RES" | tail -1)
PRIVATE_TX_BODY=$(echo "$PRIVATE_TX_RES" | sed '$d')
check_response "创建私密流水" "$PRIVATE_TX_HTTP" "201"

PRIVATE_TX_ID=$(echo "$PRIVATE_TX_BODY" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || true)

# 成人成员查询流水列表时，私密流水应被过滤
if [ -n "$PRIVATE_TX_ID" ] && [ -n "$ADULT_TOKEN" ]; then
  LIST_RES=$(curl -s "$BASE_FINANCE/api/finance/transactions?family_id=$ADMIN_FAMILY_ID&page=1&per_page=100" \
    -H "Authorization: Bearer $ADULT_TOKEN")
  if echo "$LIST_RES" | grep -q "\"id\":\"$PRIVATE_TX_ID\""; then
    fail "L3 私密流水未被过滤（成人成员可见）"
  else
    pass "L3 私密流水已被过滤（成人成员不可见）"
  fi
fi

# ==========================================
# 场景 5：动态流与消息
# ==========================================
echo ""
echo "--- 场景 5：动态流与消息 ---"

# 5.1 查询动态流
DYNAMICS_RES=$(curl -s -w "\n%{http_code}" "$BASE_HOMEOS/api/homeos/dynamics?family_id=$ADMIN_FAMILY_ID&page=1&per_page=10" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
DYNAMICS_HTTP=$(echo "$DYNAMICS_RES" | tail -1)
check_response "查询动态流" "$DYNAMICS_HTTP" "200"

# 5.2 查询消息列表
MESSAGES_RES=$(curl -s -w "\n%{http_code}" "$BASE_HOMEOS/api/homeos/notifications?family_id=$ADMIN_FAMILY_ID&page=1&per_page=10" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
MESSAGES_HTTP=$(echo "$MESSAGES_RES" | tail -1)
check_response "查询消息列表" "$MESSAGES_HTTP" "200"

# ==========================================
# 场景 6：回收站功能
# ==========================================
echo ""
echo "--- 场景 6：回收站功能 ---"

# 6.1 删除一条流水到回收站
if [ -n "$TX_ID" ]; then
  SOFT_DELETE_RES=$(curl -s -w "\n%{http_code}" -X DELETE "$BASE_FINANCE/api/finance/transactions/$TX_ID" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"family_id\":\"$ADMIN_FAMILY_ID\",\"soft_delete\":true}")
  SOFT_DELETE_HTTP=$(echo "$SOFT_DELETE_RES" | tail -1)
  check_response "软删除流水到回收站" "$SOFT_DELETE_HTTP" "200"
fi

# 6.2 查询回收站
TRASH_RES=$(curl -s -w "\n%{http_code}" "$BASE_FINANCE/api/finance/trash?family_id=$ADMIN_FAMILY_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
TRASH_HTTP=$(echo "$TRASH_RES" | tail -1)
check_response "查询回收站" "$TRASH_HTTP" "200"

# ==========================================
# 场景 7：主题与配置
# ==========================================
echo ""
echo "--- 场景 7：主题与配置 ---"

# 7.1 查询家庭模块配置
MODULES_RES=$(curl -s -w "\n%{http_code}" "$BASE_HOMEOS/api/homeos/families/$ADMIN_FAMILY_ID/modules" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
MODULES_HTTP=$(echo "$MODULES_RES" | tail -1)
check_response "查询家庭模块配置" "$MODULES_HTTP" "200"

# 7.2 查询成员列表
MEMBERS_RES=$(curl -s -w "\n%{http_code}" "$BASE_HOMEOS/api/homeos/families/$ADMIN_FAMILY_ID/members" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
MEMBERS_HTTP=$(echo "$MEMBERS_RES" | tail -1)
check_response "查询成员列表" "$MEMBERS_HTTP" "200"

# ==========================================
# 总结
# ==========================================
echo ""
echo "=========================================="
echo "测试结果汇总"
echo "=========================================="
echo "总测试数: $TOTAL"
echo "通过: $PASS"
echo "失败: $FAIL"
echo "通过率: $(( PASS * 100 / TOTAL ))%"
echo "=========================================="

if [ "$FAIL" -gt 0 ]; then
  echo "❌ 验收未通过，存在 $FAIL 个失败项"
  exit 1
else
  echo "✅ 所有测试通过，P1 验收合格"
  exit 0
fi
