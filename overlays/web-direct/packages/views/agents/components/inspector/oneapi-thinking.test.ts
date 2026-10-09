// @vitest-environment node
import { describe, expect, it } from "vitest";
import { buildModelChangeUpdate } from "./model-change-cleanup";
import { findModelCapabilityEntry } from "./model-capability";

describe("OneAPI thinking capability contract", () => {
  it.each([
    ["codex", "gpt-5.6-luna", "gpt-5.6-luna"],
    ["claude", "Opus 5.5", "Opus 5.5"],
    ["claude", "Opus 5.5[1m]", "Opus 5.5"],
  ])("retains Max after changing a %s model", (provider, selected, catalogId) => {
    const catalog = [{
      id: catalogId, label: catalogId,
      thinking: { supported_levels: [
        { value: "low", label: "Low" }, { value: "medium", label: "Medium" },
        { value: "high", label: "High" }, { value: "xhigh", label: "Extra high" },
        { value: "max", label: "Max" },
      ] },
    }];
    expect(findModelCapabilityEntry(catalog, selected, provider)?.thinking?.supported_levels).toHaveLength(5);
    expect(buildModelChangeUpdate({provider, model:selected, thinkingLevel:"max", serviceTier:"", catalog}))
      .toEqual({model:selected});
  });
});
