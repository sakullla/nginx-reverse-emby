package agentutil

import "testing"

func TestParseInt64Default(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback int64
		want     int64
	}{
		{name: "valid", raw: "42", fallback: 7, want: 42},
		{name: "trimmed", raw: " 9 ", fallback: 7, want: 9},
		{name: "blank", raw: "", fallback: 7, want: 7},
		{name: "invalid", raw: "nope", fallback: 7, want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseInt64Default(tt.raw, tt.fallback); got != tt.want {
				t.Fatalf("ParseInt64Default(%q, %d) = %d, want %d", tt.raw, tt.fallback, got, tt.want)
			}
		})
	}
}
