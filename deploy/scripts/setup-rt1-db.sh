#!/bin/bash
# =============================================================================
# HomeCube · RT-1 Real-Stack E2E Test - Temporary Database Setup
#
# Creates an isolated temporary database (hc_rt1_e2e) for end-to-end testing
# of the bill-create → pay → revoke chain without affecting the dev database.
#
# This script:
#   1. Creates the hc_rt1_e2e database
#   2. Creates homeos and finance schemas
#   3. Creates hc_homeos and hc_finance roles with proper permissions
#   4. Runs migrations against both schemas
#
# Usage: sh deploy/scripts/setup-rt1-db.sh [up|down]
#   up   - Create temp database and run migrations (default)
#   down - Drop temp database
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${DEPLOY_DIR}/env.local"

# Load environment variables from env.local if it exists
if [ -f "$ENV_FILE" ]; then
    set -a
    source "$ENV_FILE"
    set +a
fi

# Configuration
TEMP_DB_NAME="${RT1_DB_NAME:-hc_rt1_e2e}"
DB_HOST="${MIGRATE_DB_HOST:-postgres16}"
DB_PORT="${MIGRATE_DB_PORT:-5432}"
DB_USER="${MIGRATE_DB_USER:-postgres}"
DB_PASSWORD="${MIGRATE_DB_PASSWORD:-test123}"

# For containerized postgres, use docker exec or psql depending on availability
PSQL_HOST="127.0.0.1"  # Default for local connections
if command -v psql &> /dev/null; then
    # Use local psql client
    if [ "$DB_HOST" = "postgres16" ]; then
        PSQL_HOST="127.0.0.1"
    else
        PSQL_HOST="$DB_HOST"
    fi
    PSQL_CMD="psql -h ${PSQL_HOST} -p ${DB_PORT} -U ${DB_USER}"
    PSQL_MODE="local"
else
    # Use docker exec to run psql inside the container
    COMPOSE_FILE="${DEPLOY_DIR}/docker-compose.yml"
    ENV_FILE_ARG="--env-file ${ENV_FILE}"
    PSQL_CMD="docker compose ${ENV_FILE_ARG} -f ${COMPOSE_FILE} exec -T postgres16 psql -U ${DB_USER}"
    PSQL_MODE="docker"
fi

fail() {
    echo "[rt1-setup] ERROR: ${1}" >&2
    exit 1
}

setup_temp_db() {
    echo "[rt1-setup] Setting up temporary database: ${TEMP_DB_NAME}"
    
    # Check if database already exists and drop it with force
    local db_exists
    if [ "$PSQL_MODE" = "docker" ]; then
        db_exists=$(docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -lqt 2>/dev/null | cut -d \| -f 1 | grep -qw "${TEMP_DB_NAME}" && echo "yes" || echo "no")
    else
        db_exists=$(${PSQL_CMD} -lqt 2>/dev/null | cut -d \| -f 1 | grep -qw "${TEMP_DB_NAME}" && echo "yes" || echo "no")
    fi
    
    if [ "$db_exists" = "yes" ]; then
        echo "[rt1-setup] WARNING: Database ${TEMP_DB_NAME} already exists, terminating connections and dropping..."
        # Terminate all connections to the database first
        if [ "$PSQL_MODE" = "docker" ]; then
            docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='${TEMP_DB_NAME}' AND pid <> pg_backend_pid();" 2>/dev/null || true
            docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -c "DROP DATABASE IF EXISTS ${TEMP_DB_NAME};" || fail "Failed to drop existing database"
        else
            ${PSQL_CMD} -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='${TEMP_DB_NAME}' AND pid <> pg_backend_pid();" 2>/dev/null || true
            ${PSQL_CMD} -c "DROP DATABASE IF EXISTS ${TEMP_DB_NAME};" || fail "Failed to drop existing database"
        fi
    fi
    
    # Create database
    echo "[rt1-setup] Creating database ${TEMP_DB_NAME}..."
    if [ "$PSQL_MODE" = "docker" ]; then
        docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -c "CREATE DATABASE ${TEMP_DB_NAME};" || fail "Failed to create database"
    else
        ${PSQL_CMD} -c "CREATE DATABASE ${TEMP_DB_NAME};" || fail "Failed to create database"
    fi
    
    # Connect to the new database and create schemas/roles
    echo "[rt1-setup] Creating schemas and roles..."
    local sql_commands="
-- Create schemas
CREATE SCHEMA IF NOT EXISTS homeos;
CREATE SCHEMA IF NOT EXISTS finance;

-- Create roles if they don't exist
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'hc_homeos') THEN
        CREATE ROLE hc_homeos LOGIN PASSWORD '${DB_PASSWORD}';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'hc_finance') THEN
        CREATE ROLE hc_finance LOGIN PASSWORD '${DB_PASSWORD}';
    END IF;
END
\$\$;

-- Grant schema usage
GRANT USAGE ON SCHEMA homeos TO hc_homeos;
GRANT USAGE ON SCHEMA finance TO hc_finance;

-- Set default privileges
ALTER DEFAULT PRIVILEGES IN SCHEMA homeos GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hc_homeos;
ALTER DEFAULT PRIVILEGES IN SCHEMA finance GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hc_finance;

-- Revoke public schema access
REVOKE ALL ON SCHEMA public FROM hc_homeos;
REVOKE ALL ON SCHEMA public FROM hc_finance;
"
    
    if [ "$PSQL_MODE" = "docker" ]; then
        docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -d "${TEMP_DB_NAME}" <<< "$sql_commands" || fail "Failed to create schemas and roles"
    else
        ${PSQL_CMD} -d "${TEMP_DB_NAME}" <<< "$sql_commands" || fail "Failed to create schemas and roles"
    fi
    
    echo "[rt1-setup] Database and schemas created successfully"
}

