package mentions

import "regexp"

var mentionRe = regexp.MustCompile(`@([A-Za-z0-9]{1,4})\b`)

// Parse returns unique @mention initials from text (matches pm frontend parseMentions).
func Parse(text string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		if _, ok := seen[m[1]]; ok {
			continue
		}
		seen[m[1]] = struct{}{}
		out = append(out, m[1])
	}
	return out
}
