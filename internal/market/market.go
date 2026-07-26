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

// Prefix returns the sh/sz/bj/hk market prefix of a normalized code — the prefix
// NormalizeCode prepends — or "" if the code is too short to carry one.
func Prefix(code string) string {
	if len(code) >= 2 {
		return code[:2]
	}
	return ""
}

// IsST reports whether the instrument name carries a risk-warning designation
// (ST / *ST / SST / S*ST). Such names face delisting risk, thin liquidity and a
// tighter daily price limit, so screeners exclude them outright.
func IsST(name string) bool {
	return strings.Contains(strings.ToUpper(name), "ST")
}

// NoPriceLimit is returned by PriceLimitPct for markets that impose no daily
// price limit at all (Hong Kong). Callers must skip their limit-up/limit-down
// gate entirely on this value — treating it as a percentage would reject every
// ordinary move.
const NoPriceLimit = 0

// PriceLimitPct returns the daily price-limit percentage for a normalized code,
// or NoPriceLimit when the market has none.
//
// Screeners use it to recognize a limit-up/limit-down bar. Hard-coding 10%
// silently mis-classifies every non-main-board name: a ChiNext stock rising 12%
// is ordinary movement, not a locked limit.
//
//	港股 (hk*)                                : 无限制 → NoPriceLimit
//	北交所 (bj*)                              : 30%
//	创业板 (sz300/301) / 科创板 (sh688/689)   : 20%
//	科创板 ETF (sh588)                        : 20%
//	主板 (sh600/601/603/605, sz000/001/002/003) 及其余 ETF/基金 : 10%
//	主板 ST/*ST                               : 5%
//
// ST on ChiNext/STAR keeps the 20% board limit — the 5% rule is main-board only.
//
// ETF caveat: a fund's limit follows what it tracks, which the code prefix
// cannot settle in general — sz159 mixes 10% and 20% products. Only the sh588
// block is uniformly STAR (20%); everything else stays at the conservative 10%,
// so the caller's gate fires earlier rather than later. Unknown or malformed
// codes fall back to 10% for the same reason.
func PriceLimitPct(code, name string) float64 {
	lower := strings.ToLower(strings.TrimSpace(code))
	switch {
	case strings.HasPrefix(lower, "hk"):
		return NoPriceLimit
	case strings.HasPrefix(lower, "bj"):
		return 30
	case strings.HasPrefix(lower, "sz300"), strings.HasPrefix(lower, "sz301"),
		strings.HasPrefix(lower, "sh688"), strings.HasPrefix(lower, "sh689"),
		strings.HasPrefix(lower, "sh588"):
		return 20
	}
	if IsST(name) {
		return 5
	}
	return 10
}
