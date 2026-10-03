#!/bin/sh
# =============================================================================
# HomeCube · Migration runner script（golang-migrate 适配层）
#
#  Runs golang-migrate CLI against the homeos and finance sequences, each with
#  its own migration directory and its own version table (§2.2 每服务一条独立序列).
#
#  本脚本 meant to be run inside the compose `migrate` 容器（镜像 migrate/migrate
#  的 busybox ash，故本文件只用 POSIX sh 语法：无数组、无 [[ ]]），也可在宿主上
#  直接 `sh deploy/migrate.sh up`（需要 migrate CLI 在 PATH 里）。
#
#  Usage: sh migrate.sh [up|down|force|version] [N]
#    up      - Apply all pending migrations（默认）
#    down    - Rollback the last migration of each sequence（可给 N）
#    force   - Set version without running migration（仅故障恢复用；正常链路不用）
#    version - Print the current version of each sequence
#
#  ---- 为什么需要这个适配层（FBR1 修掉的三条缺陷）-----------------------------
#  (1) 仓库里的迁移文件名带 `{code}_00NN_` 前缀，是定版纪律：表前缀=所在服务 code，
#      CI 门禁按它做归属与跨域校验（.github/scripts/validate-migrations.sh 的
#      `s/^${code}_\([0-9]\{4\}\)_.*\.up\.sql$/\1/`，以及 check-data-ownership.sh）。
#      golang-migrate 的 file driver 却要求版本号在**文件名开头**，直接挂
#      server/migrations/{code} 会报 `error: first .: file does not exist`。
#      => 本脚本在临时目录里为每个 schema 生成一份「去掉 {code}_ 前缀」的视图，
#         用**软链**而不是拷贝：内容永远等于 server/migrations/ 下的原文，不存在
#         陈旧副本；仓库文件名一个字都不改。
#  (2) 版本表：0001 显式建的是 schema_migrations_{code}（技术方案 §2.2，CI 校验同名），
#      而 golang-migrate 默认的表名是 schema_migrations。
#      => -x-migrations-table=schema_migrations_{code}
#  (3) 版本表所在 schema：不传时工具会把版本表建在它自己认定的 schema 里
#      （默认走 public / search_path，dev 库 public 里那张 0 行的
#       schema_migrations_homeos 就是这么来的）。
#      => -x-migrations-table-search-path={code}，把版本表钉在所属 schema。
#
#  迁移序列本身（server/migrations/**）不在本脚本的可改范围：适配只发生在执行侧。
# =============================================================================

set -euo pipefail

# ---- Configuration from environment ----------------------------------------
DB_HOST="${MIGRATE_DB_HOST:-postgres16}"
DB_PORT="${MIGRATE_DB_PORT:-5432}"
DB_NAME="${MIGRATE_DB_NAME:-homecube}"
DB_USER="${MIGRATE_DB_USER:-postgres}"
DB_PASSWORD="${MIGRATE_DB_PASSWORD:-CHANGE_ME}"

MIGRATIONS_DIR="${MIGRATE_MIGRATIONS_DIR:-/migrations}"

# 要跑的序列（空格分隔）。默认按 §2.2 的顺序：homeos 底座在前，finance 在后。
# 单序列操作（如只对 finance 回滚一步）用 MIGRATE_SCHEMAS=finance 覆盖。
SCHEMAS="${MIGRATE_SCHEMAS:-homeos finance}"

# 版本表名前缀：与 0001 建立的表名同源（§2.2 定版 schema_migrations_{code}）。
VERSION_TABLE_PREFIX="${MIGRATE_VERSION_TABLE_PREFIX:-schema_migrations_}"

# 临时视图根目录（软链出来的 00NN_desc.up.sql 放这里）
STAGE_ROOT="${MIGRATE_STAGE_DIR:-}"
STAGE_OWNED=false

fail() {
    echo "[migrate] ERROR: ${1}" >&2
    exit 1
}

cleanup() {
    if [ "${STAGE_OWNED}" = true ] && [ -n "${STAGE_ROOT}" ]; then
        rm -rf "${STAGE_ROOT}"
    fi
}
trap cleanup EXIT INT TERM

