package backtest

import (
	"math"
	"testing"

	"stock-tui/internal/store"
)

// 跳空低开整日都在止损价下方(最高价 < 止损价)：按止损价成交会低估亏损。
// snapshot 无 open 列，最高价是开盘价的上界，按它成交是最接近且不偏悲观的近似。
func TestSimulateTradeGapThroughStopFillsAtHigh(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-11", "2026-06-12"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-11", Close: 85, Low: 84, High: 88},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-12", Close: 86, Low: 85, High: 87},
	)
	st := openTestStore(t, snaps)
	e := NewEngine(st.DB(), st, Config{StopLoss: 5, HoldingDays: 2})
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if outcome != outcomeStop || res.ExitPrice != 88 || !near(res.ReturnPct, -12) {
		t.Fatalf("outcome=%v result=%+v, want stop filled @88 (-12%%), not @95", outcome, res)
	}
}

// 空头镜像：跳空高开整日在止损价上方，按最低价成交。
func TestSimulateTradeShortGapThroughStopFillsAtLow(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-11"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-11", Close: 112, Low: 110, High: 115},
	)
	st := openTestStore(t, snaps)
	e := NewEngine(st.DB(), st, Config{StopLoss: 5, HoldingDays: 1})
	res, _ := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "空头", Close: 100})

	if res.ExitPrice != 110 || !near(res.ReturnPct, -10) {
		t.Fatalf("result=%+v, want short stop filled @110 (-10%%)", res)
	}
}

// 盘中才穿过止损价(最高价 ≥ 止损价)：仍按止损价成交。
func TestStopFillPriceIntradayTouchUsesStopPrice(t *testing.T) {
	if got := stopFillPrice(100, 5, 94, 99, true); !near(got, 95) {
		t.Errorf("long intraday touch fill = %v, want 95", got)
	}
	if got := stopFillPrice(100, 5, 101, 106, false); !near(got, 105) {
		t.Errorf("short intraday touch fill = %v, want 105", got)
	}
}

// 信号日封板：多头涨停买不到、空头跌停卖不出，按板块限幅判定(主板 10% / 创业板 20%)。
func TestEntryBlockedAtPriceLimit(t *testing.T) {
	cases := []struct {
		sig  Signal
		want bool
	}{
		{Signal{Code: "sh600000", Direction: "多头", ChangePct: 9.98}, true},
		{Signal{Code: "sh600000", Direction: "多头", ChangePct: 9.2}, false},
		{Signal{Code: "sz300750", Direction: "多头", ChangePct: 12}, false}, // 创业板 12% 是普通波动
		{Signal{Code: "sz300750", Direction: "多头", ChangePct: 19.99}, true},
		{Signal{Code: "sh600000", Name: "ST测试", Direction: "多头", ChangePct: 5}, true},
		{Signal{Code: "sh600000", Direction: "多头", ChangePct: -10}, false}, // 跌停可以买入
		{Signal{Code: "sh600000", Direction: "空头", ChangePct: -9.99}, true},
		{Signal{Code: "sh600000", Direction: "空头", ChangePct: 10}, false},
		{Signal{Code: "hk00700", Direction: "多头", ChangePct: 15}, false}, // 港股无涨跌停
	}
	for _, c := range cases {
		if got := entryBlocked(c.sig); got != c.want {
			t.Errorf("entryBlocked(%s %s %s %.2f%%) = %v, want %v",
				c.sig.Code, c.sig.Name, c.sig.Direction, c.sig.ChangePct, got, c.want)
		}
	}
}

// 单信号回测：涨停日信号不计入统计(无成交日)，findSignals 须带出涨跌幅与名称。
func TestRunSkipsLimitUpEntry(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-10", "2026-06-11"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-10", Close: 11, ChangePct: 10, Low: 10.5, High: 11, SigTrendBull: true},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-11", Close: 10, Low: 10, High: 10},
	)
	st := openTestStore(t, snaps)
	e := NewEngine(st.DB(), st, Config{StartDate: "2026-06-10", EndDate: "2026-06-10", HoldingDays: 1, Signals: []string{"趋势跟随多头"}})
	sigs, err := e.findSignals()
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || sigs[0].ChangePct != 10 {
		t.Fatalf("signals = %+v, want one signal carrying ChangePct=10", sigs)
	}
	res, outcome := e.simulateTrade(sigs[0])
	if outcome != outcomeLimitLocked || res.ExitDate != "" {
		t.Fatalf("outcome=%v result=%+v, want limit-locked with empty ExitDate (excluded from stats)", outcome, res)
	}
}

// 组合回测：涨停日不能建仓，次日无信号则不应有交易。
func TestPortfolioSkipsLimitUpEntry(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-01", "2026-06-02", "2026-06-03"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-01", Close: 11, ChangePct: 10, Low: 10.5, High: 11, SigTrendBull: true},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-02", Close: 12, Low: 12, High: 12},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-03", Close: 13, Low: 13, High: 13},
	)
	st := openTestStore(t, snaps)
	e := NewPortfolioEngine(st.DB(), st, PortfolioConfig{
		Config:       Config{StartDate: "2026-06-01", EndDate: "2026-06-03", HoldingDays: 2, Signals: []string{"趋势跟随多头"}},
		MaxPositions: 1,
	})
	trades, _, _, err := e.simulate()
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Fatalf("trades = %+v, want no entry on a limit-up close", trades)
	}
}

// 组合回测：跳空穿过止损价按当日最高价成交。
func TestPortfolioGapThroughStopFillsAtHigh(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-01", "2026-06-02", "2026-06-03"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-01", Close: 10, Low: 10, High: 10, SigTrendBull: true},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-02", Close: 8, Low: 7.9, High: 8.5},
	)
	st := openTestStore(t, snaps)
	e := NewPortfolioEngine(st.DB(), st, PortfolioConfig{
		Config:       Config{StartDate: "2026-06-01", EndDate: "2026-06-03", HoldingDays: 5, StopLoss: 8, Signals: []string{"趋势跟随多头"}},
		MaxPositions: 1,
	})
	trades, _, _, err := e.simulate()
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].ExitReason != "止损" || trades[0].ExitPrice != 8.5 {
		t.Fatalf("trades = %+v, want 止损 filled @8.5 (day high), not @9.2", trades)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
