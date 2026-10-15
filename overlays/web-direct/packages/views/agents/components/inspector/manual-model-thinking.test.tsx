// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { RuntimeModel } from "@multica/core/types";
import { findModelCapabilityEntry, findThinkingCapabilityEntry } from "./model-capability";
import { buildModelChangeUpdate } from "./model-change-cleanup";

const state = vi.hoisted(() => ({ models: [] as RuntimeModel[] }));
vi.mock("@tanstack/react-query", () => ({ useQuery: () => ({ data: { models: state.models } }) }));
vi.mock("@multica/core/runtimes", () => ({ runtimeModelsOptions: () => ({}) }));
vi.mock("../../../i18n", () => ({ useT: () => ({ t: (fn: any) => fn({ inspector: { prop_thinking: "Thinking" } }) }) }));
vi.mock("../../../common/prop-row", () => ({ PropRow: ({ label, children }: any) => <div>{label}{children}</div> }));
vi.mock("../../../settings/components/settings-layout", () => ({ SettingsRow: ({ label, children }: any) => <div>{label}{children}</div> }));
vi.mock("./thinking-picker", () => ({ ThinkingPicker: ({ value, levels, canEdit, onChange }: any) => (
  <select aria-label="Thinking level" disabled={!canEdit} value={value} onChange={event => onChange(event.target.value)}>
    <option value="">Follow runtime</option>
    {levels.map((level: any) => <option key={level.value} value={level.value}>{level.label}</option>)}
  </select>
) }));
import { ThinkingPropRow, ThinkingSettingField } from "./thinking-prop-row";

const gateway: RuntimeModel = { id: "listed-model", label: "Listed", provider: "OneAPI", thinking: {
  supported_levels: ["low", "medium", "high", "xhigh", "max"].map(value => ({ value, label: value })),
} };
afterEach(() => { cleanup(); state.models = []; });

describe("manual OneAPI model thinking", () => {
  it.each([ThinkingPropRow, ThinkingSettingField])("shows selectable effort for a manually entered model", Component => {
    state.models = [gateway];
    const onChange = vi.fn();
    render(<Component label="Thinking" runtimeId="runtime" runtimeOnline provider="claude" model="DD" value="" canEdit onChange={onChange} />);
    expect(screen.getAllByRole("option")).toHaveLength(6);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "high" } });
    expect(onChange).toHaveBeenCalledWith("high");
  });

  it("keeps strict model lookup and existing model-change settings unchanged", () => {
    expect(findModelCapabilityEntry([gateway], "DD", "claude")).toBeUndefined();
    expect(buildModelChangeUpdate({ provider: "claude", model: "DD", thinkingLevel: "high", serviceTier: "fast", catalog: [gateway] })).toEqual({ model: "DD" });
    const entry = findThinkingCapabilityEntry([gateway], "DD", "claude")!;
    expect(entry.id).toBe("DD");
    expect(entry.service_tiers).toBeUndefined();
    entry.thinking!.supported_levels[0]!.value = "mutated";
    expect(gateway.thinking!.supported_levels[0]!.value).toBe("low");
  });

  it("does not invent capabilities for known unsupported models, defaults or other providers", () => {
    const unsupported = { id: "known", label: "Known", provider: "OneAPI" };
    expect(findThinkingCapabilityEntry([gateway, unsupported], "known", "claude")).toBe(unsupported);
    for (const provider of ["opencode", "hermes", "unknown"]) expect(findThinkingCapabilityEntry([gateway], "DD", provider)).toBeUndefined();
    expect(findThinkingCapabilityEntry([gateway], "", "codex")).toBeUndefined();
    expect(findThinkingCapabilityEntry([{ ...gateway, provider: "native" }], "DD", "claude")).toBeUndefined();
    expect(findThinkingCapabilityEntry([], "DD", "claude")).toBeUndefined();
  });
});
