# Assessment scopes and Target observations

Commander accepts an optional `assessment` when starting an operation through HTTP (`POST /api/v1/operations`) or MCP (`start_operation`). Untargeted clients can omit it. The operation stores an immutable copy, exposes it through both adapters, and carries it into every action execution, including automatically proposed tasks and recovered work.

```json
{
  "target_id": "b322f680-d3cb-47af-8588-bf087160a830",
  "target_name": "Acme production",
  "revision": 3,
  "network_id": "office",
  "scope": {
    "networks": [{"id": "office", "name": "Office", "cidrs": ["10.0.0.0/24"]}],
    "domains": [{"hostname": "app.example.com", "include_subdomains": false}]
  }
}
```

Target ownership and organization permissions belong to the calling application. Commander does not look up, edit or reassign a Target. Network IDs describe independent address spaces, not labels inferred from CIDRs. When a destination matches several Networks, a selection is required. The selected Network must contain the address being assessed.

## Declared destinations

A mission or action declares destination semantics explicitly in its public JSON Schema:

```json
{"type":"object","properties":{"destination":{"type":"string","x-defos-destination":true}},"required":["destination"]}
```

The annotation also accepts arrays of destination strings. Matching supports IPs, CIDRs, exact ASCII/IDNA hostnames and URL hostnames. Domain matching is exact unless `include_subdomains` is true. DNS resolution does not grant permission to assess every service on the returned IP. Local schema references, object properties, array items and composed schema branches preserve annotations. Schemas must use local references and bounded nesting; an annotation is never inferred from a field name.

The runner validates mission entry before starting and validates the selected executor's declared action destinations immediately before dispatch. This includes automatic follow-ups. Rejected automatic work fails the operation before that action is dispatched and retains previously accepted results. Saved scope does not change after a Target edit elsewhere.

Actions whose actual destinations come from referenced artifacts must validate them at the adapter boundary after reading and verifying that context, before making network requests:

```go
if scope := action.AssessmentFromContext(ctx); scope != nil {
    if err := scope.CheckDestination(endpoint); err != nil {
        return action.Result{}, err
    }
}
```

The framework injects a cloned snapshot into the action context; callers cannot alter the persisted operation through that value. Prepared execution claims include the same snapshot, and recovery rejects a command whose assessment differs from the saved claim.

This is application-level assessment checking. Arbitrary shell commands and scripts keep their existing capabilities; Commander does not parse their bytes or guarantee network containment. Use deployment network controls for that guarantee.

## Versioned observations

Actions can publish a normalized batch alongside their existing output and artifacts using `action.Result.Observations`. Public task results expose the same `observations` envelope through HTTP and MCP. Existing result consumers can ignore this optional field.

```json
{
  "version": 1,
  "records": [{
    "id": "service-10.0.0.5-tcp-443",
    "kind": "service",
    "network_id": "office",
    "address": "10.0.0.5",
    "port": 443,
    "transport": "tcp",
    "protocol": "https",
    "observed_at": "2026-10-30T12:00:00Z",
    "evidence": "TLS handshake and HTTP response collected"
  }]
}
```

| Kind | Meaning |
| --- | --- |
| `host` | An observed address/host. Optional `hostname` is descriptive, not sufficient identity evidence. |
| `service` | An observed endpoint, with address, port/transport and optional protocol/product/version/state. |
| `finding` | A security issue tied to an address/service, with `title` and supporting `evidence`. A stable `issue_id` enables conservative occurrence grouping. |
| `connection` | Evidence supporting a relationship between `source` and `destination` endpoints (`network_id` and optional address), with `relationship`, `directed` and `evidence`. |

Records have IDs unique within a result. Keep their content and observation timestamp stable when replaying cached evidence; a genuinely new observation gets a new occurrence/source. Timestamps are optional, and absence means unknown rather than ingestion time. A producer can provide `identity: {namespace, value, evidence}` only when its evidence supports merging several interfaces into one host. `details` can retain additional structured facts.

Platform retains observations by operation, task and producer record identity. It merges addresses only within known Network identities, preserves ambiguous addresses separately, and keeps dated provenance. Ordinary service discovery is not automatically a security finding. An old result without the normalized envelope remains available as operation evidence but does not become topology through field-name heuristics.

## Persistence and compatibility

Filesystem operation metadata and MongoDB operation bodies retain assessment snapshots. Task result records retain observations. Existing operations lacking either field continue to load. Do not reconstruct missing historical scope from a current Target definition.

Release the updated framework/provider modules and Commander adapters together before rebuilding external agents against the new framework contracts. The cybersec-agent integration consumes `action.AssessmentFromContext` and `observations.Batch`; its released module dependency must be advanced to the release containing these APIs. A local Go workspace can validate coordinated development without adding a sibling-module replacement to an external agent's `go.mod`.

## E2E checks

```sh
go test ./apps/adapters/api/tests/e2e -run '^TestE2EAssessmentScopeEvidenceAndAutomaticWork$' -count=1 -timeout=4m
```

This starts real API/controller/worker/MCP processes. It verifies saved scope across controller restart, public evidence, rejection of an automatic out-of-scope action before dispatch, exact/subdomain behavior and overlapping Network selection. The same scenario accepts the existing `COMMANDER_SESSION_E2E_DATABASE_URL`/`COMMANDER_SESSION_E2E_BUS_URL` isolated-provider configuration. No unit-test suite is invoked.
