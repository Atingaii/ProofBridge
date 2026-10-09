# ADR 021: Refreshable model metadata and reference pricing

Status: Accepted

## Context

Compiled model lists cannot recognize new releases without deployment. Provider
`/models` discovers available IDs but generally does not publish reliable prices
or reasoning effort values.

## Decision

Hub maintains a bounded, periodically refreshed public catalog from
`https://models.dev/api.json`. Exact first-party provider/model entries supply
reference prices, explicit reasoning options and context limits. Never send
credentials to this endpoint. Ambiguous bare model IDs are not resolved.

Load the last valid cache beside the Hub database, refresh in the background at
startup and every six hours, and retain the last valid snapshot on failures.
Use timeouts, a response size limit and atomic cache replacement. Request paths
only read memory. Existing compiled metadata/pricing remain offline fallbacks.
Provider model discovery and connection tests still determine actual access;
catalog presence does not imply transport compatibility. Effort options are
restricted to native CLI values, with no invented levels for unknown models.

Reference prices are labelled `models.dev`; they are current estimates, not
historical invoices. Missing rate dimensions remain unpriced. Native provider
amounts take precedence. Existing preset choices are retained when supported;
no credentials, URLs or selected model are changed automatically.

## Consequences / alternatives

This adds one public data dependency without another service or database
migration. Offline installations retain cached/compiled information. Automatic
recognition follows upstream publication, not necessarily release day. A static
table alone cannot meet the requirement; scraping each vendor adds unnecessary
maintenance. ADR 013/014 remain relevant for CLI constraints; this ADR replaces
their compiled-only metadata authority.

## Validation

Test exact and ambiguous IDs, newly introduced models, changed prices,
unsupported reasoning values, invalid/oversized responses and stale-cache
retention. Existing provider costs and native token accounting must pass their
regression tests.

Implementation: `internal/modelcatalog/catalog.go:Catalog`,
`internal/hub/model_catalog.go:reviewedModelMetadata`,
`internal/usagepricing/catalog.go:Resolve`.
