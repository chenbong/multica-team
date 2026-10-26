package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/cors"
)

// CORSOriginPolicy changes only browser origin authorization. The concrete
// origin must be reflected, not "*", because credentialed CORS forbids "*".
func CORSOriginPolicy(options cors.Options) cors.Options {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MULTICA_CORS_ALLOW_ALL_ORIGINS")), "true") {
		options.AllowedOrigins = nil
		options.AllowOriginFunc = func(_ *http.Request, _ string) bool { return true }
	}
	return options
}

// AttachmentFrameAncestorsUnrestricted does not change file authorization,
// cookie policy, sandboxing or any other Content-Security-Policy directive.
func AttachmentFrameAncestorsUnrestricted() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("MULTICA_ATTACHMENT_ALLOW_ALL_FRAME_ANCESTORS")), "true")
}
