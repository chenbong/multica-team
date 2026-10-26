# Platform expansion acceptance

## Agreed scope

1. Separate full-email accounts. With the company root configured as allowed,
   permit the root and exactly one child label. Map `user@child.root` to
   `user_child` for InfoFlow and invert it for machine provisioning. Root
   names follow this inference only when no existing verified identity matches.
   Preserve existing root accounts containing underscores (including contractor
   accounts). With no existing match, reserve valid `v_` names (including
   additional underscores) for the root domain. Reject a username matching
   both root and child identities.
2. Host-scheduled complete private BOS backup, 03:00 Asia/Shanghai, 30 days.
   No additional archive encryption. Include database/globals, uploads, protected
   configuration and encryption keys, source/deployment/version metadata.
   Verify upload before pruning only owned backup objects. Retry twice at ten
   minute intervals; notify the operator privately only after terminal failure.
3. Owner-only secret management in profile. AES-GCM at rest. A live task token
   can list/read only its agent owner's secrets, including shared-agent runs
   and chat. No secret values in logs or metadata lists. No read audit in v1.
   Latest values only. Temporary files are restricted and scoped to a command.
4. Current-issue panel CRUD via live task token. No cross-issue or workspace
   panel mutations. Preserve URL validation and real-time invalidation.
5. Cross-workspace visible non-built-in agent copying. New owner/avatar/runtime;
   preserve non-secret configuration/model/thinking/access and usable skill
   associations. No environment values or integration bindings copied.
6. Bulk same-target runtime change with preview. Skip unauthorized agents.
   Preserve supported overrides, reset unsupported overrides, leave live runs
   untouched. Revalidate authorization/state at apply time.

## Release gates

- Focused unit/integration tests and adversarial owner/run-scope tests.
- Configured production Web build, typecheck and login branding release gate.
- Preserve current API/Bridge/Web settings and CLI binary; never restart clients.
- Backup before schema/code/config cutover. Retain recoverable old code/containers.
- If schema migrations run as an administrator, grant only secret-table DML to
  the existing API database role using deploy/grant-personal-secrets.sql.
  Verify authenticated secret list/create/read/update/delete as that role;
  anonymous route checks do not prove table privileges.
- Production tests use isolated own fixtures, no real employee OTP sends.
- Verify backup round-trip and restore to an isolated database, not production.
- Check all six features and remove only exact synthetic test records.
- No actual secrets, tokens, request bodies or protected manifests in Git.
