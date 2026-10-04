# =============================================================================
# HomeCube · 根 Makefile
#
# 命令口的语义权威源：docs/p1-tech-plan.md §10.1（Compose 与一条命令）与 §十三
# （联调与环境：make up 起全栈、make dev-{code} 把某服务跑出容器外调试）。
# 目录布局权威源：docs/p1-tech-plan.md §1.2、PRD 22.4。
#
# 兼容基线：GNU Make 3.81（本机实测）+ 配方 shell 为 /bin/bash 之外的最小 POSIX
# 子集（本机 bash 3.2.57）。因此本文件不使用 $(file ...)、secondary expansion、
# target-specific 变量的 `+=`、`.DEFAULT_GOAL` 等 4.x 才稳的写法；配方内的变量一律
# 写成 ${VAR}（bash 3.2 + 中文 locale 下 $VAR 紧跟中文会中断解析）。
#
# 交付边界（现状）：本文件交付「八个口的真实配方 + 前置件检查」。
# S1-D-a 已交付 deploy/docker-compose.yml（postgres16 + nats + nginx 三容器）与
# deploy/nginx.conf（前缀反代 + 未出生 5 前缀的「即将上线」+ H5 静态），所以下面 up 的配方
# 接的是真实的 compose 生命周期；S1-D-b（两 schema/两账号、migrate 容器、两服务容器、web/admin
# 静态容器）、备份/恢复/回滚脚本（S1-E）与门禁 1/2/4 的 CI 脚本尚未交付，
# 对应配方一律以非零码退出并打印归属卡 —— 不用 echo 冒充执行，也不提前代建。
# =============================================================================

# ---- 统一变量口（可用环境变量或命令行覆盖） ---------------------------------
# go 必须用绝对路径：make 的配方走 /bin/sh，用户级 ~/.zshrc 里的 PATH 对它无效
# （本机实测：$(HOME)/sdk/go/bin/go = go1.27.1 darwin/amd64）。
GO         ?= $(HOME)/sdk/go/bin/go
COMPOSE    ?= docker compose
ENV_FILE   ?= deploy/env.local

# 构建期不许静默下载工具链：每个 go 调用点显式传 GOTOOLCHAIN=local（定版口径 §15 B 行的宿主侧兑现）。
# server/ 是 monorepo 里唯一的 Go module（PRD 22.4：每服务一个 cmd、一个镜像、一条发布线）。
SERVER_DIR  ?= server
DEPLOY_DIR  ?= deploy
WEB_DIR     ?= web

# node 只被 up 的第 1 步用到（deploy/gen-nginx-unborn.mjs 调 registryd 取未出生面集合）。
# 与 GO 一样留成可覆盖的变量口：本机 node 由版本管理器放在非默认路径时用 NODE= 指过去。
NODE        ?= node

# §1.2 把 deploy/ 的内容登记为「compose、nginx.conf、备份/恢复/回滚、env 样例」，
# 但具体文件名未在任何文档里登记 —— 下列文件名是本卡给 S1-D/S1-E 的默认约定，
# 归属卡若换名，改这几行即可（变量口对外不变）。
COMPOSE_FILE  ?= $(DEPLOY_DIR)/docker-compose.yml
NGINX_CONF    ?= $(DEPLOY_DIR)/nginx.conf
BACKUP_SH     ?= $(DEPLOY_DIR)/backup.sh
RESTORE_SH    ?= $(DEPLOY_DIR)/restore.sh
ROLLBACK_SH   ?= $(DEPLOY_DIR)/rollback.sh

# 落盘路径出自 §10.1 原文：备份「加密落 deploy/data/backups」、soak 报告
# 「落 deploy/data/soak/report.json」。
BACKUP_DIR   ?= $(DEPLOY_DIR)/data/backups
SOAK_REPORT  ?= $(DEPLOY_DIR)/data/soak/report.json

# §10.1 的常驻容器集合（compose 服务名出自该行原文：nginx + nats(JetStream) +
# postgres16 + svc-homeos + svc-finance + web/admin 静态容器 + 一次性 migrate）。
# 当期只有两个服务（卷首第 12 项、定版口径 §15 的 E 项），up 配方里按原文顺序逐个点名。

# make restore 必填：§10.1「恢复到指定备份点」。make rollback 可选传 tag，
# 不传则由 S1-E 的脚本自行解析「上一版本」（§10.1：只切回上一版本镜像 tag）。
RESTORE_POINT ?=
ROLLBACK_TAG  ?=

