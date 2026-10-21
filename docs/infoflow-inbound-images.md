# Incoming task images

Each InfoFlow message is one input. Text and all images in the message enter
the same comment and agent input. There is no debounce or cross-message batch.
Image-only input invokes the assigned agent using existing task context.

Private messages require no AT. Group input must contain a robot AT, or quote
a verified robot delivery/bound notice in that same group and workspace.
Unknown quotes and ordinary ALL_MESSAGE_FORWARD traffic never enter Chat or
tasks. An unmentioned quote delivered only through ALL_MESSAGE_FORWARD is a
candidate for server verification, not permission to execute it. This exception
is disabled when the task relay is disabled, preventing fallback into Chat.
Quoted previews are not interpreted as commands or task selectors.

Incoming images may be inline base64, download descriptors, or image blocks
inside private richtext. Private callbacks may split a single original message
into text and image fragments sharing one MsgId. A bounded per-message lookup
reads the complete original message once and shares it across those callbacks,
so they produce one complete input without a cross-message waiting window.
Lookup failures never submit the incomplete text fragment. Official API-hosted
image URLs use the robot's application token for download.
Directed group image messages also resolve their original message through the
same scoped API, replacing legacy short-lived image proxy links with supported
authenticated download URLs. Ordinary group traffic is never queried or stored.
At most five images and 20 MiB total are accepted.
Supported raster types are PNG, JPEG, GIF, WebP and BMP. Byte signatures, not
the supplied extension or Content-Type, determine image type. Streaming reads
enforce the size limit even when Content-Length is missing.

Temporary downloads retry twice. HTTP 429 and the image API's HTTP-200 JSON
rate-limit code 1300019 use bounded exponential backoff before retrying.
Images are staged and submitted as a complete
batch only: a partial download never produces a comment or starts an agent.
Temporary server uploads use the same frozen request/message ID for at most
three submission attempts. After an unknown response, read-only admission
receipts distinguish committed input from a confirmed failure; an unavailable
receipt remains pending without repeating the mutation or claiming success.
SQL rollback removes incomplete attachment records and object URLs are cleaned
using the existing upload rollback path.

Unsupported files produce a task-page upload notice after route/permission
checks, without submitting their associated text. Received reactions mean only
receipt of the original input; success creates no additional notice. Failure
notices are checkpointed so a delayed replay cannot execute the failed input.

Other bound conversations receive the author, text, image count and task link.
Original images and attachment download URLs are not retransmitted. Counts are
part of immutable delivery content snapshots, so retries preserve the original
summary even after an attachment is edited or removed.

Verification covers actual private and group images, directed quote routing,
multi-image input, complete-batch failure, bounded retry and deduplication.
No client daemon change or restart is required.