# ---- 生成 golang-migrate 可接受的视图目录 ------------------------------------
# 输入：${MIGRATIONS_DIR}/<schema>/<schema>_00NN_desc.{up,down}.sql
# 输出：<STAGE_ROOT>/<schema>/00NN_desc.{up,down}.sql （软链指回原文）
build_view_dir() {
    schema="${1}"
    src_dir="${MIGRATIONS_DIR}/${schema}"
    view_dir="${STAGE_ROOT}/${schema}"

    [ -d "${src_dir}" ] || fail "migration directory not found: ${src_dir}"

    rm -rf "${view_dir}"
    mkdir -p "${view_dir}"

    linked=0
    for f in "${src_dir}"/*.sql; do
        [ -f "${f}" ] || continue
        base=$(basename "${f}")
        # 去掉 {code}_ 前缀 —— 保留下来的部分必须以 4 位版本号开头
        stripped="${base#${schema}_}"
        if [ "${stripped}" = "${base}" ]; then
            fail "file ${base} in ${src_dir} is not named {code}_00NN_desc.(up|down).sql; expected prefix ${schema}_"
        fi
        case "${stripped}" in
            [0-9][0-9][0-9][0-9]_*.up.sql|[0-9][0-9][0-9][0-9]_*.down.sql) ;;
            *) fail "file ${base} in ${src_dir} has no leading 4-digit sequence number after the '${schema}_' prefix (got: ${stripped})" ;;
        esac
        ln -s "${f}" "${view_dir}/${stripped}" || fail "cannot link ${f} -> ${view_dir}/${stripped}"
        linked=$((linked + 1))
    done

    [ "${linked}" -gt 0 ] || fail "no .sql migration files found in ${src_dir}"

    up_count=0
    down_count=0
    for g in "${view_dir}"/*.up.sql; do
        if [ -e "${g}" ]; then up_count=$((up_count + 1)); fi
    done
    for g in "${view_dir}"/*.down.sql; do
        if [ -e "${g}" ]; then down_count=$((down_count + 1)); fi
    done
    echo "[migrate] view ready: ${view_dir} (${linked} symlinks = ${up_count} up + ${down_count} down)"
}

# ---- 连接串 ------------------------------------------------------------------
# sslmode=disable 是本地 compose 链路（§2.1 单机同 compose 网络）；
# search_path=<schema> 让 0001 里未加 schema 限定的 CREATE TABLE 落到所属 schema；
# x-migrations-table* 两项见文件头 (2)(3)。
build_db_url() {
    schema="${1}"
    echo "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable&search_path=${schema}&x-migrations-table=${VERSION_TABLE_PREFIX}${schema}&x-migrations-table-search-path=${schema}"
}

run_schema() {
    schema="${1}"
    action="${2}"
    arg="${3:-}"

    echo "[migrate] sequence '${schema}' :: action='${action}'"
    echo "[migrate]   source   : file view of ${MIGRATIONS_DIR}/${schema}"
    echo "[migrate]   database : ${DB_HOST}:${DB_PORT}/${DB_NAME} (search_path=${schema})"
    echo "[migrate]   version  : table ${schema}.${VERSION_TABLE_PREFIX}${schema}"

    build_view_dir "${schema}"
    db_url=$(build_db_url "${schema}")
    view_dir="${STAGE_ROOT}/${schema}"

    case "${action}" in
        up)
            migrate -verbose -path "${view_dir}" -database "${db_url}" up
            ;;
        down)
            migrate -verbose -path "${view_dir}" -database "${db_url}" down "${arg:-1}"
            ;;
        version)
            migrate -path "${view_dir}" -database "${db_url}" version
            ;;
        force)
            [ -n "${arg}" ] || fail "force needs a target version, e.g. 'sh migrate.sh force 7'"
            echo "[migrate] WARNING: force rewrites version tracking without running SQL (recovery only)" >&2
            migrate -path "${view_dir}" -database "${db_url}" force "${arg}"
            ;;
        *)
            fail "unknown action '${action}'. Use 'up', 'down [N]', 'version' or 'force N'."
            ;;
    esac

    if [ "${action}" != "version" ]; then
        echo -n "[migrate]   now at version: "
        migrate -path "${view_dir}" -database "${db_url}" version
    fi
    echo ""
}

main() {
    action="${1:-up}"
    if [ "$#" -gt 0 ]; then
        shift
    fi
    arg="${1:-}"

    echo "=========================================="
    echo "HomeCube Migration Runner"
    echo "=========================================="
    echo "Action        : ${action}"
    echo "Database      : ${DB_HOST}:${DB_PORT}/${DB_NAME}"
    echo "Sequences     : ${SCHEMAS}"
    echo "Migrations    : ${MIGRATIONS_DIR}"
    echo "Version table : ${VERSION_TABLE_PREFIX}<schema> inside schema <schema>"
    echo ""

    command -v migrate >/dev/null 2>&1 || fail "golang-migrate CLI not found. Run this inside the compose 'migrate' service (image migrate/migrate)."

    if [ -z "${STAGE_ROOT}" ]; then
        STAGE_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/homecube-migrate-view.XXXXXX") || fail "mktemp failed"
        STAGE_OWNED=true
    else
        mkdir -p "${STAGE_ROOT}"
    fi
    echo "[migrate] view root: ${STAGE_ROOT}"

    for schema in ${SCHEMAS}; do
        run_schema "${schema}" "${action}" "${arg}"
    done

    echo "=========================================="
    echo "All sequences completed action='${action}' successfully."
    echo "=========================================="
}

main "$@"