.PHONY: up backup restore rollback seed-perf soak dev-homeos dev-finance inject-faults check-device-lock

# =============================================================================
# up —— §10.1：干净机器 -> 可用环境：建当期 schema 与账号 -> 迁移 -> 种子 -> 健康检查
#         （≤30 分钟，PRD 18.2 附加门禁③）
#
# ★ 本口当前的真实形态（S1-D-b 完整交付态）：
#   起的是 deploy/docker-compose.yml 里的全栈容器：
#     常驻容器（5个）：postgres16 + nats(JetStream) + nginx + svc-homeos + svc-finance
#     静态容器（2个）：web + admin
#     一次性容器：migrate（执行完即退出，不计入常驻数）
#   启动顺序：
#     1. postgres16 和 nats 先起（基础设施）
#     2. migrate 容器执行数据库迁移（建表、索引等）
#     3. svc-homeos / svc-finance 服务容器启动（依赖迁移完成）
#     4. web / admin 静态容器启动
#     5. nginx 最后启动（依赖所有上游服务健康）
#
# Schema 与账号 provisioning：
#   make up 在 bootstrap 阶段通过 init.sql 脚本创建：
#     - CREATE SCHEMA homeos / finance
#     - CREATE ROLE hc_homeos / hc_finance LOGIN PASSWORD 'CHANGE_ME'
#     - GRANT USAGE ON SCHEMA {schema} TO hc_{code}
#     - ALTER DEFAULT PRIVILEGES ... GRANT SELECT,INSERT,UPDATE,DELETE
#     - REVOKE ALL ON SCHEMA public FROM hc_{code}
#   详见 deploy/init-schema.sql（由 docker-entrypoint-initdb.d 自动执行）
#
# 健康检查判据（§10.1 末条「本 schema 可连 + JetStream 已连」）：svc-{code} 容器的
#   /healthz 定义在 S1-C2 的 obs.Health 里；基础容器的探活定义在 compose 文件里，
#   本口用 --wait 收敛后打印 ps。
# =============================================================================
up:
	@set -eu; \
	for f in $(COMPOSE_FILE) $(NGINX_CONF) $(ENV_FILE); do \
		if [ ! -f "$${f}" ]; then \
			echo "make up：前置件缺失 -> $${f}"; \
			echo "  $(COMPOSE_FILE) 与 $(NGINX_CONF) 由 S1-D 交付；$(ENV_FILE) 由 $(DEPLOY_DIR)/env.local.example 复制得到（§十三：端口与 env 在 deploy/env.local.example）。"; \
			exit 2; \
		fi; \
	done; \
	if [ ! -f "$(WEB_DIR)/dist/build/h5/index.html" ]; then \
		echo "make up：H5 静态产物缺失 -> $(WEB_DIR)/dist/build/h5/index.html"; \
		echo "  web 容器需要编译后的前端产物；请先 cd $(WEB_DIR) && npm run build:h5。"; \
		echo "  本口不代跑前端构建（发版脚本保持最薄，§10.1 原文）。"; \
		exit 2; \
	fi; \
	if [ ! -d "$(WEB_DIR)/dist" ] || [ ! -f "$(WEB_DIR)/Dockerfile" ]; then \
		echo "make up：web Dockerfile 或 dist 目录缺失"; \
		echo "  请确保 $(WEB_DIR)/Dockerfile 存在且 $(WEB_DIR)/dist 目录有构建产物。"; \
		exit 2; \
	fi; \
	if [ ! -d "admin/dist" ] || [ ! -f "admin/Dockerfile" ]; then \
		echo "make up：admin Dockerfile 或 dist 目录缺失"; \
		echo "  请确保 admin/Dockerfile 存在且 admin/dist 目录有构建产物。"; \
		echo "  若暂无 admin 构建产物，可创建占位 index.html：mkdir -p admin/dist && echo '<html><body>Admin</body></html>' > admin/dist/index.html"; \
		exit 2; \
	fi; \
	echo "[up 1/5] 未出生面前缀从 registry 现取（§1.3 唯一真源，不手抄）：$(DEPLOY_DIR)/gen-nginx-unborn.mjs"; \
	if ! command -v $(NODE) >/dev/null 2>&1; then \
		echo "make up：找不到 node -> $(NODE)（可用 NODE=/path/to/node 覆盖；它调 registryd 取 registry.Unborn()）"; \
		exit 2; \
	fi; \
	$(NODE) $(DEPLOY_DIR)/gen-nginx-unborn.mjs; \
	echo "[up 2/5] 迁移（先跑一次性 migrate 容器，逐序列 up）"; \
	echo "         出处：§2.2 每服务一条独立序列、§10.1 一次性 migrate 容器；"; \
	echo "         失败即退出 —— 序列没跑到最新版本，后面起服务连的是空 schema"; \
	if ! $(COMPOSE) --env-file $(ENV_FILE) -f $(COMPOSE_FILE) run -T --rm migrate up; then \
		echo "make up：迁移步骤失败（deploy/migrate.sh 或 compose 的 migrate 服务）"; \
		exit 1; \
	fi; \
	echo "[up 2/5] 起常驻/静态容器（postgres16 + nats + svc-homeos + svc-finance + web + admin + nginx）"; \
	echo "         migrate 不在这里点名：它是一次性容器，跑完就 exited (0)，而本机 compose v5.5.1 的"; \
	echo "         'up --wait' 把任何 exited 都算失败（实测 up -d --wait postgres16 nats migrate -> 1，"; \
	echo "         去掉 migrate -> 0）；迁移已在上一步 compose run --rm migrate 里真跑过"; \
	$(COMPOSE) --env-file $(ENV_FILE) -f $(COMPOSE_FILE) up -d --wait postgres16 nats svc-homeos svc-finance web admin nginx; \
	echo "[up 3/5] 健康收敛后的状态"; \
	$(COMPOSE) --env-file $(ENV_FILE) -f $(COMPOSE_FILE) ps; \
	echo "[up 4/5] Schema 与账号 provisioning 验证"; \
	echo "  检查 homeos schema: docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE) exec postgres16 psql -U postgres -d \$$POSTGRES_DB -c '\\dn homeos'"; \
	echo "  检查 finance schema: docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE) exec postgres16 psql -U postgres -d \$$POSTGRES_DB -c '\\dn finance'"; \
	echo "  检查 hc_homeos 角色: docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE) exec postgres16 psql -U postgres -d \$$POSTGRES_DB -c '\\du hc_homeos'"; \
	echo "  检查 hc_finance 角色: docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE) exec postgres16 psql -U postgres -d \$$POSTGRES_DB -c '\\du hc_finance'"; \
	echo "[up 5/5] 服务健康检查入口"; \
	echo "  homeos healthz: curl http://127.0.0.1:\$$NGINX_HTTP_PORT/api/homeos/healthz"; \
	echo "  finance healthz: curl http://127.0.0.1:\$$NGINX_HTTP_PORT/api/finance/healthz"; \
	echo "  H5 首页: 浏览器打开 http://127.0.0.1:\$$NGINX_HTTP_PORT"

