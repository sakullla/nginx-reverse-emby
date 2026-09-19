package sanitize

import (
	"encoding/json"
	"regexp"
	"strings"
)

const Redacted = "[REDACTED]"

var credentialPattern = regexp.MustCompile(`(?i)((?:authorization|cookie|credential|password|private[_-]?key|secret|token|api[_-]?key)\s*[:=]\s*)[^\s,;]+|Bearer\s+[^\s,;]+`)
var quotedCredentialPattern = regexp.MustCompile(`((?:"[^"]+"|'[^']+')\s*:\s*)(?:"(?:\\.|[^"\\])*"?|'(?:\\.|[^'\\])*'?|[^\s,}\]]+)`)
var urlCredentialPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s/:@]+:)[^\s/@]+@`)
var privateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)

// Text is the single host-owned sanitizer for guest-controlled diagnostics.
// It recognizes structured JSON before falling back to conservative textual
// credentials and always masks broker-known exact values, including their JSON
// escaped representation.
func Text(input string, exact []string) string {
	input = replaceExact(input, exact)
	var value any
	trimmed := strings.TrimSpace(input)
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Unmarshal([]byte(input), &value) == nil {
		encoded, err := json.Marshal(structured(value, exact))
		if err == nil {
			input = string(encoded)
		}
	}
	input = quotedCredentialPattern.ReplaceAllStringFunc(input, func(match string) string {
		separator := strings.Index(match, ":")
		if separator < 0 || !credentialKey(strings.Trim(strings.TrimSpace(match[:separator]), `"'`)) {
			return match
		}
		return match[:separator+1] + `"[REDACTED]"`
	})
	input = privateKeyPattern.ReplaceAllString(input, "[REDACTED PRIVATE KEY]")
	input = urlCredentialPattern.ReplaceAllString(input, `${1}[REDACTED]@`)
	return credentialPattern.ReplaceAllStringFunc(input, func(match string) string {
		if strings.HasPrefix(strings.ToLower(match), "bearer ") {
			return "Bearer " + Redacted
		}
		if index := strings.IndexAny(match, ":="); index >= 0 {
			return match[:index+1] + Redacted
		}
		return Redacted
	})
}

func structured(value any, exact []string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if credentialKey(key) {
				out[key] = Redacted
			} else {
				out[key] = structured(item, exact)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = structured(typed[i], exact)
		}
		return out
	case string:
		return Text(typed, exact)
	default:
		return value
	}
}

func credentialKey(key string) bool {
	var normalized strings.Builder
	for _, current := range key {
		if current >= 'A' && current <= 'Z' {
			current += 'a' - 'A'
		}
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
			normalized.WriteRune(current)
		}
	}
	value := normalized.String()
	for _, marker := range []string{"authorization", "cookie", "credential", "password", "privatekey", "secret", "token", "apikey"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func replaceExact(input string, exact []string) string {
	for _, secret := range exact {
		if secret == "" {
			continue
		}
		input = strings.ReplaceAll(input, secret, Redacted)
		if encoded, err := json.Marshal(secret); err == nil && len(encoded) >= 2 {
			input = strings.ReplaceAll(input, string(encoded[1:len(encoded)-1]), Redacted)
		}
	}
	return input
}
