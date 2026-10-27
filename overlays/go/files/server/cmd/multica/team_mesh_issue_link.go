package main

import (
	"fmt"
	"net/url"
	"strings"
)

// parseTeamMeshIssueLink extracts a task reference without contacting its host.
// The caller must supply trusted Team Mesh origins and authorize the target on
// the server before using the result. Query parameters are never forwarded.
func parseTeamMeshIssueLink(raw string, trustedOrigins []string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", fmt.Errorf("invalid Team Mesh issue link")
	}
	trusted := false
	for _, origin := range trustedOrigins {
		v, e := url.Parse(origin)
		if e == nil && v.User == nil && strings.EqualFold(v.Scheme, u.Scheme) && strings.EqualFold(v.Host, u.Host) {
			trusted = true
			break
		}
	}
	if !trusted {
		return "", "", fmt.Errorf("issue link is not a trusted Team Mesh origin")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "issues" || parts[2] == "" || strings.Contains(parts[0], "\\") || strings.Contains(parts[2], "\\") {
		return "", "", fmt.Errorf("invalid Team Mesh issue path")
	}
	if !looksLikeIssueIdentifier(parts[2]) && !uuidRegexp.MatchString(parts[2]) {
		return "", "", fmt.Errorf("invalid Team Mesh issue reference")
	}
	return parts[0], parts[2], nil
}
