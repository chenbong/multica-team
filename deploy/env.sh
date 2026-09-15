#!/bin/bash
# Environment for one isolated Multica overlay instance. Everything that names
# a host, port, database or profile comes from the machine-local host.env, so
# the same scripts serve several instances and a public clone without edits.
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTANCE_DIR="$(dirname "$DEPLOY_DIR")"

if [ -f "$DEPLOY_DIR/host.env" ]; then
  . "$DEPLOY_DIR/host.env"
fi

HOST_IP="${MULTICA_HOST_IP:-127.0.0.1}"
API_PORT="${MULTICA_API_PORT:-8006}"
WEB_PORT="${MULTICA_WEB_PORT:-8004}"
BRIDGE_PORT="${MULTICA_BRIDGE_PORT:-8002}"
PROFILE_NAME="${MULTICA_PROFILE_NAME:-multica-local}"
DATABASE_NAME="${MULTICA_DATABASE_NAME:-multica}"
NODE_BIN_DIR="${MULTICA_NODE_BIN_DIR:-/opt/node-v24.18.0/bin}"

export PATH="$NODE_BIN_DIR:/usr/local/go/bin:$PATH"
export DATABASE_URL="postgres://multica:multica@127.0.0.1:5432/$DATABASE_NAME?sslmode=disable"
export PORT=$API_PORT
export MULTICA_BIND_HOST=0.0.0.0
export APP_ENV=development
export FRONTEND_ORIGIN="http://$HOST_IP:$WEB_PORT"
export MULTICA_APP_URL="http://$HOST_IP:$WEB_PORT"
export MULTICA_PUBLIC_URL="http://$HOST_IP:$API_PORT"
export MULTICA_DAEMON_SERVER_URL="http://$HOST_IP:$API_PORT"
export REMOTE_API_URL="http://127.0.0.1:$API_PORT"
export CORS_ALLOWED_ORIGINS="http://$HOST_IP:$WEB_PORT,http://127.0.0.1:$WEB_PORT,http://localhost:$WEB_PORT"
# Who may sign in: set the real domain(s) in the machine-local deploy/host.env.
# Empty keeps the API's own default instead of guessing a domain here.
export ALLOWED_EMAIL_DOMAINS="${MULTICA_ALLOWED_EMAIL_DOMAINS:-}"
export ALLOW_SIGNUP=false
export NEXT_TELEMETRY_DISABLED=1

# Optional branding for the patched web login page (overlays/web-patch.py).
# Every value defaults to empty, which leaves the upstream Multica copy in
# place, so a public clone builds a stock login page.
export MULTICA_LOGIN_TITLE="${MULTICA_LOGIN_TITLE:-}"
export MULTICA_LOGIN_DESCRIPTION="${MULTICA_LOGIN_DESCRIPTION:-}"
export MULTICA_EMAIL_PLACEHOLDER="${MULTICA_EMAIL_PLACEHOLDER:-}"
export MULTICA_VERIFICATION_BOT_NAME="${MULTICA_VERIFICATION_BOT_NAME:-}"
export MULTICA_VERIFICATION_BOT_URL="${MULTICA_VERIFICATION_BOT_URL:-}"
# Where the "add a computer" dialog points for the CLI installer. Empty keeps
# upstream's installer, so a public clone is not tied to this repository.
export MULTICA_CLI_INSTALL_URL="${MULTICA_CLI_INSTALL_URL:-}"
# InfoFlow (如流) binding page linked from the agents page header. Empty hides
# the button, so a public clone builds the upstream page unchanged.
export MULTICA_INFOFLOW_BRIDGE_URL="${MULTICA_INFOFLOW_BRIDGE_URL:-}"
export MULTICA_INFOFLOW_BRIDGE_LABEL="${MULTICA_INFOFLOW_BRIDGE_LABEL:-}"
# Runtime ids the "add a computer" command pins, comma separated. Empty hands
# out the upstream command, which registers every agent CLI found on the
# machine; a deployment that only wants its own runtimes sets e.g. `ducc,ducx`.
export MULTICA_DAEMON_RUNTIME_DEFAULTS="${MULTICA_DAEMON_RUNTIME_DEFAULTS:-}"
# Commands the handed-out daemon command declares as needing a generated
# wrapper (runtime_shims). Empty hands out the upstream command.
export MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS="${MULTICA_DAEMON_RUNTIME_SHIM_DEFAULTS:-}"

# IM open-platform endpoints used by infoflow-bridge. All optional: empty keeps
# the SDK's own defaults, and the deployment sets its real values in host.env.
export INFOFLOW_BASE_URL="${INFOFLOW_BASE_URL:-}"
# Leave the WS override variables absent unless host.env explicitly sets them.
# Empty environment values prevent the SDK from using its built-in gateway.
if test -n "$(printenv INFOFLOW_WS_GATEWAY 2>/dev/null)"; then
  export INFOFLOW_WS_GATEWAY
else
  unset INFOFLOW_WS_GATEWAY
fi
if test -n "$(printenv INFOFLOW_WS_CONNECT_DOMAIN 2>/dev/null)"; then
  export INFOFLOW_WS_CONNECT_DOMAIN
else
  unset INFOFLOW_WS_CONNECT_DOMAIN
fi

export WEB_TREE="$INSTANCE_DIR/.web-build/multica"

# These values are private deployment state, never committed to this repo.
SECRETS="$DEPLOY_DIR/secrets.env"
if [ ! -f "$SECRETS" ]; then
  umask 077
  {
    echo "export JWT_SECRET=$(openssl rand -base64 48 | tr -d '\n')"
    echo "export MULTICA_PLUGIN_SECRET_KEY=$(openssl rand -base64 32 | tr -d '\n')"
  } > "$SECRETS"
fi
. "$SECRETS"
