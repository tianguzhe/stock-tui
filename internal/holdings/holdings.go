// Package holdings parses the .holdings portfolio file that both the screener
// and the watch TUI read.
//
// The file lists one or more `code:cost:shares` items separated by commas or
// newlines. Lines starting with `#` are comments, conventionally used to split
// the portfolio across brokerage accounts:
//
//	# 银河证券账户
//	sh601138:61.551:2,sz159858:0.709:151
//	# 国泰海通证券账户
//	sh600909:9.465:6
//
// The same code may appear under several accounts; Merge folds those into one
// position with a share-weighted average cost, which is the figure every
// downstream P&L and stop-loss calculation expects.
package holdings

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPath is the portfolio file read when no explicit path is given.
const DefaultPath = ".holdings"

// Holding is one position. Shares counts 手 (lots), where 1 lot = 100 shares.
type Holding struct {
	Code   string
	Cost   float64 // per-share cost
	Shares int     // 手
}

// Load reads path and returns its holdings with duplicate codes merged.
// A missing file is reported as an error wrapping os.ErrNotExist so callers
// can treat "no portfolio file" as an empty portfolio if that suits them.
func Load(path string) ([]Holding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read holdings %s: %w", path, err)
	}
	hs, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse holdings %s: %w", path, err)
	}
	return hs, nil
}

// Parse reads holdings from comma- and/or newline-separated `code:cost:shares`
// items, skipping blank lines and `#` comments. Duplicate codes are merged.
//
// A malformed item is an error rather than a skipped line: these figures drive
// P&L and position sizing, and silently dropping one understates the portfolio
// without any visible symptom.
func Parse(raw string) ([]Holding, error) {
	var out []Holding
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, item := range strings.Split(line, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			h, err := parseItem(item)
			if err != nil {
				return nil, err
			}
			out = append(out, h)
		}
	}
	return Merge(out), nil
}

func parseItem(item string) (Holding, error) {
	parts := strings.Split(item, ":")
	if len(parts) != 3 {
		return Holding{}, fmt.Errorf("invalid holding %q (expected code:cost:shares)", item)
	}
	code := strings.TrimSpace(parts[0])
	if code == "" {
		return Holding{}, fmt.Errorf("invalid holding %q: empty code", item)
	}
	cost, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return Holding{}, fmt.Errorf("invalid cost in holding %q: %w", item, err)
	}
	if cost <= 0 {
		return Holding{}, fmt.Errorf("invalid cost in holding %q: must be positive", item)
	}
	shares, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return Holding{}, fmt.Errorf("invalid shares in holding %q: %w", item, err)
	}
	if shares <= 0 {
		return Holding{}, fmt.Errorf("invalid shares in holding %q: must be positive", item)
	}
	return Holding{Code: code, Cost: cost, Shares: shares}, nil
}

// Merge folds duplicate codes into one position whose cost is the
// share-weighted average, preserving first-appearance order so output stays
// stable across runs.
func Merge(in []Holding) []Holding {
	if len(in) < 2 {
		return in
	}
	idx := make(map[string]int, len(in))
	out := make([]Holding, 0, len(in))
	for _, h := range in {
		i, ok := idx[h.Code]
		if !ok {
			idx[h.Code] = len(out)
			out = append(out, h)
			continue
		}
		total := out[i].Shares + h.Shares
		if total <= 0 {
			continue
		}
		out[i].Cost = (out[i].Cost*float64(out[i].Shares) + h.Cost*float64(h.Shares)) / float64(total)
		out[i].Shares = total
	}
	return out
}
