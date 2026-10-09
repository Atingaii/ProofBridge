# ADR-018: Additional Administrator Usernames

Status: Accepted

## Context and goals

An existing account must be granted administrator access without removing the
bootstrap administrator. Previously `internal/hub/server.go:isAdminUser` only
recognized `auth.bootstrap_username`.

## Decision

Add the YAML-only `auth.admin_usernames` list, defaulting to empty. Membership
adds to the bootstrap administrator, with trimmed, case-insensitive exact
username matching. Empty entries never grant access. Keep the same predicate
for HTTP authorization, `user.isAdmin`, and administrator feature rollouts.
Configuration changes take effect after restarting the Hub.

## Non-goals and impact

No new role-management API, account creation, password changes, database
migration, JWT claims, Bridge protocol, or frontend changes. Existing ownership
rules and the administrator's read-only cross-user capabilities remain intact.
Operators should only list existing accounts; a listed username also grants
access to a future account registered with that name.

## Implementation and exit gates

1. Add the config field and document configuration and restart behavior.
2. Extend the existing shared administrator predicate.
3. Verify YAML loading, bootstrap preservation, additional administrator access,
   ordinary-user denial, removal, empty entries, and UI/rollout consistency.
4. Build with bounded concurrency, deploy to the isolated Hub, and verify the
   requested account through the live API while retaining rollback config.

## Reviewer Q&A

**Why not change the bootstrap username?**

That would revoke the current administrator instead of granting another one.

**Why not add persisted roles now?**

The existing operator-managed configuration boundary suffices for this grant.
Persisted roles and a management API would require a separate authorization
design. No username is hard-coded into source or public example configuration.

**Do existing sessions need new JWTs?**

No. Each request resolves the user and checks the active Hub configuration.
Refreshing the browser reloads administrator navigation from `/api/me`.

## Verification

On 2026-09-08, focused authorization/configuration tests and the full Go suite
passed with `GOMAXPROCS=2`, `-p 1`, and `-parallel 1`. The deployment build and
`git diff --check` passed. `make doc-lint` reported zero errors and the existing
`Server.Run` anchor warning. Live isolated-Hub checks confirmed both the
bootstrap and newly configured administrator metadata and middleware access;
an ordinary account remained denied. Bridge reconnects retained active work.
