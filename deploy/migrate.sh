#!/bin/bash
# =============================================================================
# HomeCube · Migration runner script
#
# Runs golang-migrate CLI against both homeos and finance schemas sequentially.
# This script is meant to be run inside the migrate container or directly on
# the host with access to PostgreSQL.
#
# Usage: ./migrate.sh [up|down|force]
#   up   - Apply all pending migrations (default)
#   down - Rollback last migration
#   force - Force version without running migration (for recovery)
# =============================================================================

set -euo pipefail

# Configuration from environment
DB_HOST="${MIGRATE_DB_HOST:-postgres16}"
DB_PORT="${MIGRATE_DB_PORT:-5432}"
DB_NAME="${MIGRATE_DB_NAME:-homecube}"
DB_USER="${MIGRATE_DB_USER:-postgres}"
DB_PASSWORD="${MIGRATE_DB_PASSWORD:-CHANGE_ME}"

MIGRATIONS_DIR="${MIGRATE_MIGRATIONS_DIR:-/migrations}"

# Build database URL for golang-migrate
# Format: postgres://user:password@host:port/dbname?sslmode=disable&search_path=schema
build_db_url() {
    local schema="$1"
    echo "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable&search_path=${schema}"
}

# Run migrations for a specific schema
run_migrations() {
    local schema="$1"
    local action="${2:-up}"
    local db_url
    db_url=$(build_db_url "$schema")
    local migration_path="file://${MIGRATIONS_DIR}/${schema}"

    echo "[migrate] Running '${action}' for schema '${schema}'..."
    echo "  Database: ${DB_NAME}.${schema}"
    echo "  Migrations: ${migration_path}"

    case "$action" in
        up)
            migrate -path "$migration_path" -database "$db_url" up
            ;;
        down)
            migrate -path "$migration_path" -database "$db_url" down 1
            ;;
        force)
            local version="${3:-0}"
            migrate -path "$migration_path" -database "$db_url" force "$version"
            ;;
        *)
            echo "Error: Unknown action '${action}'. Use 'up', 'down', or 'force'."
            exit 1
            ;;
    esac

    if [ $? -eq 0 ]; then
        echo "[migrate] Schema '${schema}' migration completed successfully."
    else
        echo "[migrate] ERROR: Schema '${schema}' migration failed!"
        return 1
    fi
}

# Main execution
main() {
    local action="${1:-up}"

    echo "=========================================="
    echo "HomeCube Migration Runner"
    echo "=========================================="
    echo "Action: ${action}"
    echo "Database: ${DB_HOST}:${DB_PORT}/${DB_NAME}"
    echo ""

    # Check if migrate CLI is available
    if ! command -v migrate &> /dev/null; then
        echo "Error: golang-migrate CLI not found. Install it or use the migrate/migrate Docker image."
        exit 1
    fi

    # Run migrations for each schema in order
    # Order matters: homeos first (base service), then finance
    local schemas=("homeos" "finance")

    for schema in "${schemas[@]}"; do
        # Check if migration directory exists
        if [ ! -d "${MIGRATIONS_DIR}/${schema}" ]; then
            echo "Warning: Migration directory for '${schema}' not found at ${MIGRATIONS_DIR}/${schema}, skipping."
            continue
        fi

        # Check if there are any migration files
        if [ -z "$(ls -A "${MIGRATIONS_DIR}/${schema}"/*.up.sql 2>/dev/null)" ]; then
            echo "Warning: No migration files found for '${schema}', skipping."
            continue
        fi

        run_migrations "$schema" "$action" || exit 1
        echo ""
    done

    echo "=========================================="
    echo "All migrations completed successfully!"
    echo "=========================================="
}

main "$@"
