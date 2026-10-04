# RT-1 Real-Stack End-to-End Test

This directory contains the infrastructure for running the RT-1 real-stack end-to-end proof, which validates the complete bill lifecycle chain in an isolated temporary database.

## What is RT-1?

RT-1 (Real-Stack Test 1) verifies that the event-driven architecture works correctly end-to-end:

1. **Bill Creation**: Finance service creates a bill and emits `finance.due.registered` event
2. **Due Registration**: HomeOS service consumes the event and creates a due registration
3. **Bill Payment**: Finance service processes payment and emits `finance.due.revoked` event
4. **Revocation**: HomeOS service consumes the revoke event and soft-deletes the registration
5. **Deduplication**: Event dedupe table prevents duplicate processing

## Problem Solved

The original RT-1 agent failed because it didn't configure environment variables for the temporary database DSN. This caused services to connect to the dev database (`homecube`) instead of the temporary test database (`hc_rt1_e2e`), making it impossible to verify that:

- Messages were consumed into the correct database
- The dedupe table had entries in the temp DB
- The full chain worked in isolation

## Files

- `setup-rt1-db.sh` - Creates and manages the temporary database
- `rt1-e2e-test.sh` - Runs the complete end-to-end test chain
- `../env.local.rt1` - Environment configuration pointing to temporary database

## Usage

### Quick Start (All-in-One)

```bash
# Run setup, test, and cleanup automatically
sh deploy/scripts/rt1-e2e-test.sh all
```

### Step-by-Step

```bash
# 1. Set up temporary database
sh deploy/scripts/setup-rt1-db.sh up

# 2. Start services with RT-1 environment
set -a; source deploy/env.local.rt1; set +a
make dev-homeos  # In one terminal
make dev-finance # In another terminal

# 3. Run the test
sh deploy/scripts/rt1-e2e-test.sh test

# 4. Clean up when done
sh deploy/scripts/setup-rt1-db.sh down
```

### Individual Commands

```bash
# Just set up the database
sh deploy/scripts/setup-rt1-db.sh up

# Just run tests (assumes services are already running)
sh deploy/scripts/rt1-e2e-test.sh test

# Just clean up
sh deploy/scripts/setup-rt1-db.sh down
```

## What Gets Verified

The test script verifies:

1. ✓ Bill creation succeeds and returns 201/200
2. ✓ Outbox entry created for `finance.due.registered`
3. ✓ Due registration appears in `homeos_due_registration` table
4. ✓ Bill payment succeeds
5. ✓ Revoke event emitted to outbox
6. ✓ Due registration is soft-deleted (revoked)
7. ✓ Dedupe table has entries preventing reprocessing
8. ⚠ JetStream messages (requires `nats` CLI, skipped if not available)

## Database Isolation

The temporary database `hc_rt1_e2e` is completely isolated from the dev database:

- Separate database name: `hc_rt1_e2e` vs `homecube`
- Same schemas: `homeos` and `finance`
- Same roles: `hc_homeos` and `hc_finance`
- Independent migration state
- No data leakage between environments

## Environment Variables

The key difference between `env.local` and `env.local.rt1` is the DSN configuration:

**env.local (dev):**
```bash
HOMEOS_DSN="... dbname=homecube ..."
FINANCE_DSN="... dbname=homecube ..."
```

**env.local.rt1 (test):**
```bash
HOMEOS_DSN="... dbname=hc_rt1_e2e ..."
FINANCE_DSN="... dbname=hc_rt1_e2e ..."
```

All other configuration (NATS URL, ports, etc.) remains the same.

## Troubleshooting

### Services can't connect to database

Ensure the temporary database is set up first:
```bash
sh deploy/scripts/setup-rt1-db.sh up
```

### Tests fail with "connection refused"

Make sure services are running with the RT-1 environment:
```bash
set -a; source deploy/env.local.rt1; set +a
```

### Dedupe table is empty

The dedupe table (`homeos_event_dedupe`) should have entries after events are processed. If it's empty:

1. Check that the consumer is running (look for consumer logs)
2. Verify NATS JetStream is running
3. Check that events are being published to the correct stream

### Migration fails

Ensure PostgreSQL is running and accessible:
```bash
docker compose --env-file deploy/env.local -f deploy/docker-compose.yml ps postgres16
```

## Technical Details

### How It Works

1. **Database Setup**: Creates `hc_rt1_e2e` database with proper schemas and roles
2. **Migration**: Runs all migrations against both `homeos` and `finance` schemas
3. **Service Configuration**: Services read DSN from environment via `obs.LoadConfig()`
4. **Event Flow**: 
   - Finance writes to `finance_outbox` → JetStream publishes → HomeOS consumer reads
   - HomeOS writes to `homeos_event_dedupe` to prevent duplicates
5. **Verification**: SQL queries check each step of the chain

### Key Tables

- `finance_outbox` - Pending events to be published
- `homeos_due_registration` - Registered due dates for bills
- `homeos_event_dedupe` - Prevents duplicate event processing
- `schema_migrations_homeos` / `schema_migrations_finance` - Migration state

### Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│  Finance    │     │   NATS       │     │   HomeOS    │
│  Service    │────▶│   JetStream  │────▶│   Service   │
└─────────────┘     └──────────────┘     └─────────────┘
      │                                        │
      ▼                                        ▼
┌─────────────────────────────────────────────────────┐
│         PostgreSQL (hc_rt1_e2e)                     │
│  ┌──────────────┐          ┌─────────────────────┐  │
│  │ finance      │          │ homeos              │  │
│  │  - outbox    │          │  - due_registration │  │
│  │  schema      │          │  - event_dedupe     │  │
│  └──────────────┘          └─────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

## Related Documentation

- PRD 14.5 - Event-driven architecture requirements
- tech plan §3.1 - NATS JetStream flow planning
- tech plan §3.3 - Outbox deliverer pattern
- tech plan §3.4 - Consumer retry and deduplication
