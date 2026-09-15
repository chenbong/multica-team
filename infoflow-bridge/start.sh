#!/bin/bash
# Starts the bridge detached, logging to logs/bridge.log.
set -euo pipefail
cd "$(dirname "$0")"
exec python3 scripts/bridgectl.py start
