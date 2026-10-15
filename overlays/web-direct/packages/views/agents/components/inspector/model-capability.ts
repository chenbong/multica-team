import type { RuntimeModel } from "@multica/core/types";

// Claude Code appends a context-window modifier to some runtime-native model
// IDs (for example, claude-opus-5[1m]). Restrict inheritance to a numeric
// context size so arbitrary bracketed variants remain fail-closed.
const CLAUDE_CONTEXT_WINDOW_TAG = /\[[1-9]\d*[km]\]$/;

export function modelIdForCapabilityLookup(
  provider: string,
  model: string,
): string {
  return provider === "claude"
    ? model.replace(CLAUDE_CONTEXT_WINDOW_TAG, "")
    : model;
}

/**
 * Resolves the catalog entry used for capability display and cleanup. The raw
 * model remains the value persisted and sent to the runtime; only this lookup
 * identity is normalized.
 */
export function findModelCapabilityEntry(
  models: readonly RuntimeModel[],
  model: string,
  provider: string,
): RuntimeModel | undefined {
  if (!model) return undefined;
  const lookupId = modelIdForCapabilityLookup(provider, model);
  // Normalize the catalog side too. Claude discovery reports what the CLI
  // would really run, tag included (`claude-opus-5[1m]`), so comparing a
  // stripped query against raw catalog ids would miss the entry for the very
  // model the user just picked — and hiding a model's own effort picker is how
  // that failure would show up (MUL-6961). Mirrors the daemon's lookup in
  // ValidateThinkingLevelWith.
  return models.find(
    (entry) => modelIdForCapabilityLookup(provider, entry.id) === lookupId,
  );
}

/** Unknown explicit OneAPI IDs may use the gateway's common effort controls.
 *  This is not a claim about model compatibility. Exact catalog entries always
 *  win; no default-model, service-tier or unrelated-provider capability leaks. */
export function findThinkingCapabilityEntry(
  models: readonly RuntimeModel[], model: string, provider: string,
): RuntimeModel | undefined {
  const exact = findModelCapabilityEntry(models, model, provider);
  if (exact || !model || (provider !== "claude" && provider !== "codex")) return exact;
  const gateway = models.find(entry => entry.provider === "OneAPI" &&
    (entry.thinking?.supported_levels.length ?? 0) > 0);
  if (!gateway?.thinking) return undefined;
  return {
    id: model, label: model, provider: "OneAPI",
    thinking: { supported_levels: gateway.thinking.supported_levels.map(level => ({ ...level })) },
  };
}
