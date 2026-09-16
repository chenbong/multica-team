#!/bin/bash
# Starts the bridge under a small supervisor so an unexpected Node exit does
# not leave the admin page offline. The deployment env is optional for a
# standalone clone, but is loaded when this instance has deploy/env.sh.
set -euo pipefail
cd "$(dirname "$0")"
if [ -f "../deploy/env.sh" ]; then
  . ../deploy/env.sh
fi
exec python3 scripts/bridgectl.py start