# =============================================================================
# backup —— §10.1：当期各 schema 一份 pg_dump -Fd + NATS 流快照 + uploads tar
#              -> 加密落 deploy/data/backups
# uploads 目录形态出自 PRD 14.7（uploads/{family_id}/）与 §8.1 票据附件行。
# 加密凭据的配置项名文档未登记（§10.1 只写「加密」二字），归 S1-E 定版并由人类确认。
# 接口约定：传给脚本的 COMPOSE 值含空格（"docker compose"），S1-E 须把它当命令前缀用
# （${COMPOSE} 不加引号做词分割，或 set -- ${COMPOSE} 后用 "$@"）；COMPOSE_FILE、ENV_FILE、
# BACKUP_DIR / RESTORE_POINT / ROLLBACK_TAG 是路径或单值，可直接加引号使用。
# =============================================================================
backup:
	@set -eu; \
	for f in $(BACKUP_SH) $(COMPOSE_FILE) $(ENV_FILE); do \
		if [ ! -f "$${f}" ]; then \
			echo "make backup：前置件缺失 -> $${f}"; \
			echo "  $(BACKUP_SH) 归属 S1-E（备份/恢复/回滚脚本）；$(COMPOSE_FILE) 归属 S1-D；$(ENV_FILE) 由 $(DEPLOY_DIR)/env.local.example 复制（§十三）。"; \
			echo "  本卡（S1-A）只交付命令口，不以 echo 冒充执行，故以非零码退出。"; \
			exit 2; \
		fi; \
	done; \
	mkdir -p $(BACKUP_DIR); \
	COMPOSE="$(COMPOSE)" COMPOSE_FILE="$(COMPOSE_FILE)" ENV_FILE="$(ENV_FILE)" BACKUP_DIR="$(BACKUP_DIR)" \
		sh $(BACKUP_SH)

