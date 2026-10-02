#!/bin/bash
set -euo pipefail

SERVICE=$1  # homeos or finance
INJECTION_COUNT=${2:-20}
BASE_URL="http://localhost:8080"
COMPOSE_FILE="deploy/docker-compose.yml"

echo "=== Fault Injection Test ==="
echo "Service: $SERVICE"
echo "Injection Count: $INJECTION_COUNT"
echo "Start Time: $(date)"

SUCCESS_COUNT=0
FAIL_COUNT=0

for i in $(seq 1 $INJECTION_COUNT); do
    echo ""
    echo "--- Injection #$i ---"
    
    # Randomly select fault type (1-6)
    FAULT_TYPE=$((RANDOM % 6 + 1))
    
    case $FAULT_TYPE in
        1)
            echo "Fault Type: Database connection interrupt"
            docker compose -f $COMPOSE_FILE stop postgres
            sleep 5
            docker compose -f $COMPOSE_FILE start postgres
            sleep 10  # Wait for recovery
            ;;
        2)
            echo "Fault Type: NATS connection interrupt"
            docker compose -f $COMPOSE_FILE stop nats
            sleep 5
            docker compose -f $COMPOSE_FILE start nats
            sleep 10
            ;;
        3)
            echo "Fault Type: Memory pressure (concurrent requests)"
            for j in $(seq 1 50); do
                curl -s -o /dev/null "$BASE_URL/api/$SERVICE/healthz" &
            done
            wait
            sleep 5
            ;;
        4)
            echo "Fault Type: Disk full simulation"
            dd if=/dev/zero of=/tmp/disk_fill bs=1M count=100 2>/dev/null || true
            sleep 5
            rm -f /tmp/disk_fill
            ;;
        5)
            echo "Fault Type: Network latency injection"
            # This requires tc command, skip if not available
            if command -v tc &> /dev/null; then
                tc qdisc add dev lo root netem delay 5000ms 2>/dev/null || true
                sleep 10
                tc qdisc del dev lo root netem delay 5000ms 2>/dev/null || true
            else
                echo "tc not available, skipping"
            fi
            ;;
        6)
            echo "Fault Type: Process crash and restart"
            CONTAINER_ID=$(docker compose -f $COMPOSE_FILE ps -q svc-$SERVICE | head -1)
            if [ -n "$CONTAINER_ID" ]; then
                docker kill $CONTAINER_ID
                sleep 10  # Wait for Docker to restart
            fi
            ;;
    esac
    
    # Verify service recovery
    HTTP_CODE=$(curl -s -w "%{http_code}" -o /dev/null "$BASE_URL/api/$SERVICE/healthz" || echo "000")
    
    if [ "$HTTP_CODE" -eq 200 ]; then
        echo "✅ Recovery successful (HTTP $HTTP_CODE)"
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        echo "❌ Recovery failed (HTTP $HTTP_CODE)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
    
    # Small delay between injections
    sleep 2
done

echo ""
echo "=== Fault Injection Test Completed ==="
echo "Total Injections: $INJECTION_COUNT"
echo "Successful Recoveries: $SUCCESS_COUNT"
echo "Failed Recoveries: $FAIL_COUNT"
echo "Success Rate: $((SUCCESS_COUNT * 100 / INJECTION_COUNT))%"

if [ $SUCCESS_COUNT -ge $((INJECTION_COUNT * 95 / 100)) ]; then
    echo "✅ SLO MET: Success rate >= 95%"
    exit 0
else
    echo "❌ SLO NOT MET: Success rate < 95%"
    exit 1
fi
