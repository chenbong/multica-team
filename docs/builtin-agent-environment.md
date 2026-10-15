# Built-in agent environment defaults

This container deployment already initializes user-created agents with
`IS_SANDBOX=1`. Mika and the hidden AI agent-builder execution carriers now use
the same default through their own creation queries. `IS_SANDBOX` is an explicit
Claude/ducc opt-in marker; it does not create isolation. Do not enable this
deployment policy on unisolated root hosts.

- New Mika agents and builder carriers persist their own default environment.
- Mika's existing-agent path returns the existing configuration unchanged.
- Migration `522_builtin_agent_sandbox_env.up.sql` fills only absent keys on
  `kind=user, system_key=mika` and `kind=system, system_key=agent_builder:*`.
- Other environment keys and explicit values, including `0`, empty strings,
  `false`, or JSON null, are not overwritten. Malformed non-object maps are
  left for operator review rather than silently replaced.
- The data-only rollback deliberately retains the filled defaults. Removing a
  value later edited by an owner would not be safe.
- The daemon receives the freshly stored agent environment on its next claim.
  No global daemon environment, binary update, or client restart is needed.
- Hidden builders remain hidden. This does not relax agent permissions or add
  an environment-management endpoint for system execution carriers.

`overlays/builtin-agent-env-sql.py` validates both upstream SQL and SQLc anchors
and composes with the existing task-transfer guards. The upstream submodule
stays unmodified.

Verification:

```sh
python3 scripts/builtin-agent-env-sql-test.py
python3 overlays/prepare.py
python3 overlays/prepare.py --check
```

Against an explicitly configured isolated test database, run the handler tests
with the generated Go overlay and `-run '^TestBuiltinSandbox'`. The tests cover
creation, idempotent Mika re-entry, scoped backfill, explicit-value retention,
unrelated agent exclusion, migration replay, and non-destructive rollback.