# =============================================================================
# restore —— §10.1：恢复到指定备份点：整集群 -> 逐服务重放迁移 -> 起当期服务与消费组
# 不允许只恢复单个 schema（§10.1：跨面逻辑引用会立刻失配）。
# =============================================================================
restore:
	@set -eu; \
	if [ -z "$(RESTORE_POINT)" ]; then \
		echo "make restore：未指定备份点。用法：make restore RESTORE_POINT=$(BACKUP_DIR)/<备份目录名>"; \
		echo "  §10.1 的语义是「恢复到指定备份点」，缺该参数不得隐式挑最新一份，故以非零码退出。"; \
		exit 2; \
	fi; \
	for f in $(RESTORE_SH) $(COMPOSE_FILE) $(ENV_FILE); do \
		if [ ! -f "$${f}" ]; then \
			echo "make restore：前置件缺失 -> $${f}"; \
			echo "  $(RESTORE_SH) 归属 S1-E；$(COMPOSE_FILE) 归属 S1-D；$(ENV_FILE) 由 $(DEPLOY_DIR)/env.local.example 复制（§十三）。"; \
			exit 2; \
		fi; \
	done; \
	if [ ! -e "$(RESTORE_POINT)" ]; then \
		echo "make restore：备份点不存在 -> $(RESTORE_POINT)（备份落盘目录见 §10.1：$(BACKUP_DIR)）"; \
		exit 2; \
	fi; \
	COMPOSE="$(COMPOSE)" COMPOSE_FILE="$(COMPOSE_FILE)" ENV_FILE="$(ENV_FILE)" BACKUP_DIR="$(BACKUP_DIR)" \
		RESTORE_POINT="$(RESTORE_POINT)" sh $(RESTORE_SH)

