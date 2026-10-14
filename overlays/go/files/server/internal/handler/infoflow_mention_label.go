package handler

import (
	"fmt"
	"strings"
	"unicode"
)

// Labels are presentation only. The resolved target type and UUID retain the
// existing single-agent or squad-leader dispatch semantics.
func infoFlowNamedMention(kind, id, name string) string {
	label := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, name))
	if label == "" {
		label = id
	}
	label = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)").Replace(label)
	return fmt.Sprintf("[@%s](mention://%s/%s)", label, kind, id)
}
