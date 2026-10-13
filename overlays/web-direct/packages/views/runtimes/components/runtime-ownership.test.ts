// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime, RuntimeProfile } from "@multica/core/types";
import { runtimeOwnershipView } from "./runtime-ownership";
import { buildRuntimeMachines } from "./runtime-machines";

const runtime = (id: string, owner: string | null, daemon = id): AgentRuntime => ({
  id, workspace_id: "ws", daemon_id: daemon, name: `Claude (${daemon})`, provider: "claude",
  runtime_mode: "local", status: "online", owner_id: owner, visibility: "private", device_info: "", launch_header: "",
  metadata: {}, last_seen_at: new Date().toISOString(), created_at: "", updated_at: "",
});
const profile = (id: string, owner: string | null) => ({id, created_by: owner}) as RuntimeProfile;

describe("runtime ownership view", () => {
  const runtimes = [runtime("own", "alice", "shared-host"), runtime("other", "bob", "shared-host"), runtime("unowned", null)];
  const profiles = [profile("own-profile", "alice"), profile("other-profile", "bob"), profile("unknown-profile", null)];
  it("keeps only owned rows and profiles in the default view", () => {
    const view = runtimeOwnershipView(runtimes, profiles, "alice", false);
    expect(view.runtimes.map(r => r.id)).toEqual(["own"]);
    expect(view.profiles.map(p => p.id)).toEqual(["own-profile"]);
  });
  it("filters before grouping mixed-owner machine rows", () => {
    const view = runtimeOwnershipView(runtimes, profiles, "alice", false);
    const machines = buildRuntimeMachines(view.runtimes, {now: Date.now(), currentUserId:"alice"});
    expect(machines).toHaveLength(1);
    expect(machines[0]?.runtimes.map(r => r.id)).toEqual(["own"]);
  });
  it("all means exactly the server-authorized input, without widening access", () => {
    expect(runtimeOwnershipView(runtimes, profiles, "alice", true).runtimes).toBe(runtimes);
    const authorized = [runtimes[0]!];
    expect(runtimeOwnershipView(authorized, [], "alice", true).runtimes).toBe(authorized);
    expect(runtimes).toHaveLength(3);
  });
  it.each([null, undefined, ""])("does not expose rows without a viewer identity (%s)", (id) => {
    expect(runtimeOwnershipView(runtimes, profiles, id, true)).toEqual({runtimes:[],profiles:[]});
  });
});
