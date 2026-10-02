#!/bin/bash
# =============================================================================
# HomeCube · Health Check Test Script
#
# Tests the health probe requirements:
# 1. /api/homeos/healthz through nginx returns 200
# 2. Five unborn prefixes return "即将上线" (404)
# 3. Stop nats → homeos/finance /healthz becomes 503, recover → 200
#
# Usage: ./test-healthz.sh [nginx_port]
#   nginx_port defaults to 80 from env.local
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/env.local"

# Load environment variables
if [ -f "$ENV_FILE" ]; then
    set -a
    source "$ENV_FILE"
    set +a
fi

NGINX_PORT="${NGINX_HTTP_PORT:-80}"
BASE_URL="http://127.0.0.1:${NGINX_PORT}"

echo "=========================================="
echo "HomeCube Health Check Tests"
echo "=========================================="
echo "Base URL: ${BASE_URL}"
echo ""

# Color codes for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counter
PASS=0
FAIL=0

# Helper function to test HTTP endpoint
test_endpoint() {
    local url="$1"
    local expected_code="$2"
    local description="$3"
    local expect_body="$4"  # Optional: expected substring in response body

    echo -n "Testing ${description}... "

    # Make request and capture response
    local http_code
    local body
    http_code=$(curl -s -o /tmp/response_body.txt -w "%{http_code}" "$url" 2>/dev/null || echo "000")
    body=$(cat /tmp/response_body.txt)

    if [ "$http_code" = "$expected_code" ]; then
        if [ -z "$expect_body" ] || echo "$body" | grep -q "$expect_body"; then
            echo -e "${GREEN}✓ PASS${NC} (HTTP ${http_code})"
            ((PASS++))
            return 0
        else
            echo -e "${RED}✗ FAIL${NC} (Expected body '${expect_body}' not found)"
            echo "  Response: ${body}"
            ((FAIL++))
            return 1
        fi
    else
        echo -e "${RED}✗ FAIL${NC} (Expected HTTP ${expected_code}, got ${http_code})"
        echo "  Response: ${body}"
        ((FAIL++))
        return 1
    fi
}

echo "--- Test 1: Health probes through nginx ---"
test_endpoint "${BASE_URL}/api/homeos/healthz" "200" "homeos healthz"
test_endpoint "${BASE_URL}/api/finance/healthz" "200" "finance healthz"
echo ""

echo "--- Test 2: Unborn prefixes return '即将上线' ---"
# The five unborn domains from registry: purchase, diet, trip, kin, growth
UNBORN_DOMAINS=("purchase" "diet" "trip" "kin" "growth")
for domain in "${UNBORN_DOMAINS[@]}"; do
    test_endpoint "${BASE_URL}/api/${domain}/test" "404" "${domain} prefix" "即将上线"
done
echo ""

echo "--- Test 3: NATS dependency test ---"
echo -e "${YELLOW}Note: This test requires manual intervention${NC}"
echo "Step 1: Current state (should be 200):"
test_endpoint "${BASE_URL}/api/homeos/healthz" "200" "homeos healthz before stop"
test_endpoint "${BASE_URL}/api/finance/healthz" "200" "finance healthz before stop"

echo ""
echo "Step 2: Stopping NATS container..."
docker compose --env-file "$ENV_FILE" -f "${SCRIPT_DIR}/docker-compose.yml" stop nats
sleep 3

echo "Step 3: After stopping NATS (should be 503):"
test_endpoint "${BASE_URL}/api/homeos/healthz" "503" "homeos healthz after nats stop" || true
test_endpoint "${BASE_URL}/api/finance/healthz" "503" "finance healthz after nats stop" || true

echo ""
echo "Step 4: Restarting NATS container..."
docker compose --env-file "$ENV_FILE" -f "${SCRIPT_DIR}/docker-compose.yml" start nats
sleep 5

echo "Step 5: After restarting NATS (should be 200 again):"
test_endpoint "${BASE_URL}/api/homeos/healthz" "200" "homeos healthz after nats restart"
test_endpoint "${BASE_URL}/api/finance/healthz" "200" "finance healthz after nats restart"
echo ""

echo "=========================================="
echo "Test Summary"
echo "=========================================="
echo -e "Passed: ${GREEN}${PASS}${NC}"
echo -e "Failed: ${RED}${FAIL}${NC}"
echo ""

if [ $FAIL -eq 0 ]; then
    echo -e "${GREEN}All tests passed! ✓${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed. ✗${NC}"
    exit 1
fi
