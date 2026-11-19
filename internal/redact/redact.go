// Package redact scrubs secrets and personal details from prompt text before
// anything is uploaded in a submission bundle.
package redact

import "regexp"

var rules = []struct {
	re  *regexp.Regexp
	tag string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), "[private-key]"},
	{regexp.MustCompile(`\b(sk|pk|rk)-[A-Za-z0-9_\-]{16,}`), "[api-key]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), "[github-token]"},
	{regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9\-]{10,}`), "[slack-token]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[aws-key]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`), "[jwt]"},
	{regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+`), "$1=[redacted]"},
	{regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+\-]*://[^\s:/@]+:[^\s@/]+@`), "[url-credentials]@"},
	{regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), "[email]"},
	{regexp.MustCompile(`\b[A-Fa-f0-9]{40,}\b`), "[hex-secret]"},
	{regexp.MustCompile(`\+?\d[\d \-]{8,}\d`), "[phone]"},
}

// Text returns s with known secret shapes replaced by tags.
func Text(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.tag)
	}
	return s
}
