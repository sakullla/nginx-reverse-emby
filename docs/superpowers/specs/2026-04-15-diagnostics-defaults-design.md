# Diagnostics Defaults Design

## Scope

This change updates the go-agent diagnostics defaults in two places:

1. HTTP diagnostics will issue `GET` requests by default instead of `HEAD`.
2. HTTP rule diagnostics and L4 TCP rule diagnostics will default to `5` samples.

The change is intentionally limited to diagnostic behavior. It does not alter proxy runtime behavior, rule storage, task payload shape, or panel API contracts.

## Current Behavior

HTTP diagnostics currently start with `HEAD` and only retry with `GET` when the backend responds with `405 Method Not Allowed` or `501 Not Implemented`. Sampling defaults are already configured as `5` in the agent app wiring, but the implementation and tests must be checked to ensure the default is consistent everywhere that constructs or relies on the probers.

## Design

### HTTP diagnostics request method

The HTTP prober will send `GET` on the first and only diagnostic request for each sample attempt.

The fallback path from `HEAD` to `GET` will be removed because:

- it adds method-dependent behavior that is no longer desired,
- it can under-report issues for endpoints that behave differently on `HEAD`,
- and the requested product behavior is an explicit `GET` default.

The prober will continue to reuse the existing timeout handling, custom headers, user agent handling, relay dialing, response draining, and success / failure accounting.

### Diagnostic sample defaults

The effective default sample count for both HTTP and L4 TCP diagnostics will be `5`.

Implementation should preserve the existing `Attempts <= 0 => 5` defaulting inside the diagnostic probers and keep the app-level constructor wiring aligned with that same value so behavior is consistent whether probers are created directly in tests or through the application bootstrap path.

## Testing

Tests will be updated using TDD:

1. Add or adjust HTTP diagnostic tests to assert that the prober uses `GET` by default and no longer depends on `HEAD` fallback behavior.
2. Add or adjust tests covering the default attempts behavior for HTTP and TCP probers where needed.
3. Run the focused go-agent diagnostics and task tests first, then the full `go-agent` test suite after the implementation is green.

## Risks

The main behavior change is that diagnostics now perform a full `GET`, which can trigger heavier backend work than `HEAD`. This is acceptable because the request is explicit, bounded by the existing timeout, and only used in operator-triggered diagnostics.
