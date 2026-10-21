package logutil

import "strings"

// Redacted replaces a secret value in job output.
const Redacted = "[redacted]"

// Redactor replaces known secret values in text. Values shorter than
// MinLen are not redacted: masking "1" or "ok" would mangle ordinary output
// and reveals nothing worth protecting.
type Redactor struct {
	replacer *strings.Replacer
}

// MinLen is the shortest secret value that is redacted.
const MinLen = 6

// NewRedactor builds a redactor for the given values. Longer values are
// replaced first so a secret that contains another is masked whole.
func NewRedactor(values []string) *Redactor {
	var pairs []string
	seen := map[string]bool{}
	// strings.Replacer picks the first matching pair at each position, so
	// order longer values first.
	sorted := make([]string, 0, len(values))
	for _, v := range values {
		if len(v) < MinLen || seen[v] {
			continue
		}
		seen[v] = true
		sorted = append(sorted, v)
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len(sorted[j]) > len(sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, v := range sorted {
		pairs = append(pairs, v, Redacted)
	}
	if len(pairs) == 0 {
		return nil
	}
	return &Redactor{replacer: strings.NewReplacer(pairs...)}
}

// Apply masks secrets in s. A nil Redactor returns s unchanged. A secret
// split across two chunks is not caught; chunks are cut on write
// boundaries, so this only happens when a job prints a secret in pieces.
func (r *Redactor) Apply(s string) string {
	if r == nil {
		return s
	}
	return r.replacer.Replace(s)
}
