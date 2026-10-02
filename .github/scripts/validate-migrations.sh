#!/usr/bin/env bash
# validate-migrations.sh — CI Gate 1: Migration replay validation (docs/p1-tech-plan.md §10.2 Gate 1)
#
# For each implemented service, validates:
#   1. Version table matches code declarations
#   2. Migration files only touch their own schema (no cross-domain prefixes)
#
# Note: Full database replay requires a running PostgreSQL instance. This script performs
# static validation of migration structure and naming conventions.
#
# Exit codes: 0 = pass, 1 = fail
# Bash 3.2 compatible; all variables use ${VAR} form.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_DIR="$(cd "${SCRIPT_DIR}/../../server" && pwd)"
MIGRATIONS_DIR="${SERVER_DIR}/migrations"

# Get implemented domains from registryd
REGISTRYD_BIN="${HC_REGISTRYD_BIN:-}"
if [ -z "${REGISTRYD_BIN}" ]; then
    REGISTRYD_BIN="${SERVER_DIR}/packages/registry/cmd/registryd/registryd"
fi

if [ ! -x "${REGISTRYD_BIN}" ]; then
    echo "ERROR: registryd binary not found at ${REGISTRYD_BIN}" >&2
    echo "Build it first: cd server && go build -o packages/registry/cmd/registryd/registryd ./packages/registry/cmd/registryd" >&2
    exit 1
fi

# Get implemented codes as space-separated list
IMPLEMENTED_JSON=$("${REGISTRYD_BIN}" --format=json | python3 -c "import sys,json; print(' '.join([d['code'] for d in json.load(sys.stdin) if d['implemented']]))")

if [ -z "${IMPLEMENTED_JSON}" ]; then
    echo "FAIL: No implemented services found in registry" >&2
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

check_migration_version_consistency() {
    local code="${1}"
    local migration_dir="${MIGRATIONS_DIR}/${code}"

    if [ ! -d "${migration_dir}" ]; then
        add_failure "Migration directory missing for ${code}: ${migration_dir}"
        return
    fi

    # Extract version numbers from migration filenames
    local versions_in_files=""
    for f in "${migration_dir}"/*.up.sql; do
        if [ -f "${f}" ]; then
            local file_basename
            file_basename=$(basename "${f}")
            # Extract sequence number: {code}_NNNN_desc.up.sql
            local seq
            seq=$(echo "${file_basename}" | sed -n "s/^${code}_\([0-9]\{4\}\)_.*\.up\.sql$/\1/p")
            if [ -n "${seq}" ]; then
                versions_in_files="${versions_in_files} ${seq}"
            fi
        fi
    done

    if [ -z "${versions_in_files}" ]; then
        add_failure "No migration files found for ${code} in ${migration_dir}"
        return
    fi

    # Check that version table name is correct per registry convention
    local version_table="schema_migrations_${code}"

    # Verify migration files reference the correct version table
    local has_version_table=false
    for f in "${migration_dir}"/*_0001_*.up.sql; do
        if [ -f "${f}" ] && grep -q "${version_table}" "${f}"; then
            has_version_table=true
            break
        fi
    done

    if [ "${has_version_table}" = false ]; then
        add_failure "Migration 0001 for ${code} does not create version table ${version_table}"
        return
    fi

    add_pass "Version table consistency for ${code}: OK"
}

check_schema_isolation() {
    local code="${1}"
    local migration_dir="${MIGRATIONS_DIR}/${code}"

    if [ ! -d "${migration_dir}" ]; then
        return
    fi

    # Get all domain codes to check against
    local all_codes
    all_codes=$("${REGISTRYD_BIN}" --format=codes)

    # Check each migration file for cross-domain references
    local has_cross_domain=false
    for f in "${migration_dir}"/*.up.sql; do
        if [ ! -f "${f}" ]; then
            continue
        fi

        local file_basename
        file_basename=$(basename "${f}")

        # Read file content
        local content
        content=$(cat "${f}")

        # Check for CREATE TABLE or ALTER TABLE with other domain prefixes
        for other_code in ${all_codes}; do
            if [ "${other_code}" = "${code}" ]; then
                continue
            fi

            # Look for table names with other domain's prefix
            if echo "${content}" | grep -qiE "(CREATE|ALTER)\s+TABLE\s+.*\b${other_code}_"; then
                add_failure "Migration ${file_basename} in ${code} references table with prefix ${other_code}_ (cross-domain violation)"
                has_cross_domain=true
            fi
        done
    done

    if [ "${has_cross_domain}" = false ]; then
        add_pass "Schema isolation for ${code}: OK (no cross-domain table references)"
    fi
}

# Main validation loop
for code in ${IMPLEMENTED_JSON}; do
    echo "Validating migrations for service: ${code}"

    check_migration_version_consistency "${code}"
    check_schema_isolation "${code}"
done

# Report results
echo ""
echo "=== Migration Validation Results ==="

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
    echo "Conclusion: Migration validation FAILED (${failure_count} failures)"
    exit 1
else
    pass_count=0
    if [ -s "${PASSES_FILE}" ]; then
        pass_count=$(wc -l < "${PASSES_FILE}" | tr -d ' ')
    fi
    echo ""
    echo "Conclusion: All migration validations passed (${pass_count} checks)"
    exit 0
fi
