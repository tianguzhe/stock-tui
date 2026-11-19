package backtest

import (
	"fmt"
	"os"
	"sort"
)

// Bar 是一根日K。Open 为 0 表示未知（snapshot 无 open 列）。
type Bar struct {
	Open, High, Low, Close float64
}

// PriceSource 提供与 snapshot 无关的连续价格序列，用来消除快照回测的两个结构性偏差：
//
//  1. 出池断档：snapshot 只覆盖热榜池，标的掉榜后再无行情，只能按最后一条收盘
//     提前平仓（outcomeDataGap / 「出池平仓」）。掉榜多半是走弱标的，持有期被截短
//     会系统性低估亏损；
//  2. 混合前复权基准：snapshot 逐日写入，除权日之前的行是旧基准，入场价与后续
//     价格可能分属两套基准，产生人为跳空（exright --pool 实测 2026-10-15 后 7 处，
//     大洋电机 10-30 序列 -10.99% vs 实际 -10.01%）。
//
// 实现方必须保证同一 code 的全部价格来自同一次拉取（同一前复权基准）；入场价也必须
// 从这里取，不能混用 snapshot.close。
type PriceSource interface {
	// Bars 返回 code 的日K，键为 YYYY-MM-DD。停牌日没有键。
	Bars(code string) (map[string]Bar, error)
}

// Prefetcher 是可选扩展：回测开始前并发预取全部标的，避免逐笔串行请求。
type Prefetcher interface {
	Prefetch(codes []string) (failed map[string]error)
}

// WithPrices 让单信号回测改用 p 的价格；p 为 nil 时保持 snapshot 口径。
func (e *Engine) WithPrices(p PriceSource) *Engine {
	e.prices = p
	return e
}

// WithPrices 让组合回测改用 p 的价格；p 为 nil 时保持 snapshot 口径。
func (e *PortfolioEngine) WithPrices(p PriceSource) *PortfolioEngine {
	e.prices = p
	return e
}

// prefetchSignals 对信号涉及的全部标的预取价格，并逐只报出失败（不静默丢弃）。
func prefetchSignals(p PriceSource, signals []Signal) {
	pf, ok := p.(Prefetcher)
	if !ok {
		return
	}
	seen := map[string]bool{}
	var codes []string
	for _, s := range signals {
		if !seen[s.Code] {
			seen[s.Code] = true
			codes = append(codes, s.Code)
		}
	}
	sort.Strings(codes)
	failed := pf.Prefetch(codes)
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d/%d 只标的价格预取失败，相关信号计为「窗口内无行情」\n", len(failed), len(codes))
		for c, err := range failed {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", c, err)
		}
	}
}

// lastBarOnOrBefore 返回 date(含) 之前最后一根K线的日期与收盘价。
func lastBarOnOrBefore(bars map[string]Bar, date string) (string, float64, bool) {
	best := ""
	for d := range bars {
		if d <= date && d > best {
			best = d
		}
	}
	if best == "" {
		return "", 0, false
	}
	return best, bars[best].Close, true
}

// hasBarIn 判断 (after, until] 内是否还有K线（区分停牌与真正无行情）。
func hasBarIn(bars map[string]Bar, after, until string) bool {
	for d := range bars {
		if d > after && d <= until {
			return true
		}
	}
	return false
}