# =============================================================================
# restore-drill —— P1 首次恢复演练（14.5 第 9 项门禁 + 12.3 每季度演练）
# 全栈 Compose 重建：停所有容器 → 删数据卷 → 重新构建并启动 → 验证健康检查
# 这是「干净机器 → 可用环境」的完整演练，不依赖任何备份文件。
# =============================================================================
.PHONY: restore-drill
restore-drill:
	@echo "=== P1 Restore Drill: Stopping all containers ==="
	-$(COMPOSE) --env-file $(ENV_FILE) -f $(COMPOSE_FILE) down
	
	@echo "=== Removing data volumes ==="
	rm -rf deploy/data/postgres/*
	
	@echo "=== Rebuilding and starting ==="
	@if ! $(COMPOSE) --env-file $(ENV_FILE) -f $(COMPOSE_FILE) up -d --build --wait; then \
		echo ""; \
		echo "⚠️  Build failed. This may be due to:"; \
		echo "   1. Network issues preventing image pulls from Docker Hub"; \
		echo "   2. Missing base images (try: docker pull alpine:3.20, docker pull golang:1.27-alpine)"; \
		echo "   3. Registry configuration issues"; \
		echo ""; \
		echo "Note: If you have images from alternative registries, you may need to tag them:"; \
		echo "   docker tag docker.m.daocloud.io/library/alpine:3.20 alpine:3.20"; \
		echo "   docker tag docker.m.daocloud.io/library/golang:1.27-alpine golang:1.27-alpine"; \
		echo ""; \
		exit 1; \
	fi
	
	@echo "=== Waiting for services to stabilize (30s) ==="
	sleep 30
	
	@echo "=== Verifying healthz endpoints ==="
	@if curl -f http://localhost:8080/api/homeos/healthz && curl -f http://localhost:8080/api/finance/healthz; then \
		echo ""; \
		echo "=== Restore drill completed successfully ==="; \
		echo "All services are healthy and responding."; \
	else \
		echo ""; \
		echo "⚠️  Services may still be starting. Check status with:"; \
		echo "   docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE) ps"; \
		echo "   docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE) logs"; \
		exit 1; \
	fi

# =============================================================================
# rollback —— §10.1：只切回上一版本镜像 tag，校验备份完整性并打印，不自动动数据
# 部署包上传与目标机执行由人手动完成，脚本不做远程编排（§10.1 原文）。
# =============================================================================
rollback:
	@set -eu; \
	for f in $(ROLLBACK_SH) $(COMPOSE_FILE) $(ENV_FILE); do \
		if [ ! -f "$${f}" ]; then \
			echo "make rollback：前置件缺失 -> $${f}"; \
			echo "  $(ROLLBACK_SH) 归属 S1-E；$(COMPOSE_FILE) 归属 S1-D；$(ENV_FILE) 由 $(DEPLOY_DIR)/env.local.example 复制（§十三）。"; \
			exit 2; \
		fi; \
	done; \
	COMPOSE="$(COMPOSE)" COMPOSE_FILE="$(COMPOSE_FILE)" ENV_FILE="$(ENV_FILE)" ROLLBACK_TAG="$(ROLLBACK_TAG)" BACKUP_DIR="$(BACKUP_DIR)" \
		sh $(ROLLBACK_SH)

# =============================================================================
# seed-perf —— 按 21.1 上限 ×1.5 构造压测样本（6 万流水 + HomeOS 三对象，CI 缓存）
# 当前为 fail-fast 状态，待 S6/S14 交付后启用。
# =============================================================================
seed-perf:
	@echo "make seed-perf：未实现（本卡按人类定版做成 fail-fast，非桩）。"; \
	echo "本口属 18.2#13/21.1 的压测载体，依赖财务流水表与压测栈，归属 S6/S14，未到期不得运行"; \
	exit 1

# =============================================================================
# soak —— 18.2#13 长压测试：≥24 小时、净样本 ≥10 万次、专用压测家庭
# 用法：make soak [FAMILY_ID=<uuid>] [DURATION_HOURS=24] [TARGET_REQUESTS=100000]
#
# 使用独立的压测家庭（family_id 固定），跑完整库丢弃，不并入生产样本库、
# 不进每日对账与动态流口径。报告落 deploy/data/soak/report.json。
#
# 该测试在后台运行，可通过 tail -f deploy/logs/soak_*.log 查看进度。
# =============================================================================
.PHONY: soak
soak:
	@set -eu; \
	FAMILY_ID=$${FAMILY_ID:-"00000000-0000-0000-0000-000000000001"}; \
	DURATION_HOURS=$${DURATION_HOURS:-24}; \
	TARGET_REQUESTS=$${TARGET_REQUESTS:-100000}; \
	LOG_FILE="deploy/logs/soak_$$(date +%Y%m%d_%H%M%S).log"; \
	mkdir -p deploy/logs deploy/data/soak; \
	echo "=== P1 Long Pressure Test (≥24h, ≥100k requests) ==="; \
	echo "Family ID: $${FAMILY_ID}"; \
	echo "Duration: $${DURATION_HOURS} hours"; \
	echo "Target Requests: $${TARGET_REQUESTS}"; \
	echo "Start Time: $$(date)"; \
	echo "Log File: $${LOG_FILE}"; \
	echo ""; \
	echo "Starting soak test in background..."; \
	nohup ./deploy/scripts/soak_test.sh "$${FAMILY_ID}" "$${DURATION_HOURS}" "$${TARGET_REQUESTS}" > "$${LOG_FILE}" 2>&1 & \
	PID=$$!; \
	echo "Soak test started in background (PID: $$PID)"; \
	echo "Check progress: tail -f $${LOG_FILE}"; \
	echo "Report will be saved to: $(SOAK_REPORT)"

# =============================================================================
# dev-homeos / dev-finance —— §十三：make dev-{code} 把某服务跑出容器外调试
# 服务代码归属 S1-C。cmd 布局本卡定为 $(SERVER_DIR)/services/svc-{code}/cmd/svc-{code}/main.go
# （PRD 22.4「每服务一个 cmd」）；S1-C 若换形状，改下面两处包路径即可。
# =============================================================================
dev-homeos:
	@set -eu; \
	if [ ! -x "$(GO)" ]; then \
		echo "make dev-homeos：找不到 go 可执行文件 -> $(GO)（可用 GO=/path/to/go 覆盖；须为 1.27.x 且 GOTOOLCHAIN=local）"; \
		exit 2; \
	fi; \
	if [ ! -f "$(ENV_FILE)" ]; then \
		echo "make dev-homeos：env 缺失 -> $(ENV_FILE)，请先 cp $(DEPLOY_DIR)/env.local.example $(ENV_FILE) 并填 CHANGE_ME（§十三）"; \
		exit 2; \
	fi; \
	pkg=./services/svc-homeos/cmd/svc-homeos; \
	if [ ! -d "$(SERVER_DIR)/services/svc-homeos/cmd/svc-homeos" ]; then \
		echo "make dev-homeos：服务代码缺失 -> $(SERVER_DIR)/services/svc-homeos/cmd/svc-homeos"; \
		echo "  两服务骨架（cmd/ + handler/service/repo/model/dto + 本服务订阅器）归属 S1-C；本卡（S1-A）只交付命令口。"; \
		exit 2; \
	fi; \
	echo "[dev] svc-homeos 跑在容器外（§十三），GOTOOLCHAIN=local，env 取自 $(ENV_FILE)"; \
	set -a; . ./"$(ENV_FILE)"; set +a; \
	GOTOOLCHAIN=local $(GO) -C $(SERVER_DIR) run "$${pkg}"

dev-finance:
	@set -eu; \
	if [ ! -x "$(GO)" ]; then \
		echo "make dev-finance：找不到 go 可执行文件 -> $(GO)（可用 GO=/path/to/go 覆盖；须为 1.27.x 且 GOTOOLCHAIN=local）"; \
		exit 2; \
	fi; \
	if [ ! -f "$(ENV_FILE)" ]; then \
		echo "make dev-finance：env 缺失 -> $(ENV_FILE)，请先 cp $(DEPLOY_DIR)/env.local.example $(ENV_FILE) 并填 CHANGE_ME（§十三）"; \
		exit 2; \
	fi; \
	pkg=./services/svc-finance/cmd/svc-finance; \
	if [ ! -d "$(SERVER_DIR)/services/svc-finance/cmd/svc-finance" ]; then \
		echo "make dev-finance：服务代码缺失 -> $(SERVER_DIR)/services/svc-finance/cmd/svc-finance"; \
		echo "  两服务骨架（cmd/ + handler/service/repo/model/dto + 本服务订阅器）归属 S1-C；本卡（S1-A）只交付命令口。"; \
		exit 2; \
	fi; \
	echo "[dev] svc-finance 跑在容器外（§十三），GOTOOLCHAIN=local，env 取自 $(ENV_FILE)"; \
	set -a; . ./"$(ENV_FILE)"; set +a; \
	GOTOOLCHAIN=local $(GO) -C $(SERVER_DIR) run "$${pkg}"

# =============================================================================
# inject-faults —— P1 故障注入测试（每服务各 20 次，PRD 18.2#13）
# 对 homeos 和 finance 分别执行 20 次随机故障注入，验证服务容错能力。
# 故障类型：数据库中断、NATS 中断、内存压力、磁盘满、网络延迟、进程崩溃
# 出口判据：成功率 ≥95%
# =============================================================================
.PHONY: inject-faults
inject-faults:
	@echo "=== P1 Fault Injection Test (20 times per service) ==="
	@echo "Testing svc-homeos..."
	./deploy/scripts/fault_injection.sh homeos 20
	
	@echo ""
	@echo "Testing svc-finance..."
	./deploy/scripts/fault_injection.sh finance 20
	
	@echo ""
	@echo "=== All fault injection tests completed ==="

# =============================================================================
# check-device-lock —— P1 设备锁与 L3 解锁链路抽检（PRD 18.2#14）
# 验证敏感数据访问控制：未解锁隐藏、解锁后可见、PIN 失败锁定、跨设备重解锁
# 测试场景：5 个（未解锁访问、解锁、解锁后访问、PIN 锁定、跨设备）
# =============================================================================
.PHONY: check-device-lock
check-device-lock:
	@echo "=== P1 Device Lock & L3 Unlock Check ==="
	./deploy/scripts/device_lock_check.sh


# =============================================================================
# acceptance —— P1 可交付验收（G1 API 门禁）
#
# 为什么要独立成 target：验收必须是**一条命令、可重复、结果确定**的，
# 不能靠「人工点一遍页面觉得没问题」。前几轮之所以反复返工，正是因为
# 验收依赖执行者的临场判断 —— 同一个缺陷被不同的人看成不同的问题。
#
# G1（API 层）：从 cmd/*/main.go 自动发现**实际注册**的全部路由，逐条发真实请求断言。
#   路由清单不手写：手写清单等于拿「我以为有哪些接口」当 oracle，
#   上一轮 /statistics/summary 与 /members 就是这样漏掉的。
#
# 用法：
#   make acceptance            # G1
#   make acceptance-verbose    # G1 + 打印每条明细
#   make acceptance-all        # G1 + G2 浏览器 + G3 构建 + G4 数据 + G5 权限
#
# 前置：两个服务与 PostgreSQL/NATS 已在跑（make up 或容器常驻）。
# =============================================================================
PYTHON ?= python3
ACCEPT_DIR := server/test/acceptance

