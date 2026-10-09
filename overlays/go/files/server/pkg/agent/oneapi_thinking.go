package agent

import (
	"path/filepath"
	"slices"
)

// OneAPIThinking supplies the gateway's effort picker without pretending that
// its name-only catalog is the local CLI's complete per-model capability list.
// Reuse the native Claude/Codex labels. Do not advertise Ultra universally:
// the gateway does not guarantee that native Codex-only mode for every model.
func OneAPIThinking(provider string) *ModelThinking {
	var labels map[string]string
	switch provider {
	case "claude":
		labels = claudeEffortLabel
	case "codex":
		labels = codexEffortLabel
	default:
		return nil
	}
	thinking := &ModelThinking{}
	for _, value := range []string{"low", "medium", "high", "xhigh", "max"} {
		thinking.SupportedLevels = append(thinking.SupportedLevels, ThinkingLevel{Value: value, Label: labels[value]})
	}
	return thinking
}

// ValidateRuntimeThinkingLevelWith preserves explicit gateway effort for the
// ducc/ducx wrappers, including models absent from a local default-only list.
// The actual CLI flag vocabulary remains authoritative. Ordinary Claude and
// Codex, unknown wrapper names and the follow-local-model case are unchanged.
func ValidateRuntimeThinkingLevelWith(loadCatalog func() (Catalog, error), provider string, cmd Command, model, value string) (bool, error) {
	command := filepath.Base(cmd.Path)
	baidu := provider == "claude" && command == "ducc" || provider == "codex" && command == "ducx"
	if !baidu || model == "" || value == "" {
		return ValidateThinkingLevelWith(loadCatalog, provider, model, value)
	}
	labels := claudeEffortLabel
	if provider == "codex" {
		labels = codexEffortLabel
	}
	if _, known := labels[value]; !known {
		return false, nil
	}
	catalog, err := loadCatalog()
	if err != nil {
		return false, err
	}
	if catalog.CLIThinkingLevels != nil && !slices.Contains(catalog.CLIThinkingLevels, value) {
		return false, nil
	}
	return true, nil
}
