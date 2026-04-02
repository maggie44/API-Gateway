package http

import (
	"errors"
	"testing"
)

// TestExtractBearerToken verifies Authorization header parsing edge cases in the HTTP adapter.
func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantToken string
		wantErr   error
	}{
		{"valid bearer", "Bearer demo-token", "demo-token", nil},
		{"lowercase bearer", "bearer demo-token", "demo-token", nil},
		{"token with surrounding spaces", "Bearer  demo-token ", "demo-token", nil},
		{"empty header", "", "", errMissingBearerToken},
		{"whitespace only", "   ", "", errMissingBearerToken},
		{"wrong scheme", "Token demo-token", "", errInvalidBearerToken},
		{"bearer without value", "Bearer ", "", errInvalidBearerToken},
		{"bearer prefix only", "Bearer", "", errInvalidBearerToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractBearerToken(tt.header)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if got != tt.wantToken {
				t.Fatalf("expected token %q, got %q", tt.wantToken, got)
			}
		})
	}
}
