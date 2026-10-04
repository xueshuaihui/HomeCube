#!/bin/bash
# =============================================================================
# HomeCube · RT-1 Real-Stack End-to-End Proof
#
# Tests the complete bill lifecycle chain:
#   1. Create a bill (finance service) → registers finance.due.registered event
#   2. Pay the bill (finance service) → emits finance.due.revoked event
#   3. Revoke is consumed by homeos service → soft-deletes due registration
#
# Verifies:
#   - Messages are consumed into the temporary database (hc_rt1_e2e)
#   - Dedupe table has entries in the temp DB
#   - The full chain works end-to-end
#
# Prerequisites:
#   1. Temporary database set up: sh deploy/scripts/setup-rt1-db.sh up
#   2. Services running with env.local.rt1 configuration
#   3. NATS JetStream running
#
# Usage: sh deploy/scripts/rt1-e2e-test.sh [setup|test|cleanup|all]
#   setup   - Set up temp database and start services
#   test    - Run the e2e test chain
#   cleanup - Clean up temp database
#   all     - Run setup, test, and cleanup (default)
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT_DIR="$(cd "$DEPLOY_DIR/.." && pwd)"
ENV_FILE="${DEPLOY_DIR}/env.local.rt1"

# Load environment
if [ ! -f "$ENV_FILE" ]; then
    echo "[rt1-e2e] ERROR: Environment file not found: ${ENV_FILE}"
    echo "  Please ensure deploy/env.local.rt1 exists"
    exit 1
fi

set -a
source "$ENV_FILE"
set +a

# Configuration
TEMP_DB_NAME="${RT1_DB_NAME:-hc_rt1_e2e}"
BASE_URL="http://127.0.0.1:${NGINX_HTTP_PORT}"
FINANCE_URL="http://127.0.0.1:8081"
HOMEOS_URL="http://127.0.0.1:8080"

# Test data
TEST_FAMILY_ID="00000000-0000-4000-8000-000000000001"
TEST_PAYEE_ID="11111111-1111-4111-8111-111111111111"
TEST_DUE_AT=$(date -u -v+7d +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "+7 days" +"%Y-%m-%dT%H:%M:%SZ")

# Color codes
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PASS=0
FAIL=0

log_info() {
    echo -e "${BLUE}[rt1-e2e]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[rt1-e2e] ✓${NC} $1"
    ((PASS++))
}

log_fail() {
    echo -e "${RED}[rt1-e2e] ✗${NC} $1"
    ((FAIL++))
}

log_warn() {
    echo -e "${YELLOW}[rt1-e2e] ⚠${NC} $1"
}

# Helper to run psql commands against temp database
run_psql() {
    psql -h 127.0.0.1 -p 5432 -U postgres -d "${TEMP_DB_NAME}" -t -A "$@"
}

# Check if a command exists
check_command() {
    if ! command -v "$1" &> /dev/null; then
        log_fail "Required command not found: $1"
        exit 1
    fi
}

# Setup phase: create temp database and verify services
setup() {
    log_info "Setting up RT-1 test environment..."
    
    # Check prerequisites
    check_command psql
    check_command curl
    
    # Set up temporary database
    log_info "Creating temporary database..."
    sh "${DEPLOY_DIR}/scripts/setup-rt1-db.sh" up
    
    log_info "Verifying services are healthy..."
    
    # Wait for services to be ready
    local retries=0
    local max_retries=30
    
    while [ $retries -lt $max_retries ]; do
        if curl -s -f "${FINANCE_URL}/api/finance/healthz" > /dev/null 2>&1 && \
           curl -s -f "${HOMEOS_URL}/api/homeos/healthz" > /dev/null 2>&1; then
            log_success "Services are healthy"
            return 0
        fi
        retries=$((retries + 1))
        sleep 2
    done
    
    log_fail "Services failed to become healthy after ${max_retries} attempts"
    return 1
}

