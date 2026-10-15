import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { startBridge } from "./src/startup.js";

test("admin and shutdown are available before slow robot connections complete", async () => {
  const signals = new EventEmitter(), order = [];
  let finish;
  const runtime = { sync: () => { order.push("sync"); return new Promise(resolve => { finish = resolve; }); }, stopAll: async () => { order.push("stop"); } };
  const starting = startBridge(runtime, () => { order.push("admin"); }, { signals, exit: () => order.push("exit"), logger: { log() {} } });
  assert.deepEqual(order, ["admin", "sync"]);
  signals.emit("SIGTERM"); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(order, ["admin", "sync", "stop", "exit"]);
  finish(); await starting;
});
