package backtest

import (
	"math"
	"path/filepath"
	"testing"

	"stock-tui/internal/store"
)

func openTestStore(t *testing.T, snaps []store.Snapshot) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for _, s := range snaps {
		if err := st.SaveSnapshot(s); err != nil {
			t.Fatalf("save %s@%s: %v", s.Code, s.TradeDate, err)
		}
	}
	return st
}

// calendar 用另一只标的铺出全市场交易日，被测标的在其中提前消失(掉出热榜)。
func calendar(code string, dates ...string) []store.Snapshot {
	out := make([]store.Snapshot, len(dates))
	for i, d := range dates {
		out[i] = store.Snapshot{Code: code, TradeDate: d, Close: 50, Low: 50, High: 50}
	}
	return out
}

// 掉出热榜的标的不能再从统计中消失：按最后一条行情平仓并计入。
func TestSimulateTradeExitsAtLastCloseWhenStockLeavesPool(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-11", "2026-06-12", "2026-06-15", "2026-06-16"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-11", Close: 95, Low: 94, High: 99},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-12", Close: 90, Low: 89, High: 96},
	)
	st := openTestStore(t, snaps)
	e := NewEngine(st.DB(), st, Config{HoldingDays: 3})
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if outcome != outcomeDataGap {
		t.Fatalf("outcome = %v, want outcomeDataGap", outcome)
	}
	if res.ExitDate != "2026-06-12" || res.ExitPrice != 90 || res.ReturnPct != -10 || res.HoldingDays != 2 {
		t.Fatalf("result = %+v, want exit 2026-06-12 @90 -10%% after 2 days", res)
	}
}

// 持有窗口按全市场交易日计：标的中途缺一天(停牌/暂时出池)不应拉长持有期。
func TestSimulateTradeWindowCountsMarketDaysNotStockRows(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-11", "2026-06-12", "2026-06-15"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-11", Close: 101, Low: 100, High: 102},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-15", Close: 104, Low: 103, High: 105},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-16", Close: 120, Low: 119, High: 121},
	)
	st := openTestStore(t, snaps)
	e := NewEngine(st.DB(), st, Config{HoldingDays: 3})
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if outcome != outcomeExpired || res.ExitDate != "2026-06-15" || res.ExitPrice != 104 {
		t.Fatalf("outcome=%v result=%+v, want expiry on 3rd market day 2026-06-15 @104", outcome, res)
	}
}

// 全市场交易日还不足 N 天：真正的待结算，不可提前平仓。
func TestSimulateTradePendingWhenMarketWindowIncomplete(t *testing.T) {
	snaps := []store.Snapshot{{Code: "sh600000", TradeDate: "2026-06-11", Close: 101, Low: 100, High: 102}}
	st := openTestStore(t, snaps)
	e := NewEngine(st.DB(), st, Config{HoldingDays: 3})
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if outcome != outcomePending || res.ExitDate != "" {
		t.Fatalf("outcome=%v ExitDate=%q, want pending with empty ExitDate", outcome, res.ExitDate)
	}
}

// 组合回测：持仓标的掉出热榜后按末次收盘平仓并计入交易；卖出扣印花税。
func TestPortfolioClosesPositionThatLeavesPool(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-01", "2026-06-02", "2026-06-03", "2026-06-04", "2026-06-05"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-01", Close: 10, Low: 10, High: 10, SigTrendBull: true},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-02", Close: 9, Low: 9, High: 9},
	)
	st := openTestStore(t, snaps)
	e := NewPortfolioEngine(st.DB(), st, PortfolioConfig{
		Config:       Config{StartDate: "2026-06-01", EndDate: "2026-06-05", HoldingDays: 3, Signals: []string{"趋势跟随多头"}},
		MaxPositions: 1,
	})
	trades, _, _, err := e.simulate()
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 {
		t.Fatalf("trades = %+v, want the dropped-out position recorded", trades)
	}
	tr := trades[0]
	if tr.ExitReason != exitReasonDataGap || tr.ExitDate != "2026-06-02" || tr.ExitPrice != 9 {
		t.Fatalf("trade = %+v, want %s on 2026-06-02 @9", tr, exitReasonDataGap)
	}
	// 100000×20%/10 = 2000 股；买入含佣金，卖出扣佣金 + 印花税
	cost := 2000 * 10 * (1 + 0.0003)
	revenue := 2000 * 9 * (1 - 0.0003 - 0.0005)
	if math.Abs(tr.PnL-(revenue-cost)) > 1e-6 {
		t.Fatalf("PnL = %.4f, want %.4f (stamp tax on sell)", tr.PnL, revenue-cost)
	}
}

func TestSellCostRateExemptsFundsFromStampTax(t *testing.T) {
	cfg := PortfolioConfig{Commission: 0.0003, StampTax: 0.0005}
	if got := sellCostRate(cfg, "sh600000"); math.Abs(got-0.0008) > 1e-12 {
		t.Errorf("stock sell cost = %v, want 0.0008", got)
	}
	for _, code := range []string{"sh512480", "sz159915", "sh588000"} {
		if got := sellCostRate(cfg, code); got != 0.0003 {
			t.Errorf("%s sell cost = %v, want commission only", code, got)
		}
	}
}

// 组合回测只做多：看空信号(超买反转空头等)不能被当成买入信号建仓。
func TestPortfolioIgnoresBearishSignals(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-01", "2026-06-02", "2026-06-03"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-01", Close: 10, Low: 10, High: 10, SigOverbought: true},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-02", Close: 9, Low: 9, High: 9},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-03", Close: 8, Low: 8, High: 8},
	)
	st := openTestStore(t, snaps)
	e := NewPortfolioEngine(st.DB(), st, PortfolioConfig{
		Config:       Config{StartDate: "2026-06-01", EndDate: "2026-06-03", HoldingDays: 2, Signals: []string{"超买反转空头"}},
		MaxPositions: 1,
	})
	trades, _, capital, err := e.simulate()
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 || capital != e.config.InitialCapital {
		t.Fatalf("trades=%+v capital=%.2f, want no position opened on a bearish signal", trades, capital)
	}
}