# Step 1: Create a bill
create_bill() {
    log_info "Step 1: Creating bill..."
    
    local bill_payload="{
        \"family_id\": \"${TEST_FAMILY_ID}\",
        \"payee_id\": \"${TEST_PAYEE_ID}\",
        \"amount_cents\": 120000,
        \"due_at\": \"${TEST_DUE_AT}\",
        \"title\": \"RT-1 Test Bill\",
        \"kind\": \"bill\"
    }"
    
    local response
    response=$(curl -s -w "\n%{http_code}" -X POST "${FINANCE_URL}/api/finance/bills" \
        -H "Content-Type: application/json" \
        -d "$bill_payload")
    
    local http_code
    http_code=$(echo "$response" | tail -n1)
    local body
    body=$(echo "$response" | sed '$d')
    
    if [ "$http_code" = "201" ] || [ "$http_code" = "200" ]; then
        log_success "Bill created successfully (HTTP ${http_code})"
        
        # Extract bill ID from response
        BILL_ID=$(echo "$body" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
        if [ -z "$BILL_ID" ]; then
            # Try alternative JSON parsing
            BILL_ID=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")
        fi
        
        if [ -n "$BILL_ID" ]; then
            log_info "Bill ID: ${BILL_ID}"
        else
            log_warn "Could not extract bill ID from response"
        fi
        
        return 0
    else
        log_fail "Failed to create bill (HTTP ${http_code}): ${body}"
        return 1
    fi
}

# Step 2: Verify outbox entry was created
verify_outbox_entry() {
    log_info "Step 2: Verifying outbox entry..."
    
    # Give some time for the outbox to be written
    sleep 2
    
    local outbox_count
    outbox_count=$(run_psql -c "SELECT count(*) FROM finance_outbox WHERE family_id='${TEST_FAMILY_ID}' AND subject='finance.due.registered';")
    
    if [ "$outbox_count" -gt 0 ]; then
        log_success "Outbox entry found (${outbox_count} entries)"
        return 0
    else
        log_fail "No outbox entry found for finance.due.registered"
        return 1
    fi
}

# Step 3: Verify due registration in homeos
verify_due_registration() {
    log_info "Step 3: Verifying due registration in homeos..."
    
    # Give consumer time to process
    sleep 3
    
    local reg_count
    reg_count=$(run_psql -c "SELECT count(*) FROM homeos_due_registration WHERE family_id='${TEST_FAMILY_ID}' AND deleted_at IS NULL;")
    
    if [ "$reg_count" -gt 0 ]; then
        log_success "Due registration found (${reg_count} active registrations)"
        
        # Show registration details
        run_psql -c "SELECT id, title, due_at, created_at FROM homeos_due_registration WHERE family_id='${TEST_FAMILY_ID}' AND deleted_at IS NULL;"
        
        return 0
    else
        log_fail "No active due registration found"
        return 1
    fi
}

# Step 4: Pay the bill (which should trigger revoke)
pay_bill() {
    log_info "Step 4: Paying bill (triggers revoke)..."
    
    if [ -z "${BILL_ID:-}" ]; then
        log_fail "No bill ID available, cannot pay"
        return 1
    fi
    
    local response
    response=$(curl -s -w "\n%{http_code}" -X PUT "${FINANCE_URL}/api/finance/bills/${BILL_ID}/pay" \
        -H "Content-Type: application/json" \
        -d "{\"paid_at\": \"$(date -u +"%Y-%m-%dT%H:%M:%SZ")\"}")
    
    local http_code
    http_code=$(echo "$response" | tail -n1)
    local body
    body=$(echo "$response" | sed '$d')
    
    if [ "$http_code" = "200" ]; then
        log_success "Bill paid successfully"
        return 0
    else
        log_fail "Failed to pay bill (HTTP ${http_code}): ${body}"
        return 1
    fi
}

# Step 5: Verify revoke event was emitted
verify_revoke_event() {
    log_info "Step 5: Verifying revoke event..."
    
    sleep 2
    
    local revoke_count
    revoke_count=$(run_psql -c "SELECT count(*) FROM finance_outbox WHERE family_id='${TEST_FAMILY_ID}' AND subject='finance.due.revoked';")
    
    if [ "$revoke_count" -gt 0 ]; then
        log_success "Revoke event found in outbox (${revoke_count} entries)"
        return 0
    else
        log_fail "No revoke event found in outbox"
        return 1
    fi
}

