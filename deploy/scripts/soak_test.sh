#!/bin/bash
set -euo pipefail

# =============================================================================
# HomeCube P1 Long Pressure Test (Soak Test)
#
# Usage: ./soak_test.sh <FAMILY_ID> <DURATION_HOURS> <TARGET_REQUESTS>
#
# This script performs a long-duration pressure test on the HomeOS and Finance
# services, meeting the requirements of PRD 18.2#13:
#   - Duration >= 24 hours
#   - Net samples >= 100,000 requests
#   - SLO: P95 <= 5s, P99 <= 60s, Availability >= 99.9%
#
# Uses a dedicated load testing family (fixed family_id) that is independent
# from production data and not included in daily reconciliation.
# =============================================================================

if [ $# -lt 3 ]; then
    echo "Usage: $0 <FAMILY_ID> <DURATION_HOURS> <TARGET_REQUESTS>"
    echo ""
    echo "Example:"
    echo "  $0 00000000-0000-0000-0000-000000000001 24 100000"
    exit 1
fi

FAMILY_ID=$1
DURATION_HOURS=$2
TARGET_REQUESTS=$3

START_TIME=$(date +%s)
END_TIME=$((START_TIME + DURATION_HOURS * 3600))
REQUEST_COUNT=0
SUCCESS_COUNT=0
ERROR_COUNT=0
TOTAL_LATENCY=0
LATENCY_SAMPLES=""

BASE_URL="http://localhost:8080"

# Generate test transaction data
generate_transaction() {
    local amount=$((RANDOM % 10000 + 100))
    local types=("expense" "income" "transfer")
    local type=${types[$((RANDOM % 3))]}
    local account_uuid
    local category_uuid
    
    # Generate UUIDs using uuidgen if available, otherwise use a simple method
    if command -v uuidgen >/dev/null 2>&1; then
        account_uuid=$(uuidgen)
        category_uuid=$(uuidgen)
    else
        account_uuid="acc-$(date +%s)-$((RANDOM % 1000))"
        category_uuid="cat-$(date +%s)-$((RANDOM % 1000))"
    fi
    
    cat <<EOF
{
    "family_id": "${FAMILY_ID}",
    "type": "${type}",
    "amount_cents": ${amount},
    "account_id": "${account_uuid}",
    "category_id": "${category_uuid}",
    "occurred_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "description": "Soak test transaction #${REQUEST_COUNT}"
}
EOF
}

echo "=== Soak Test Started ==="
echo "Family ID: ${FAMILY_ID}"
echo "Duration: ${DURATION_HOURS} hours"
echo "Target Requests: ${TARGET_REQUESTS}"
echo "Start Time: $(date)"
echo "Base URL: ${BASE_URL}"
echo ""

while [ $(date +%s) -lt ${END_TIME} ] && [ ${REQUEST_COUNT} -lt ${TARGET_REQUESTS} ]; do
    REQUEST_COUNT=$((REQUEST_COUNT + 1))
    
    # Test homeos service - get family modules
    HOMEOS_START=$(date +%s%N)
    HOMEOS_RESPONSE=$(curl -s -w "%{http_code}" -o /tmp/homeos_response.json \
        -H "Authorization: Bearer test-token" \
        "${BASE_URL}/api/homeos/family/modules?family_id=${FAMILY_ID}" 2>/dev/null || echo "000")
    HOMEOS_END=$(date +%s%N)
    HOMEOS_LATENCY=$(( (HOMEOS_END - HOMEOS_START) / 1000000 ))
    
    if [ "${HOMEOS_RESPONSE}" = "200" ] || [ "${HOMEOS_RESPONSE}" = "201" ]; then
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
        TOTAL_LATENCY=$((TOTAL_LATENCY + HOMEOS_LATENCY))
        LATENCY_SAMPLES="${LATENCY_SAMPLES} ${HOMEOS_LATENCY}"
    else
        ERROR_COUNT=$((ERROR_COUNT + 1))
    fi
    
    # Test finance service - create transaction
    TRANSACTION_DATA=$(generate_transaction)
    FINANCE_START=$(date +%s%N)
    FINANCE_RESPONSE=$(curl -s -w "%{http_code}" -o /tmp/finance_response.json \
        -X POST \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer test-token" \
        -d "${TRANSACTION_DATA}" \
        "${BASE_URL}/api/finance/transactions" 2>/dev/null || echo "000")
    FINANCE_END=$(date +%s%N)
    FINANCE_LATENCY=$(( (FINANCE_END - FINANCE_START) / 1000000 ))
    
    if [ "${FINANCE_RESPONSE}" = "200" ] || [ "${FINANCE_RESPONSE}" = "201" ]; then
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
        TOTAL_LATENCY=$((TOTAL_LATENCY + FINANCE_LATENCY))
        LATENCY_SAMPLES="${LATENCY_SAMPLES} ${FINANCE_LATENCY}"
    else
        ERROR_COUNT=$((ERROR_COUNT + 1))
    fi
    
    # Progress report every 1000 requests
    if [ $((REQUEST_COUNT % 1000)) -eq 0 ]; then
        ELAPSED=$(( $(date +%s) - START_TIME ))
        if [ ${ELAPSED} -gt 0 ]; then
            RATE=$((REQUEST_COUNT * 3600 / ELAPSED))
        else
            RATE=0
        fi
        
        AVG_LATENCY=0
        if [ ${SUCCESS_COUNT} -gt 0 ]; then
            AVG_LATENCY=$((TOTAL_LATENCY / SUCCESS_COUNT))
        fi
        
        echo "[$(date)] Requests: ${REQUEST_COUNT} | Success: ${SUCCESS_COUNT} | Errors: ${ERROR_COUNT} | Rate: ${RATE}/h | Avg Latency: ${AVG_LATENCY}ms"
    fi
    
    # Small delay to avoid overwhelming the services
    sleep 0.1
done

END_TIME_ACTUAL=$(date +%s)
TOTAL_DURATION=$((END_TIME_ACTUAL - START_TIME))
DURATION_HOURS_ACTUAL=$((TOTAL_DURATION / 3600))
DURATION_MINUTES_ACTUAL=$(( (TOTAL_DURATION % 3600) / 60 ))

# Calculate availability
if [ $((SUCCESS_COUNT + ERROR_COUNT)) -gt 0 ]; then
    AVAILABILITY=$(awk "BEGIN {printf \"%.4f\", ${SUCCESS_COUNT} * 100.0 / (${SUCCESS_COUNT} + ${ERROR_COUNT})}")
else
    AVAILABILITY="0.0000"
fi

# Calculate average latency
if [ ${SUCCESS_COUNT} -gt 0 ]; then
    AVG_LATENCY=$((TOTAL_LATENCY / SUCCESS_COUNT))
else
    AVG_LATENCY=0
fi

# Calculate P95 and P99 latencies (approximate from sorted samples)
P95_LATENCY=0
P99_LATENCY=0
if [ ${SUCCESS_COUNT} -gt 0 ] && [ -n "${LATENCY_SAMPLES}" ]; then
    SORTED_LATENCIES=$(echo ${LATENCY_SAMPLES} | tr ' ' '\n' | sort -n)
    SAMPLE_COUNT=$(echo "${SORTED_LATENCIES}" | wc -l | tr -d ' ')
    
    P95_INDEX=$(( (SAMPLE_COUNT * 95 + 99) / 100 ))
    P99_INDEX=$(( (SAMPLE_COUNT * 99 + 99) / 100 ))
    
    if [ ${P95_INDEX} -le 0 ]; then P95_INDEX=1; fi
    if [ ${P99_INDEX} -le 0 ]; then P99_INDEX=1; fi
    if [ ${P95_INDEX} -gt ${SAMPLE_COUNT} ]; then P95_INDEX=${SAMPLE_COUNT}; fi
    if [ ${P99_INDEX} -gt ${SAMPLE_COUNT} ]; then P99_INDEX=${SAMPLE_COUNT}; fi
    
    P95_LATENCY=$(echo "${SORTED_LATENCIES}" | sed -n "${P95_INDEX}p")
    P99_LATENCY=$(echo "${SORTED_LATENCIES}" | sed -n "${P99_INDEX}p")
fi

echo ""
echo "=== Soak Test Completed ==="
echo "Total Duration: ${DURATION_HOURS_ACTUAL}h ${DURATION_MINUTES_ACTUAL}m (${TOTAL_DURATION}s)"
echo "Total Requests: ${REQUEST_COUNT}"
echo "Success: ${SUCCESS_COUNT}"
echo "Errors: ${ERROR_COUNT}"
echo "Availability: ${AVAILABILITY}%"
echo "Average Latency: ${AVG_LATENCY}ms"
echo "P95 Latency: ${P95_LATENCY}ms"
echo "P99 Latency: ${P99_LATENCY}ms"
echo ""
echo "=== SLO Evaluation ==="
echo "Target Availability: 99.9%"
echo "Target P95 Latency: 5000ms"
echo "Target P99 Latency: 60000ms"
echo ""

# Check SLO compliance
SLO_MET=true

if awk "BEGIN {exit !(${AVAILABILITY} < 99.9)}"; then
    echo "❌ SLO NOT MET: Availability ${AVAILABILITY}% < 99.9%"
    SLO_MET=false
else
    echo "✅ SLO MET: Availability ${AVAILABILITY}% >= 99.9%"
fi

if [ ${P95_LATENCY} -gt 5000 ]; then
    echo "❌ SLO NOT MET: P95 Latency ${P95_LATENCY}ms > 5000ms"
    SLO_MET=false
else
    echo "✅ SLO MET: P95 Latency ${P95_LATENCY}ms <= 5000ms"
fi

if [ ${P99_LATENCY} -gt 60000 ]; then
    echo "❌ SLO NOT MET: P99 Latency ${P99_LATENCY}ms > 60000ms"
    SLO_MET=false
else
    echo "✅ SLO MET: P99 Latency ${P99_LATENCY}ms <= 60000ms"
fi

echo ""
if [ "${SLO_MET}" = true ]; then
    echo "🎉 All SLOs MET!"
    exit 0
else
    echo "⚠️  Some SLOs NOT MET"
    exit 1
fi
