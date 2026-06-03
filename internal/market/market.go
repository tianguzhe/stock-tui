package market

import "strings"

// NormalizeCodes expands comma-separated user inputs into provider codes.
//
// The rules match the runtime CLI behavior: prefixed codes pass through, while
// bare six-digit A-share/ETF/fund/bond codes are mapped to Tencent-style market
// prefixes.
func NormalizeCodes(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, group := range raw {
		for _, code := range strings.Split(group, ",") {
			code = strings.TrimSpace(code)
			if code == "" {
				continue
			}
			normalized, ok := NormalizeCode(code)
			if ok {
				out = append(out, normalized)
			}
		}
	}
	return out
}

// NormalizeCode returns a Tencent-style market code and whether the input was
// recognized. Non-prefixed inputs must be exactly six digits, preserving the
// previous TUI behavior of ignoring malformed bare values.
func NormalizeCode(code string) (string, bool) {
	code = strings.TrimSpace(code)
	lower := strings.ToLower(code)
	if strings.HasPrefix(lower, "sh") || strings.HasPrefix(lower, "sz") ||
		strings.HasPrefix(lower, "bj") || strings.HasPrefix(lower, "hk") {
		return lower, true
	}
	if len(code) != 6 {
		return "", false
	}

	switch code[:2] {
	case "11":
		return "sh" + code, true
	case "12", "15", "16", "18":
		return "sz" + code, true
	case "43", "82", "83", "87", "88", "92":
		return "bj" + code, true
	default:
		switch code[0] {
		case '6', '5':
			return "sh" + code, true
		case '0', '3':
			return "sz" + code, true
		default:
			return "sh" + code, true
		}
	}
}
