# Local Web verification

Use **build, then start** for user-facing previews and acceptance tests. Do not
use `next dev`: route-on-demand compilation makes the first navigation slow and
is not the deployed execution mode. After source changes, prepare the generated
tree again and rebuild before testing. Production-mode preview has no hot reload.

Prepare an isolated frontend under `build/` with the checked overlays and its own
installed dependencies. Keep the pinned upstream checkout pristine. Use an
isolated local API and test-only data, not a production account or agent.

```sh
bash scripts/preview-built-web.sh build/<stage> http://127.0.0.1:<api-port> 18094
```

The helper builds first, starts only after a successful build, binds to loopback,
and forwards API requests on the same origin. `TEAMMESH_NODE_BIN` can select the
managed Node executable. Existing build options such as a local test font fixture
may be supplied in the environment; do not ship test fonts to production.

The helper never stops another process, changes API settings, or restarts client
daemons. Stop the previous preview that you own before reusing its port.
