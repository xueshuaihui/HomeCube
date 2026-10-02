#!/bin/bash
set -euo pipefail

BASE_URL="http://localhost:8080"
FAMILY_ID="00000000-0000-0000-0000-000000000001"
ACCOUNT_ID="test-account-001"
TOKEN="test-jwt-token"

echo "=== Device Lock & L3 Unlock Check ==="
echo "Start Time: $(date)"

PASS_COUNT=0
FAIL_COUNT=0

# Test 1: Access L3 fields without unlock
echo ""
echo "--- Test 1: Access L3 fields without unlock ---"
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $TOKEN" \
    "$BASE_URL/api/homeos/members?family_id=$FAMILY_ID")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

# Check if L3 fields are empty or redacted
if echo "$BODY" | grep -q '"blood_type":""' || ! echo "$BODY" | grep -q 'blood_type'; then
    echo "✅ L3 fields properly hidden without unlock"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ L3 fields exposed without unlock"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# Test 2: Unlock device with correct PIN
echo ""
echo "--- Test 2: Unlock device with correct PIN ---"
UNLOCK_RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d '{"pin": "123456", "device_id": "test-device-001"}' \
    "$BASE_URL/api/homeos/security/unlock")
UNLOCK_CODE=$(echo "$UNLOCK_RESPONSE" | tail -1)

if [ "$UNLOCK_CODE" -eq 200 ]; then
    echo "✅ Device unlocked successfully"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ Device unlock failed (HTTP $UNLOCK_CODE)"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# Test 3: Access L3 fields after unlock
echo ""
echo "--- Test 3: Access L3 fields after unlock ---"
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $TOKEN" \
    "$BASE_URL/api/homeos/members?family_id=$FAMILY_ID&include_l3=true")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

if echo "$BODY" | grep -q '"blood_type":"A"'; then
    echo "✅ L3 fields accessible after unlock"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ L3 fields still hidden after unlock"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# Test 4: PIN failure lockout (5 consecutive failures)
echo ""
echo "--- Test 4: PIN failure lockout ---"
for i in $(seq 1 5); do
    curl -s -o /dev/null \
        -X POST \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TOKEN" \
        -d '{"pin": "wrong", "device_id": "test-device-001"}' \
        "$BASE_URL/api/homeos/security/unlock"
done

# 6th attempt should be blocked or delayed
START_TIME=$(date +%s)
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d '{"pin": "123456", "device_id": "test-device-001"}' \
    "$BASE_URL/api/homeos/security/unlock")
END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))
HTTP_CODE=$(echo "$RESPONSE" | tail -1)

if [ "$ELAPSED" -ge 2 ] || [ "$HTTP_CODE" -eq 429 ]; then
    echo "✅ PIN lockout triggered (elapsed: ${ELAPSED}s, HTTP: $HTTP_CODE)"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ PIN lockout not triggered (elapsed: ${ELAPSED}s, HTTP: $HTTP_CODE)"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# Test 5: Cross-device access requires re-unlock
echo ""
echo "--- Test 5: Cross-device access requires re-unlock ---"
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $TOKEN" \
    "$BASE_URL/api/homeos/members?family_id=$FAMILY_ID&include_l3=true&device_id=different-device")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

if echo "$BODY" | grep -q '"blood_type":""' || ! echo "$BODY" | grep -q 'blood_type'; then
    echo "✅ Cross-device access requires re-unlock"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ Cross-device access bypassed unlock requirement"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

echo ""
echo "=== Device Lock Check Completed ==="
echo "Total Tests: $((PASS_COUNT + FAIL_COUNT))"
echo "Passed: $PASS_COUNT"
echo "Failed: $FAIL_COUNT"

if [ $PASS_COUNT -eq 5 ]; then
    echo "✅ All device lock checks passed"
    exit 0
else
    echo "❌ Some device lock checks failed"
    exit 1
fi
