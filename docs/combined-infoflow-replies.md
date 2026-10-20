# Combined Markdown replies

New group replies use one extended Markdown message with a native AT block.
The readable mention row appears below the body and above the full-conversation
link. Body mention prose remains literal. InfoFlow replaces matching AT tokens
before parsing code, so selected accounts in code receive an invisible separator
for display; use the canonical web task when copying exact source code. URI @
characters are percent encoded to preserve their destination without AT markup.
Private replies remain Markdown without AT. All new summaries have an 8192
UTF-16-unit budget, including headers, truncation hint, mentions and footer.
Truncation respects code-point boundaries and closes code fences.

Recipients are the requesters and explicitly selected members, not names inferred
from generated prose. Duplicate accounts are removed. At most 50 are notified,
with requesters first; the message states the number omitted. No @all is used.
The API stores instruction order but retains its historical sorted request hash
for input retry compatibility. A coalesced task prioritizes its requesters.

Before sending, the Bridge persists the rendered Markdown, chosen recipients,
client message ID and client time. A failed send retries this same message,
without a TEXT fallback. This reduces duplicate risk but does not promise
exactly-once delivery across external transport failures. Only an explicit
human-membership rejection allows a fresh roster lookup, removal of departed
recipients and a new pinned payload. Generic errors/timeouts never alter targets.

Accepted legacy bodies never receive their pending standalone AT reminder and
are not resent. Historical multipart checkpoints retain their original digest
and resume only unsent body chunks; their standalone reminders are retired too.
New responses never create multipart checkpoints.

Deployment updates the API and Bridge together, without database migrations,
Web changes or client daemon restart. Back up binaries, code, configuration,
database and Bridge state before cutover. Rollback restores code/binary pointers,
not old delivery-state snapshots: restoring stale state can duplicate messages.
Older code may pause on combined-v2 checkpoints, so inspect those before retrying
an old-version rollback. No live configuration or credentials belong in Git.
