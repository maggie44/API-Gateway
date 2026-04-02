package http

import (
	"path"
	"strings"
)

// canonicalPath collapses dot-segments and duplicate separators so route
// matching, authorisation, and upstream proxying all operate on the same path
// interpretation.
func canonicalPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "/" {
		return "/"
	}

	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}

	return path.Clean(trimmed)
}
