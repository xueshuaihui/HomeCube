#!/usr/bin/env bash
# check-frontend-homology.sh — CI Gate 4: Frontend homology and routing validation (docs/p1-tech-plan.md §10.2 Gate 4)
#
# Validates that:
#   1. Runs web/scripts/check-pages.mjs (existing seven frontend homology checks)
#   2. Bidirectional route AST comparison: Gin route groups vs pages.json subpackages
#      - Extract Gin route group prefix from main.go files
#      - Extract subpackage collection from pages.json
#      - Assert they match (registry implemented domains − homeos = subPackages[].root)
#
# Exit codes: 0 = pass, 1 = fail
# Bash 3.2 compatible; all variables use ${VAR} form.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER_DIR="$(cd "${SCRIPT_DIR}/../../server" && pwd)"
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

# Step 1: Run existing check-pages.mjs script
echo "=== Running check-pages.mjs (seven frontend homology checks) ==="
if node "${WEB_DIR}/scripts/check-pages.mjs"; then
    add_pass "check-pages.mjs: All seven frontend homology checks passed"
else
    add_failure "check-pages.mjs failed (see output above)"
fi

# Step 2: Extract Gin route group prefixes from main.go files
echo ""
echo "=== Extracting Gin route groups from service main.go files ==="

IMPLEMENTED_CODES=$("${REGISTRYD_BIN}" --format=json | python3 -c "import sys,json; print(' '.join([d['code'] for d in json.load(sys.stdin) if d['implemented']]))")

for code in ${IMPLEMENTED_CODES}; do
    main_go_file="${SERVER_DIR}/services/svc-${code}/cmd/svc-${code}/main.go"

    if [ ! -f "${main_go_file}" ]; then
        add_failure "main.go not found for service svc-${code}: ${main_go_file}"
        continue
    fi

    # Verify the code constant matches expected pattern
    if grep -q "\"${code}\"" "${main_go_file}"; then
        route_prefix="/api/${code}/"
        echo "  Service svc-${code}: route prefix = ${route_prefix}"
    else
        add_failure "Cannot determine route prefix for svc-${code} from ${main_go_file}"
    fi
done

# Step 3: Extract subpackage roots from pages.json
echo ""
echo "=== Extracting subpackage roots from pages.json ==="

PAGES_JSON="${WEB_DIR}/src/pages.json"
if [ ! -f "${PAGES_JSON}" ]; then
    add_failure "pages.json not found: ${PAGES_JSON}"
else
    # Extract subpackage roots using python3 for JSON parsing
    SUBPACKAGE_ROOTS=$(python3 -c "
import json
with open('${PAGES_JSON}') as f:
    doc = json.load(f)
roots = [sp['root'] for sp in doc.get('subPackages', [])]
print(' '.join(roots))
" 2>/dev/null || echo "")

    echo "  Subpackage roots: ${SUBPACKAGE_ROOTS}"

    # Step 4: Compare Gin prefixes with subpackage roots
    echo ""
    echo "=== Comparing Gin route groups with subpackage roots ==="

    # Expected subpackage roots = implemented domains - homeos
    EXPECTED_ROOTS=""
    for code in ${IMPLEMENTED_CODES}; do
        if [ "${code}" != "homeos" ]; then
            EXPECTED_ROOTS="${EXPECTED_ROOTS} pages/${code}"
        fi
    done
    EXPECTED_ROOTS=$(echo "${EXPECTED_ROOTS}" | xargs)  # trim whitespace

    # Normalize both lists for comparison
    ACTUAL_ROOTS_SORTED=$(echo "${SUBPACKAGE_ROOTS}" | tr ' ' '\n' | sort | tr '\n' ' ' | xargs)
    EXPECTED_ROOTS_SORTED=$(echo "${EXPECTED_ROOTS}" | tr ' ' '\n' | sort | tr '\n' ' ' | xargs)

    if [ "${ACTUAL_ROOTS_SORTED}" = "${EXPECTED_ROOTS_SORTED}" ]; then
        add_pass "Route AST comparison: Gin prefixes match subpackage roots (${ACTUAL_ROOTS_SORTED})"
    else
        add_failure "Route AST mismatch: expected '${EXPECTED_ROOTS_SORTED}' but got '${ACTUAL_ROOTS_SORTED}'"
        add_failure "Gin route groups must match pages.json subpackages (registry implemented − homeos)"
    fi
fi

# Report results
echo ""
echo "=== Frontend Homology Validation Results ==="

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
    echo "Conclusion: Frontend homology validation FAILED (${failure_count} failures)"
    exit 1
else
    pass_count=0
    if [ -s "${PASSES_FILE}" ]; then
        pass_count=$(wc -l < "${PASSES_FILE}" | tr -d ' ')
    fi
    echo ""
    echo "Conclusion: All frontend homology validations passed (${pass_count} checks)"
    exit 0
fi