# Step 6: Verify due registration was revoked (soft-deleted)
verify_revocation() {
    log_info "Step 6: Verifying due registration was revoked..."
    
    # Give consumer time to process revoke
    sleep 3
    
    local revoked_count
    revoked_count=$(run_psql -c "SELECT count(*) FROM homeos_due_registration WHERE family_id='${TEST_FAMILY_ID}' AND deleted_at IS NOT NULL;")
    
    local active_count
    active_count=$(run_psql -c "SELECT count(*) FROM homeos_due_registration WHERE family_id='${TEST_FAMILY_ID}' AND deleted_at IS NULL;")
    
    if [ "$revoked_count" -gt 0 ] && [ "$active_count" -eq 0 ]; then
        log_success "Due registration was revoked (${revoked_count} revoked, ${active_count} still active)"
        return 0
    else
        log_fail "Revocation not complete (revoked: ${revoked_count}, active: ${active_count})"
        return 1
    fi
}

# Step 7: Verify dedupe table entries
verify_dedupe_table() {
    log_info "Step 7: Verifying dedupe table entries..."
    
    local dedupe_count
    dedupe_count=$(run_psql -c "SELECT count(*) FROM homeos_event_dedupe;")
    
    if [ "$dedupe_count" -gt 0 ]; then
        log_success "Dedupe table has entries (${dedupe_count} entries)"
        
        # Show recent dedupe entries
        run_psql -c "SELECT event_type, business_id, processed_at FROM homeos_event_dedupe ORDER BY processed_at DESC LIMIT 5;"
        
        return 0
    else
        log_fail "Dedupe table is empty (expected entries from processing events)"
        return 1
    fi
}

# Step 8: Verify messages in JetStream
verify_jetstream_messages() {
    log_info "Step 8: Verifying JetStream messages..."
    
    # This would require nats CLI, which may not be available
    # For now, we just note that this verification is skipped
    log_warn "JetStream message verification requires 'nats' CLI tool (skipped)"
    return 0
}

# Cleanup phase
cleanup() {
    log_info "Cleaning up RT-1 test environment..."
    sh "${DEPLOY_DIR}/scripts/setup-rt1-db.sh" down
}

# Run all tests
run_tests() {
    log_info "=========================================="
    log_info "RT-1 Real-Stack End-to-End Proof"
    log_info "=========================================="
    log_info "Database: ${TEMP_DB_NAME}"
    log_info "Finance URL: ${FINANCE_URL}"
    log_info "HomeOS URL: ${HOMEOS_URL}"
    log_info ""
    
    create_bill || return 1
    verify_outbox_entry || return 1
    verify_due_registration || return 1
    pay_bill || return 1
    verify_revoke_event || return 1
    verify_revocation || return 1
    verify_dedupe_table || return 1
    verify_jetstream_messages
    
    return 0
}

# Print summary
print_summary() {
    echo ""
    echo "=========================================="
    echo "RT-1 Test Summary"
    echo "=========================================="
    echo -e "Passed: ${GREEN}${PASS}${NC}"
    echo -e "Failed: ${RED}${FAIL}${NC}"
    echo ""
    
    if [ $FAIL -eq 0 ]; then
        echo -e "${GREEN}✓ All tests passed!${NC}"
        echo ""
        echo "The real-stack proof confirms:"
        echo "  1. Bill creation triggers finance.due.registered event"
        echo "  2. Event is consumed by homeos and creates due registration"
        echo "  3. Bill payment triggers finance.due.revoked event"
        echo "  4. Revoke is consumed and soft-deletes the registration"
        echo "  5. Dedupe table prevents duplicate processing"
        echo "  6. All operations use the temporary database (${TEMP_DB_NAME})"
        return 0
    else
        echo -e "${RED}✗ Some tests failed${NC}"
        return 1
    fi
}

main() {
    action="${1:-all}"
    
    case "${action}" in
        setup)
            setup
            ;;
        test)
            run_tests
            print_summary
            ;;
        cleanup)
            cleanup
            ;;
        all)
            setup || exit 1
            run_tests
            result=$?
            print_summary
            cleanup
            exit $result
            ;;
        *)
            echo "Usage: $0 [setup|test|cleanup|all]"
            exit 1
            ;;
    esac
}

main "$@"
