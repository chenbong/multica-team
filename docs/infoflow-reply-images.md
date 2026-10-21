# Image replies in InfoFlow

Only a newly delivered agent reply's PNG, JPEG, WebP or GIF attachments are copied
to a private BOS bucket. Web attachments and canonical Markdown remain unchanged.
User image mirrors, arbitrary external image URLs and historical messages are not
uploaded. A bound conversation with synchronization disabled cannot authorize an
image download or upload.

## Delivery contract

- Preserve rendered image positions; append unreferenced attachments and display
  each attachment once. Code spans, fenced code and normal hyperlinks are opaque.
- At most five images per reply and 20 MiB per image. Unsupported, unavailable or
  excess images become a notice pointing to the existing full-conversation link.
- Try a transfer at most twice within a bounded reply preparation window. Send
  the available images and text on failure; do not send delayed image messages.
- Persist the prepared reply before sending, so delivery retries do not retry a
  failed transfer or change the original body. A later new reply may try again.
- One protected process-wide cache shares a BOS copy across robots, group/private
  destinations and restarts. Object names are deterministic and workspace-scoped.
- A single 8192-unit Markdown reply retains complete image URLs, native AT and the
  full-conversation link; excess prose is summarized rather than split into posts.
- BOS copies are retained independently of deleted platform content. There is no
  deletion hook or new lifecycle rule. Operators remain responsible for retention
  and cost. A non-expiring signature is a bearer capability, not a user login:
  anyone holding the link may read the image. Key revocation, object deletion and
  permission changes can still invalidate it.

## Protected configuration

Install the official `bcecmd` for the server architecture outside the repository.
Configure its AK/SK in a dedicated directory (0700) using `bcecmd -c DIRECTORY`;
its `credentials` file must be 0600. Do not put keys in robot JSON, Git, prompts,
frontend configuration or client daemon settings. Never enable CLI debug logs.

Create `reply-images.json` (0600) beside the server's `INFOFLOW_CONFIG_PATH` file,
or explicitly select its path with `INFOFLOW_REPLY_IMAGES_CONFIG_FILE`:

```json
{
  "enabled": true,
  "cli": "/protected/runtime/bcecmd",
  "confPath": "/protected/config/bos",
  "bucket": "your-image-bucket",
  "endpoint": "bj.bcebos.com",
  "caFile": "/protected/config/bos/ca-bundle.pem",
  "prefix": "infoflow-images/v1",
  "statePath": "/protected/state/reply-images.json",
  "since": "2026-01-01T00:00:00Z"
}
```

Set `since` once at activation; retain that value on normal restarts. Only replies
created at or after this cutoff are eligible. Existing in-flight transport
checkpoints are not reformatted. The feature is disabled without configuration.
The corresponding `INFOFLOW_REPLY_IMAGES_*` and `INFOFLOW_BOS_*` environment
settings override the protected file; `INFOFLOW_REPLY_IMAGES_ENABLED=false` is an
explicit disable. These settings are read on Bridge startup, not by the daemon.

`caFile` / `INFOFLOW_BOS_CA_FILE` is optional. If the CLI's system trust store is
outdated, select an operator-approved CA bundle (for example the CA set already
trusted by the running Node service). It applies only to the BOS child process.
TLS verification stays enabled; do not use insecure flags. Keep the CLI profile's
region/endpoint consistent with the configured bucket and validate real uploads.

Use a private bucket without an unexpected lifecycle deletion rule. Validate an
uploaded fixture and `bos gen_signed_url ... -e-1` before enabling the feature.
The application accepts HTTPS URLs scoped to the configured bucket/object and
checks that the signature has no scheduled expiry. Credentials and permanent
URLs are not logged by the image publisher or SDK logger. Cache/state backups
are private because their URLs are access capabilities.

## Authorization boundary

Task image metadata is additive to the internal delivery protocol. The Bridge
downloads bytes through `POST /api/integrations/infoflow/issue-deliveries/{id}/images/{attachmentId}`,
with the existing relay credential and scope. The server rechecks the active
binding generation, sync flag, pending delivery, workspace, issue, comment and
agent-uploaded attachment. It never grants arbitrary attachment access.

Chat images are read through the robot owner's existing authorized session and
the attachment ID, never a model-provided URL. The transfer verifies the response
size and file signature and never makes a user-provided URL into an outbound fetch.

## Operations

Back up the repository, protected configuration, database, Bridge state and Web
artifacts before activation. Update the API and Bridge together; include any
separately approved Web change in the compiled Web build. Rollback restores code
and configuration pointers, not historical task data. Do not restart or upgrade
client daemons. No database migration is required for image hosting.
