# HTTP CORS and attachment framing

These two API-only deployment opt-ins are independent from browser WebSocket
origin policy, account email rules and outbound resource-domain controls.

- `MULTICA_CORS_ALLOW_ALL_ORIGINS=true`: authorize every HTTP Origin. Reflect
  the concrete request Origin, retain `Vary: Origin` and credential support;
  do not return the literal `*`, which browsers reject for credentialed CORS.
  Allowed methods, request/exposed headers and authentication remain unchanged.
- `MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS=true`: set attachment preview
  CSP `frame-ancestors *`, including local upload previews and preview middleware.
  Leave ordinary API framing restrictions, script/form/object directives, cookie
  SameSite policy and attachment authorization unchanged.
  Avatar and plugin-surface framing retain their previous policies.

Both default to false. Permitting arbitrary sites to call authenticated APIs or
frame private documents broadens cross-site exposure. Existing authentication is
not a replacement for origin isolation. A deployment must accept this tradeoff
explicitly; external websites' own CSP/cookie rules still apply independently.

Back up the API binary, source, runtime environment and database, then recreate
only the API container to apply its new environment. Keep the prior container
and configuration recoverable. Web, Bridge, database and client daemons do not
need restarting. Do not silently remove unrelated import/plugin/image policies.
