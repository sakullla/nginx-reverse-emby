# Revision And Identity Allocator Design

## Goal

Unify configuration `id` and `revision` allocation for the Go control plane behind a single shared allocator while preserving current resource semantics.

After this refactor:

- online CRUD paths and `backup/import` use the same allocation rules
- HTTP and L4 rules continue sharing one rule ID space
- relay listeners and certificates keep their own ID spaces
- imports preserve incoming IDs when they do not conflict inside the relevant namespace
- revision allocation follows one consistent policy for single-agent and multi-target mutations

## Scope

In scope:

- `panel/backend-go/internal/controlplane/service/rules.go`
- `panel/backend-go/internal/controlplane/service/l4.go`
- `panel/backend-go/internal/controlplane/service/relay.go`
- `panel/backend-go/internal/controlplane/service/certs.go`
- `panel/backend-go/internal/controlplane/service/backup.go`
- new shared allocator code and tests under `panel/backend-go/internal/controlplane/service/`

Out of scope:

- changing database schema
- changing API payload shape
- merging all resource IDs into one global space
- changing resource reference fields such as `relay_chain`, `certificate_id`, or `trusted_ca_certificate_ids`
- changing backup conflict policy beyond centralizing the existing behavior

## Current Problems

The repository currently has two parallel families of allocation logic:

- online CRUD code calculates `maxID`, `maxRevision`, and agent revision floors inline in each service
- `backup/import` has a separate `importRevisionAllocator` and separate per-resource ID collision handling

That causes three problems:

1. behavior drifts over time because fixes land in one path but not the others
2. shared semantics are hidden, especially the HTTP/L4 shared rule ID space
3. services mix business validation with allocation concerns, which makes future changes risky

## Design Decision

Use a shared allocator module inside the service layer.

This allocator centralizes numbering policy but does not own persistence or business validation. Services remain responsible for:

- loading current state
- validating and normalizing resource inputs
- deciding which resource type is being mutated
- persisting the final rows
- applying resource-specific side effects

The allocator is responsible only for computing legal next `id` and `revision` values from a snapshot of current control-plane state.

## Namespaces

The allocator manages three ID namespaces:

- `rule`
- `listener`
- `certificate`

`rule` covers both HTTP and L4 rules. This preserves the current behavior where those two resource types intentionally share one rule ID space.

`listener` and `certificate` remain separate. Their references are type-specific, so correctness depends on uniqueness within their own resource sets, not across all resources.

## Revision Semantics

The allocator must support two revision allocation modes.

### Single agent revision

Used for mutations that affect exactly one agent's desired config:

- HTTP rule create/update/delete
- L4 rule create/update/delete
- relay listener create/update/delete

The next revision is:

- greater than the highest relevant existing revision seen for that mutation path
- greater than or equal to the agent revision floor derived from current control-plane state

The revision floor is based on:

- remote agent: `max(current_revision, desired_revision)`
- local agent: `max(current_revision, desired_revision)` from local agent state

### Multi-target revision

Used for certificate mutations that affect more than one target agent, and for import flows that need to advance multiple agents together.

The next revision is:

- greater than the supplied `maxExistingRevision`
- greater than or equal to every target agent's current revision floor

When a revision is allocated for multiple target agents, the allocator advances the internal next-revision cursor for all of them.

## Allocator Snapshot

The allocator is initialized from a control-plane snapshot containing at least:

- agent rows
- local agent state
- all HTTP rules
- all L4 rules
- all relay listeners
- all managed certificates

It does not read from storage after construction. Services and import flows build the snapshot once, then use the allocator as a pure in-memory helper for that operation.

## Allocator Responsibilities

The allocator exposes shared operations conceptually equivalent to:

- `AllocateRuleID(preferredID int) int`
- `AllocateListenerID(preferredID int) int`
- `AllocateCertificateID(preferredID int) int`
- `AllocateRevisionForAgent(agentID string, maxExistingRevision int) int`
- `AllocateRevisionForTargets(agentIDs []string, maxExistingRevision int) int`

`preferredID` semantics:

- if `preferredID <= 0`, allocate the next available ID in that namespace
- if `preferredID > 0` and unused in that namespace, keep it
- if `preferredID > 0` and already used in that namespace, allocate the next available ID

This matches the desired import behavior of preserving non-conflicting IDs.

## Internal State

