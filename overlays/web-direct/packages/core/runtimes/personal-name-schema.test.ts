import { expect, it } from "vitest";
import type { AgentRuntime } from "../types/agent";
import { withPersonalName } from "./personal-name-schema";

it.each([undefined, null, 123, {}, "My machine"])("handles personal_name %j without changing existing fields", (personal_name) => {
  const result = withPersonalName({ id: "runtime", name: "host", custom_name: "legacy", personal_name } as AgentRuntime);
  expect(result.personal_name).toBe(typeof personal_name === "string" ? personal_name : null);
  expect(result.name).toBe("host");
  expect(result.custom_name).toBe("legacy");
});
