package cli

import (
	"strconv"
	"strings"
)

// Fork release tags have a numeric overlay revision. Source builds keep a
// commit suffix and must never become eligible for unattended replacement.
func forkReleaseVersion(raw string) ([4]uint64, bool) {
	var result [4]uint64
	value := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	base, revision, ok := strings.Cut(value, "-overlay.")
	if !ok {
		return result, false
	}
	parts := append(strings.Split(base, "."), revision)
	if len(parts) != len(result) {
		return result, false
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return result, false
			}
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return result, false
		}
		result[i] = value
	}
	return result, true
}

func IsForkReleaseVersion(version string) bool {
	_, ok := forkReleaseVersion(version)
	return ok
}