run_migrations() {
    echo "[rt1-setup] Running migrations against ${TEMP_DB_NAME}..."
    
    # Run migrations using docker compose migrate service with overridden env
    # Note: We need to set POSTGRES_DB because docker-compose.yml uses that for MIGRATE_DB_NAME
    POSTGRES_DB="${TEMP_DB_NAME}" \
    MIGRATE_DB_NAME="${TEMP_DB_NAME}" \
    MIGRATE_DB_HOST="postgres16" \
    MIGRATE_DB_PORT="${DB_PORT}" \
    MIGRATE_DB_USER="${DB_USER}" \
    MIGRATE_DB_PASSWORD="${DB_PASSWORD}" \
    MIGRATE_MIGRATIONS_DIR="/migrations" \
    docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" run -T --rm migrate up || fail "Migration failed"
    
    echo "[rt1-setup] Migrations completed successfully"
}

cleanup_temp_db() {
    echo "[rt1-setup] Dropping temporary database: ${TEMP_DB_NAME}"
    if [ "$PSQL_MODE" = "docker" ]; then
        docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -c "DROP DATABASE IF EXISTS ${TEMP_DB_NAME};" || fail "Failed to drop database"
    else
        ${PSQL_CMD} -c "DROP DATABASE IF EXISTS ${TEMP_DB_NAME};" || fail "Failed to drop database"
    fi
    echo "[rt1-setup] Temporary database dropped"
}

verify_setup() {
    echo "[rt1-setup] Verifying setup..."
    
    local psql_query
    
    # Check schemas exist
    psql_query="SELECT count(*) FROM information_schema.schemata WHERE schema_name IN ('homeos', 'finance');"
    local schema_count
    if [ "$PSQL_MODE" = "docker" ]; then
        schema_count=$(docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -d "${TEMP_DB_NAME}" -t -A -c "$psql_query" 2>/dev/null | tr -d '[:space:]')
    else
        schema_count=$(${PSQL_CMD} -d "${TEMP_DB_NAME}" -t -A -c "$psql_query" 2>/dev/null | tr -d '[:space:]')
    fi
    if [ "$schema_count" != "2" ]; then
        fail "Expected 2 schemas, found ${schema_count}"
    fi
    
    # Check roles exist
    psql_query="SELECT count(*) FROM pg_roles WHERE rolname IN ('hc_homeos', 'hc_finance');"
    local role_count
    if [ "$PSQL_MODE" = "docker" ]; then
        role_count=$(docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -d "${TEMP_DB_NAME}" -t -A -c "$psql_query" 2>/dev/null | tr -d '[:space:]')
    else
        role_count=$(${PSQL_CMD} -d "${TEMP_DB_NAME}" -t -A -c "$psql_query" 2>/dev/null | tr -d '[:space:]')
    fi
    if [ "$role_count" != "2" ]; then
        fail "Expected 2 roles, found ${role_count}"
    fi
    
    # Check migration version tables exist
    psql_query="SELECT count(*) FROM information_schema.tables WHERE table_schema IN ('homeos', 'finance') AND table_name LIKE 'schema_migrations_%';"
    local version_tables
    if [ "$PSQL_MODE" = "docker" ]; then
        version_tables=$(docker compose --env-file "${ENV_FILE}" -f "${DEPLOY_DIR}/docker-compose.yml" exec -T postgres16 psql -U "${DB_USER}" -d "${TEMP_DB_NAME}" -t -A -c "$psql_query" 2>/dev/null | tr -d '[:space:]')
    else
        version_tables=$(${PSQL_CMD} -d "${TEMP_DB_NAME}" -t -A -c "$psql_query" 2>/dev/null | tr -d '[:space:]')
    fi
    if [ "$version_tables" != "2" ]; then
        fail "Expected 2 version tables, found ${version_tables}"
    fi
    
    echo "[rt1-setup] Verification passed: 2 schemas, 2 roles, 2 version tables"
}

main() {
    action="${1:-up}"
    
    echo "=========================================="
    echo "RT-1 Real-Stack E2E Test - DB Setup"
    echo "=========================================="
    echo "Action          : ${action}"
    echo "Database        : ${TEMP_DB_NAME}"
    echo "Host            : ${PSQL_HOST}:${DB_PORT}"
    echo ""
    
    case "${action}" in
        up)
            setup_temp_db
            run_migrations
            verify_setup
            echo ""
            echo "=========================================="
            echo "Temporary database ready for RT-1 tests"
            echo "=========================================="
            echo ""
            echo "Use these environment variables to connect services:"
            echo "  HOMEOS_DSN=\"host=${PSQL_HOST} port=${DB_PORT} user=hc_homeos password=${DB_PASSWORD} dbname=${TEMP_DB_NAME} search_path=homeos\""
            echo "  FINANCE_DSN=\"host=${PSQL_HOST} port=${DB_PORT} user=hc_finance password=${DB_PASSWORD} dbname=${TEMP_DB_NAME} search_path=finance\""
            echo ""
            ;;
        down)
            cleanup_temp_db
            echo ""
            echo "=========================================="
            echo "Temporary database cleaned up"
            echo "=========================================="
            ;;
        *)
            fail "Unknown action '${action}'. Use 'up' or 'down'."
            ;;
    esac
}

main "$@"
