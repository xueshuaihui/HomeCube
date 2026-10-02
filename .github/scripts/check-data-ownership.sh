#!/usr/bin/env bash
# check-data-ownership.sh — CI Gate 2: Data ownership validation (docs/p1-tech-plan.md §10.2 Gate 2)
#
# Validates that:
#   1. Business table names have prefix matching their service code
#   2. Base runtime tables follow suffix whitelist (定版 ㉔)
#   3. Demo tables follow tightened whitelist
#   4. schema_migrations_{code} is an explicit exception
#   5. No tables/migrations/subpackages for unborn domains appear
#
# Exit codes: 0 = pass, 1 = fail
# Bash 3.2 compatible; all variables use ${VAR} form.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_DIR="$(cd "${SCRIPT_DIR}/../../server" && pwd)"
MIGRATIONS_DIR="${SERVER_DIR}/migrations"
WEB_DIR="$(cd "${SCRIPT_DIR}/../../web" && pwd)"

# Get registry information
REGISTRYD_BIN="${HC_REGISTRYD_BIN:-}"
if [ -z "${REGISTRYD_BIN}" ]; then
    REGISTRYD_BIN="${SERVER_DIR}/packages/registry/cmd/registryd/registryd"
fi

if [ ! -x "${REGISTRYD_BIN}" ]; then
    echo "ERROR: registryd binary not found at ${REGISTRYD_BIN}" >&2
    exit 1
fi

FAILURES_FILE=$(mktemp)
PASSES_FILE=$(mktemp)
trap 'rm -f "${FAILURES_FILE}" "${PASSES_FILE}"' EXIT

add_failure() {
    echo "${1}" >> "${FAILURES_FILE}"
}

add_pass() {
    echo "${1}" >> "${PASSES_FILE}"
}

# Get implemented and unborn codes
IMPLEMENTED_JSON=$("${REGISTRYD_BIN}" --format=json | python3 -c "import sys,json; print(' '.join([d['code'] for d in json.load(sys.stdin) if d['implemented']]))")
UNBORN_JSON=$("${REGISTRYD_BIN}" --format=json | python3 -c "import sys,json; print(' '.join([d['code'] for d in json.load(sys.stdin) if not d['implemented']]))")
ALL_CODES=$("${REGISTRYD_BIN}" --format=codes)

# Suffix whitelist for base runtime tables (定版 ㉔ from docs/p1-tech-plan.md §10.2 Gate 2)
BASE_SUFFIX_WHITELIST="_outbox _event_dedupe _dead_letter _change_log _idempotency _proj_ homeos_search_index homeos_audit_log homeos_due_registration homeos_event_archive"

# Explicit exception: schema_migrations pattern
SCHEMA_MIGRATIONS_PREFIX="schema_migrations_"

# Demo table whitelist (tightened per §8.3)
DEMO_TABLE_WHITELIST="finance_demo_item"

is_base_runtime_table() {
    local table_name="${1}"

    # Check schema_migrations exception first
    case "${table_name}" in
        ${SCHEMA_MIGRATIONS_PREFIX}*)
            return 0
            ;;
    esac

    # Check demo table whitelist
    for demo in ${DEMO_TABLE_WHITELIST}; do
        if [ "${table_name}" = "${demo}" ]; then
            return 0
        fi
    done

    # Check base suffix whitelist
    for suffix in ${BASE_SUFFIX_WHITELIST}; do
        case "${table_name}" in
            *"${suffix}"*)
                return 0
                ;;
        esac
    done

    return 1
}

check_migration_file_ownership() {
    local code="${1}"
    local migration_dir="${MIGRATIONS_DIR}/${code}"

    if [ ! -d "${migration_dir}" ]; then
        add_failure "Migration directory exists for non-implemented domain: ${migration_dir}"
        return
    fi

    for f in "${migration_dir}"/*.up.sql; do
        if [ ! -f "${f}" ]; then
            continue
        fi

        local file_basename
        file_basename=$(basename "${f}")
        local content
        content=$(cat "${f}")

        # Extract CREATE TABLE statements using python for reliable parsing
        local tables
        tables=$(python3 -c "
import re
with open('${f}') as f:
    content = f.read()
# Match CREATE TABLE [IF NOT EXISTS] [schema.]table_name
pattern = r'CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:\w+\.)?(\w+)'
tables = re.findall(pattern, content, re.IGNORECASE)
for t in tables:
    print(t)
" 2>/dev/null || echo "")

        for table in ${tables}; do
            # Skip schema_migrations exception
            case "${table}" in
                ${SCHEMA_MIGRATIONS_PREFIX}*)
                    continue
                    ;;
            esac

            # Check if it's a base runtime table (whitelist)
            if is_base_runtime_table "${table}"; then
                # For base runtime tables, only verify prefix matches service
                case "${table}" in
                    ${code}_*)
                        # Correct prefix
                        ;;
                    homeos_*)
                        # Allow homeos_* tables only in homeos schema
                        if [ "${code}" != "homeos" ]; then
                            add_failure "Table ${table} in ${code} migration does not match expected prefix (${code}_ or homeos_ for homeos schema)"
                        fi
                        ;;
                    *)
                        add_failure "Base runtime table ${table} in ${code} migration has unexpected prefix"
                        ;;
                esac
                continue
            fi

            # For business tables, verify prefix matches service code
            case "${table}" in
                ${code}_*)
                    # Correct prefix
                    ;;
                *)
                    add_failure "Business table ${table} in ${code} migration has wrong prefix (expected ${code}_)"
                    ;;
            esac
        done
    done
}

check_no_unborn_artifacts() {
    # Check for migration directories of unborn domains
    for code in ${UNBORN_JSON}; do
        if [ -d "${MIGRATIONS_DIR}/${code}" ]; then
            add_failure "Migration directory exists for unborn domain ${code}: ${MIGRATIONS_DIR}/${code}"
        fi
    done

    # Check for subpackage directories of unborn domains
    local pages_dir="${WEB_DIR}/src/pages"
    if [ -d "${pages_dir}" ]; then
        for dir in "${pages_dir}"/*/; do
            if [ -d "${dir}" ]; then
                local dirname
                dirname=$(basename "${dir}")
                for code in ${UNBORN_JSON}; do
                    if [ "${dirname}" = "${code}" ]; then
                        add_failure "Subpackage directory exists for unborn domain ${code}: ${dir}"
                    fi
                done
            fi
        done
    fi
}

# Main validation loop
echo "Checking data ownership for implemented services..."

for code in ${IMPLEMENTED_JSON}; do
    echo "  Validating: ${code}"
    check_migration_file_ownership "${code}"
done

echo "Checking for unborn domain artifacts..."
check_no_unborn_artifacts

# Report results
echo ""
echo "=== Data Ownership Validation Results ==="

if [ -s "${PASSES_FILE}" ]; then
    while IFS= read -r line; do
        echo "  PASS: ${line}"
    done < "${PASSES_FILE}"
fi

if [ -s "${FAILURES_FILE}" ]; then
    echo ""
    echo "FAILURES:"
    while IFS= read -r line; do
        echo "  FAIL: ${line}"
    done < "${FAILURES_FILE}"
    echo ""
    failure_count=$(wc -l < "${FAILURES_FILE}" | tr -d ' ')
    echo "Conclusion: Data ownership validation FAILED (${failure_count} failures)"
    exit 1
else
    pass_count=0
    if [ -s "${PASSES_FILE}" ]; then
        pass_count=$(wc -l < "${PASSES_FILE}" | tr -d ' ')
    fi
    echo ""
    echo "Conclusion: All data ownership validations passed (${pass_count} checks)"
    exit 0
fi
