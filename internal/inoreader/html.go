package inoreader

import (
	"html"
	"strings"
	"unicode"
)

// StripHTML converts an HTML fragment to compact plain text: script/style
// content is removed, tags are dropped, entities are unescaped, and
// whitespace is collapsed. Block-level boundaries become single spaces.
func StripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	skipDepth := 0 // inside <script>/<style>
	i := 0
	for i < len(s) {
		ch := s[i]
		if inTag {
			if ch == '>' {
				inTag = false
			}
			i++
			continue
		}
		if ch == '<' {
			// Determine the tag name.
			j := i + 1
			closing := false
			if j < len(s) && s[j] == '/' {
				closing = true
				j++
			}
			k := j
			for k < len(s) && (s[k] >= 'a' && s[k] <= 'z' || s[k] >= 'A' && s[k] <= 'Z' || s[k] >= '0' && s[k] <= '9') {
				k++
			}
			name := strings.ToLower(s[j:k])
			if name == "script" || name == "style" {
				if closing {
					if skipDepth > 0 {
						skipDepth--
					}
				} else {
					skipDepth++
				}
			}
			inTag = true
			b.WriteByte(' ')
			i++
			continue
		}
		if skipDepth > 0 {
			i++
			continue
		}
		b.WriteByte(ch)
		i++
	}
	return collapseSpace(html.UnescapeString(b.String()))
}

func collapseSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || r == ' ' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// Truncate shortens s to at most n runes, appending an ellipsis if cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	cut := runes[:n]
	// Avoid cutting mid-word when possible.
	if idx := strings.LastIndexByte(string(cut), ' '); idx >= n/2 {
		cut = []rune(string(cut)[:idx])
	}
	return strings.TrimRight(string(cut), " ,;:") + "…"
}
