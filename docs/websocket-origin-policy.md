# Browser WebSocket origin policy

By default, retain upstream same-origin, explicitly allowed origins and trusted
proxy behavior. A self-hosted deployment can explicitly set the API environment
variable `MULTICA_WS_ALLOW_ALL_ORIGINS=true` to accept every WebSocket Origin.
This is separate from HTTP CORS and does not modify `CORS_ALLOWED_ORIGINS`.
Putting `*` in that existing list is not equivalent: the WS policy compares its
entries as exact origins.

Unrestricted origins remove cross-site WebSocket hijacking defense. Another
website can attempt connections; a browser might attach a valid authentication
cookie when its cookie policy allows it. Enable this only after accepting that
tradeoff. Do not use this setting to expose data without authentication.

The narrow overlay changes only the origin gate. Cookie/first-frame token
validation, disabled-account checks, workspace membership, resource subscriptions
and frame limits are unchanged. Tests cover the opt-in, rejected invalid tokens
and cookies, foreign workspace/resource denial and authenticated event delivery.

Set the variable in the API container environment. Editing a Docker env file
does not change an existing container: recreate only the API container while
preserving its effective environment and startup configuration. Back up the
binary, source, runtime environment and persistent state first, and retain the
stopped old container for rollback. No Web build, Bridge restart or client
daemon restart is needed.
