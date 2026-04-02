package http

import "testing"

// TestCanonicalPath verifies request paths are normalised consistently before
// authorisation and proxying.
func TestCanonicalPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", "/"},
		{"root", "/", "/"},
		{"duplicate slashes", "/api/v1/users//123", "/api/v1/users/123"},
		{"dot segment", "/api/v1/users/../products/123", "/api/v1/products/123"},
		{"missing leading slash", "api/v1/users/123", "/api/v1/users/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalPath(tt.input)
			if got != tt.want {
				t.Fatalf("canonicalPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