.PHONY: acceptance
acceptance:
	@echo "=== G1 · API 契约验收（路由自动发现 + 逐条实调）==="
	@$(PYTHON) $(ACCEPT_DIR)/route_discovery.py > $(ACCEPT_DIR)/routes.json
	@$(PYTHON) $(ACCEPT_DIR)/api_acceptance.py --json $(ACCEPT_DIR)/result-g1.json

.PHONY: acceptance-verbose
acceptance-verbose:
	@echo "=== G1 · API 契约验收（verbose）==="
	@$(PYTHON) $(ACCEPT_DIR)/route_discovery.py > $(ACCEPT_DIR)/routes.json
	@$(PYTHON) $(ACCEPT_DIR)/api_acceptance.py --verbose --json $(ACCEPT_DIR)/result-g1.json

# =============================================================================
# acceptance-g3 —— 构建与静态门禁
#
# 覆盖：Go 侧 build/vet/test/gofmt + 前端 check/tc/H5 构建。
# H5 构建输出到 web/dist-acceptance 而不是 web/dist：dist 里有上一轮留下的 57 个产物，
# 清理它们会触发环境的批量删除保护从而中断构建。构建到独立目录既绕开该保护，
# 也让「本轮产物」与「历史产物」可分辨。
# =============================================================================
.PHONY: acceptance-g3
acceptance-g3:
	@echo "=== G3-1 · Go 格式化 / 构建 / vet / 单测 ==="
	@cd $(SERVER_DIR) && GOTOOLCHAIN=local $(GO) fmt ./... >/dev/null
	@cd $(SERVER_DIR) && test -z "$$(GOTOOLCHAIN=local $(GO) run cmd/gofmtcheck 2>/dev/null || $(HOME)/sdk/go/bin/gofmt -l .)" \
		&& echo "  PASS gofmt" || (echo "  FAIL gofmt: 上述文件未格式化"; exit 1)
	@cd $(SERVER_DIR) && GOTOOLCHAIN=local $(GO) build ./... && echo "  PASS go build"
	@cd $(SERVER_DIR) && GOTOOLCHAIN=local $(GO) vet ./... && echo "  PASS go vet"
	@cd $(SERVER_DIR) && GOTOOLCHAIN=local $(GO) test ./... 2>&1 | tee /tmp/hc-gotest.txt | grep -E '^(FAIL|---)' \
		&& (echo "  FAIL go test"; exit 1) || echo "  PASS go test"
	@echo "=== G3-2 · 前端类型检查 ==="
	@cd web && npx tsc --noEmit -p tsconfig.json && echo "  PASS tsc (app)"
	@cd web && npx tsc --noEmit -p tests/tsconfig.json && echo "  PASS tsc (tests)"
	@echo "=== G3-3 · 前端工程检查（check:pages 13 项 + check:request 6 项）==="
	@cd web && npm run check >/dev/null && echo "  PASS npm run check"
	@echo "=== G3-4 · H5 构建 ==="
	@cd web && npx uni build --outDir dist-acceptance 2>&1 | grep -qE 'DONE' && echo "  PASS uni build"

# =============================================================================
# acceptance-g5 —— 权限与越权边界门禁
#
# 验收的是**安全性质**，不是「接口能不能调通」。用两个真实账号互相试探 ——
# 真实 token 才有意义（签名完全合法，只是家庭不对），伪造的 payload 测不出这类越权。
#
# 覆盖：无 token 401 / 伪造 token 401 / 跨家庭不可见 / 家庭数据边界 /
#       无效 refresh 401 / 非法 family_id 4xx。
# =============================================================================
.PHONY: acceptance-g5
acceptance-g5:
	@echo "=== G5 · 权限与越权边界验收 ==="
	@$(PYTHON) $(ACCEPT_DIR)/route_discovery.py > $(ACCEPT_DIR)/routes.json
	@$(PYTHON) $(ACCEPT_DIR)/authz_acceptance.py
