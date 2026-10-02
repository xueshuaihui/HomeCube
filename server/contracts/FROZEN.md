# Contract Freeze Rules

## Overview

All contracts under `server/contracts/` are frozen as of P1-M2 closeout (S19). This document defines the rules for managing breaking and non-breaking changes.

## Frozen Contracts

- `openapi/finance.yaml` - Finance OpenAPI specification (frozen at 2026-10-03, v1.0.0)
- `events/finance.yaml` - Finance event directory (frozen at 2026-10-03, v1.0.0)

## Change Management Rules

### Breaking Changes

A **breaking change** is any modification that would cause existing consumers to fail or behave incorrectly. Examples include:

- Removing or renaming an API endpoint
- Removing or changing the type of a required field
- Changing the semantics of an existing field
- Removing an event type from the event directory

**Requirements for breaking changes:**

1. Must bump the major version number (e.g., 1.0.0 → 2.0.0)
2. Must provide a deprecation period per PRD 10.4 (one release cycle)
3. Old version must remain available during the deprecation period
4. Must update all consumer services before removing the old version

### Non-Breaking Changes

A **non-breaking change** is any modification that maintains backward compatibility. Examples include:

- Adding new optional fields to request/response bodies
- Adding new API endpoints
- Adding new event types to the event directory
- Updating descriptions or documentation

**Requirements for non-breaking changes:**

1. No version bump required
2. Can be deployed immediately
3. Should maintain backward compatibility with existing consumers

## Version History

| Version | Date       | Description                  |
|---------|------------|------------------------------|
| 1.0.0   | 2026-10-03 | Initial freeze (P1-M2 S19)   |

## References

- PRD 10.4: Deprecation policy
- PRD 16.2: Contract governance
- docs/p1-tech-plan.md §十一 S19: P1-M2 closeout requirements
