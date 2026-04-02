package http

import (
	"errors"
	"strings"
)

var (
	// errMissingBearerToken indicates that the Authorization header was absent.
	errMissingBearerToken = errors.New("missing bearer token")
	// errInvalidBearerToken indicates that the Authorization header was malformed.
	errInvalidBearerToken = errors.New("invalid bearer token")
)

// extractBearerToken parses the Authorization header and returns the bearer token value.
func extractBearerToken(headerValue string) (string, error) {
	if strings.TrimSpace(headerValue) == "" {
		return "", errMissingBearerToken
	}

	parts := strings.SplitN(headerValue, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", errInvalidBearerToken
	}

	return strings.TrimSpace(parts[1]), nil
}
