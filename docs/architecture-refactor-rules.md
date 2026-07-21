# Architecture refactor rules

Use these rules when reviewing or implementing architecture work in harbrr. They
turn `docs/autobrr-app-template.md` into a practical review checklist.

## Rule 1: tie every structural PR to a target

Every structural PR must name the issue it implements and the template rule it
moves toward. If the target rule is wrong or missing, update the docs or add an
ADR before reshaping code.

Structural changes include:

- moving composition or startup/shutdown wiring;
- changing package boundaries;
- changing storage seams;
- changing public service interfaces;
- adding a shared base or lifecycle abstraction;
- changing generated-contract workflows;
- moving HTTP routes or API contracts.

## Rule 2: make invariants structural

Prefer code and tests that make the invariant hard to violate.

Examples:

- pass dependencies through `internal/app` instead of re-wiring them in commands;
- depend on `dbinterface.Querier` instead of concrete `*database.DB` when that is
  all a service needs;
- pass per-call state as parameters instead of mutating shared dependency fields;
- generate API types from OpenAPI instead of hand-mirroring them.

Comments may explain why an invariant exists, but comments are not the guard.

## Rule 3: no phantom seams

Do not keep empty packages, placeholder interfaces, or doc-only seams for future
behavior. If removing a package changes nothing, it is not an architectural
boundary yet.

Future behavior belongs in a GitHub issue. Recreate the package when the behavior
exists.

## Rule 4: keep abstractions earned

Add a shared base or lifecycle helper only when there are multiple real adopters
with the same shape and a clear invariant to centralize.

An acceptable extraction has:

- at least two existing structural twins, preferably three;
- a small public surface;
- no reflection or tag-driven behavior;
- no callback into an embedding type;
- tests for the invariant once in the extracted module;
- service-level tests reduced to mapping and behavior specific to that service.

If the second hook or option would turn the helper into a framework, stop and
redesign instead of growing it.

## Rule 5: preserve product correctness gates

harbrr's parity gate is the product correctness gate. Refactors must not weaken
it or hide differences by editing vendored definitions.

For engine work:

- keep parity fixtures green;
- keep stage imports one-way;
- add direct tests at the moved seam;
- preserve Jackett compatibility quirks unless the issue explicitly changes
  their disposition.

For HTTP/API work:

- update OpenAPI with route changes;
- run the drift tests;
- keep management API and Torznab/Newznab contracts separate.

For frontend work:

- keep generated route and API artifacts current;
- keep API calls behind the client wrapper;
- keep query keys in the registry.

## Rule 6: keep scope narrow

A refactor PR should have one reason to exist. Do not bundle cosmetic renames,
drive-by cleanup, or adjacent feature work unless the issue calls for it.

Each issue should state:

- problem;
- agreed design;
- explicit out of scope;
- sequencing constraints;
- acceptance tests;
- required commands.

If review discovers new work, file or update an issue. Do not quietly expand the
PR.

## Rule 7: docs and code move together

Update docs when a PR changes the architecture target, shipped/planned status,
security model, public API, or package ownership.

Common doc updates:

- `docs/architecture.md` for product boundaries and invariants;
- `CONTEXT.md` for new project vocabulary;
- an ADR for a load-bearing decision;
- `docs/security.md` for credential or redaction behavior;
- user docs when shipped behavior changes;
- `AGENTS.md` when contributor or agent rules change.

Docs must not advertise planned work as shipped.

## Rule 8: legacy-data removal ships a refusing guard

Removing a fold window's legacy storage (columns, tables, boot backfills) requires
that the drop migration **refuses to apply** (whole-transaction rollback) while any
row the fold never processed still exists — never assume every deployment folded.

The refusal message is part of the design, not an afterthought: it must name the
release to boot first so an operator who version-skips lands on instructions, never
on data loss. Precedents: migrations `0021` (#269) and `0022` (#294).

## Review checklist

Use this checklist before approving a structural PR:

- Does the PR cite the GitHub issue and the template/refactor rule it implements?
- Is the change smaller than the issue, not larger?
- Are startup, shutdown, storage, and secret-handling invariants preserved?
- Are there tests for the moved boundary, not only for the old behavior?
- Did generated artifacts and drift gates run when contracts changed?
- Did docs update when package ownership or shipped behavior changed?
- Are there no new phantom packages, placeholder interfaces, or generic helpers
  without real adopters?
- Did required checks run, and are skipped checks explained?

## Useful guardrails to automate

Prefer tests or scripts for rules that are easy to regress:

- import-arrow tests for package layering;
- route/spec/frontend generated-client drift checks;
- grep checks for deleted packages still named in docs;
- secret-redaction tests for every new credential surface;
- database interface compile-time assertions for services expected to use
  `dbinterface.Querier`;
- generated route/API artifact checks in CI.