The allocator keeps:

- `usedRuleIDs`
- `usedListenerIDs`
- `usedCertificateIDs`
- `nextRevisionByAgent`

`nextRevisionByAgent` is initialized from the greater of:

- the agent sync floor from desired/current revision state
- the highest relevant existing revision already attached to that agent in current config

For multi-target certificate revisions, the allocator computes the maximum cursor across the target set, compares it with `maxExistingRevision + 1`, returns the larger value, then advances all target-agent cursors to at least the returned value plus one.

## Online Service Integration

### HTTP rules

`rules.go` stops calculating:

- `maxID`
- inline revision floor logic
- ad-hoc delete revision bumps

It still loads current HTTP/L4 state and validation dependencies, then asks the allocator for:

- next rule ID when creating
- next single-agent revision for create/update/delete

### L4 rules

`l4.go` uses the same `rule` namespace allocator as HTTP rules for ID assignment and uses single-agent revision allocation for create/update/delete.

### Relay listeners

`relay.go` uses the `listener` namespace allocator for IDs and single-agent revision allocation for listener mutations.

### Certificates

`certs.go` uses the `certificate` namespace allocator for IDs and:

- single-agent revision allocation when a mutation only targets one agent
- target-set revision allocation when a mutation impacts multiple target agents

Existing certificate business logic around targeting, issuance, relay CA behavior, and uploaded material remains in the certificate service.

## Backup Import Integration

`backup.go` removes the dedicated `importRevisionAllocator` and the duplicated per-resource `maxID`, `usedIDs`, and `maxRevisionByAgent` logic where that logic only exists to allocate identifiers.

Import behavior becomes:

- build one allocator from current store state before importing mutable resources
- for each imported object, ask the allocator for a namespace-specific ID
- pass the backup ID as `preferredID`
- if the preferred ID is available in that namespace, preserve it
- if not, reassign
- allocate revisions through the same shared revision methods used by online CRUD paths

Resource-specific conflict handling stays where it is today:

- duplicate frontend URL checks for HTTP rules
- duplicate listen endpoint checks for L4 rules
- duplicate relay listener name checks per agent
- duplicate certificate domain checks

The allocator does not decide whether an import should be skipped. It only decides numbering once the import logic has decided the record is valid and should be accepted.

## File Layout

Add a focused shared implementation, for example:

- `panel/backend-go/internal/controlplane/service/config_identity_allocator.go`
- `panel/backend-go/internal/controlplane/service/config_identity_allocator_test.go`

Update call sites:

- `panel/backend-go/internal/controlplane/service/rules.go`
- `panel/backend-go/internal/controlplane/service/l4.go`
- `panel/backend-go/internal/controlplane/service/relay.go`
- `panel/backend-go/internal/controlplane/service/certs.go`
- `panel/backend-go/internal/controlplane/service/backup.go`

## Testing Strategy

### Allocator unit tests

Add dedicated tests for:

- rule namespace shared by HTTP and L4
- listener namespace independence
- certificate namespace independence
- preserving preferred import IDs when unused
- reassigning preferred import IDs on collision
- single-agent revision floor behavior
- multi-target revision allocation behavior
- advancing per-agent cursors after each allocation

### Service regression tests

Keep and update existing tests so behavior remains covered without relying on internal implementation details.

Important regression cases:

- HTTP create/update/delete allocate revisions above agent sync floor
- L4 create/update/delete allocate revisions above agent sync floor
- relay listener mutations allocate revisions consistently
- certificate mutations preserve current targeting semantics while using shared allocation

### Import regression tests

Add or update tests for:

- preserved imported IDs when no conflict exists
- reassigned IDs when conflicts exist
- relay listener references still resolve after listener ID remap
- certificate references still resolve after certificate ID remap
- HTTP and L4 rule IDs remain in one shared rule namespace during import
- multi-target certificate revisions still advance all affected agents together

## Acceptance Criteria

- one shared allocator is used by online CRUD and backup import flows
- HTTP and L4 continue sharing one rule ID space
- relay listeners and certificates keep separate ID spaces
- non-conflicting imported IDs are preserved within their namespace
- conflicting imported IDs are reassigned within their namespace
- revision allocation for single-agent and multi-target mutations follows one shared implementation
- duplicated inline allocation logic is removed from the services listed in scope
- backend test suite remains green after the refactor
