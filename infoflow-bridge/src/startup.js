// Administration must be reachable while slow gateways connect. Shutdown
// handlers also precede network initialization so startup can be interrupted.
export async function startBridge(runtime, startAdminServer, { signals = process, exit = code => process.exit(code), logger = console } = {}) {
  for (const signal of ["SIGINT", "SIGTERM"]) {
    signals.on(signal, async () => {
      logger.log(`${signal} received, shutting down`);
      try { await runtime.stopAll(); }
      finally { exit(0); }
    });
  }
  startAdminServer({ runtime });
  await runtime.sync();
}
