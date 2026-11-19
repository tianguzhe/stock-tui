package backtest

import (
	"errors"
	"reflect"
	"testing"

	"stock-tui/internal/store"
)

type fakePrices struct {
	bars       map[string]map[string]Bar
	errs       map[string]error
	prefetched []string
}

func (f *fakePrices) Bars(code string) (map[string]Bar, error) {
	if err := f.errs[code]; err != nil {
		return nil, err
	}
	return f.bars[code], nil
}

func (f *fakePrices) Prefetch(codes []string) map[string]error {
	f.prefetched = append(f.prefetched, codes...)
	return nil
}

func flat(c float64) Bar { return Bar{Open: c, High: c, Low: c, Close: c} }

// 掉出热榜不再截断持有期：snapshot 只到 06-12，价格源持续到 06-16，
// 按第 3 个全市场交易日 06-15 到期平仓；入场价取价格源 98 而非 snapshot 的 100
// （两者分属不同前复权基准时，混用会产生人为收益）。
func TestBarsPathHoldsThroughPoolExitWithConsistentEntry(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-11", "2026-06-12", "2026-06-15", "2026-06-16"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-11", Close: 95, Low: 94, High: 99},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-12", Close: 90, Low: 89, High: 96},
	)
	st := openTestStore(t, snaps)
	p := &fakePrices{bars: map[string]map[string]Bar{"sh600000": {
		"2026-06-10": flat(98), "2026-06-11": {Open: 96, High: 99, Low: 94, Close: 95},
		"2026-06-12": flat(90), "2026-06-15": flat(88), "2026-06-16": flat(92),
	}}}
	e := NewEngine(st.DB(), st, Config{HoldingDays: 3}).WithPrices(p)
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if outcome != outcomeExpired || res.ExitDate != "2026-06-15" || res.ExitPrice != 88 || res.EntryPrice != 98 || res.HoldingDays != 3 {
		t.Fatalf("outcome=%v result=%+v, want expiry 06-15 @88 from entry 98", outcome, res)
	}
	if !near(res.ReturnPct, (88.0-98)/98*100) {
		t.Fatalf("return = %v", res.ReturnPct)
	}
}

// 有真实开盘价时，跳空止损按开盘价成交（snapshot 口径只能用最高价 90 近似）。
func TestBarsPathGapStopFillsAtOpen(t *testing.T) {
	st := openTestStore(t, calendar("sz000001", "2026-06-11"))
	p := &fakePrices{bars: map[string]map[string]Bar{"sh600000": {
		"2026-06-10": flat(100), "2026-06-11": {Open: 88, High: 90, Low: 84, Close: 86},
	}}}
	e := NewEngine(st.DB(), st, Config{StopLoss: 5, HoldingDays: 1}).WithPrices(p)
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})
	if outcome != outcomeStop || res.ExitPrice != 88 || !near(res.ReturnPct, -12) {
		t.Fatalf("outcome=%v result=%+v, want gap stop filled at open 88", outcome, res)
	}
	if got := stopFillPrice(100, 5, 97, 94, 99, true); !near(got, 95) {
		t.Errorf("open above stop must still fill at stop, got %v", got)
	}
	if got := stopFillPrice(100, 5, 108, 104, 110, false); got != 108 {
		t.Errorf("short gap-up should fill at open 108, got %v", got)
	}
}

// 停牌：窗口中间缺一根仍按全市场交易日计；窗口末日停牌按最后一根收盘平仓并标记中断。
func TestBarsPathSuspension(t *testing.T) {
	st := openTestStore(t, calendar("sz000001", "2026-06-11", "2026-06-12", "2026-06-15"))
	p := &fakePrices{bars: map[string]map[string]Bar{"sh600000": {
		"2026-06-10": flat(100), "2026-06-11": flat(101), "2026-06-12": flat(103),
	}}}
	e := NewEngine(st.DB(), st, Config{HoldingDays: 3}).WithPrices(p)
	res, outcome := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})
	if outcome != outcomeDataGap || res.ExitDate != "2026-06-12" || res.ExitPrice != 103 || res.HoldingDays != 2 {
		t.Fatalf("outcome=%v result=%+v", outcome, res)
	}
}

func TestBarsPathNoEntryBarOrFetchError(t *testing.T) {
	st := openTestStore(t, calendar("sz000001", "2026-06-11"))
	p := &fakePrices{
		bars: map[string]map[string]Bar{"sh600000": {"2026-06-11": flat(101)}},
		errs: map[string]error{"hk00700": errors.New("not covered")},
	}
	e := NewEngine(st.DB(), st, Config{HoldingDays: 1}).WithPrices(p)
	if _, o := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "t", Direction: "多头", Close: 100}); o != outcomeNoData {
		t.Errorf("missing entry bar: outcome %v", o)
	}
	if _, o := e.simulateTrade(Signal{Date: "2026-06-10", Code: "hk00700", SignalType: "t", Direction: "多头", Close: 100}); o != outcomeNoData {
		t.Errorf("fetch error: outcome %v", o)
	}
}

// 组合回测：snapshot 里 06-02 后就掉榜的持仓，在价格源口径下持有满 3 天按到期平仓，
// 不再「出池平仓」；入场价取价格源。
func TestPortfolioWithPricesHoldsAfterPoolExit(t *testing.T) {
	snaps := append(calendar("sz000001", "2026-06-01", "2026-06-02", "2026-06-03", "2026-06-04", "2026-06-05"),
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-01", Close: 10.2, Low: 10, High: 10.4, SigTrendBull: true},
		store.Snapshot{Code: "sh600000", TradeDate: "2026-06-02", Close: 9, Low: 9, High: 9},
	)
	st := openTestStore(t, snaps)
	p := &fakePrices{bars: map[string]map[string]Bar{"sh600000": {
		"2026-06-01": flat(10), "2026-06-02": flat(9), "2026-06-03": flat(9.5), "2026-06-04": flat(11), "2026-06-05": flat(12),
	}}}
	e := NewPortfolioEngine(st.DB(), st, PortfolioConfig{
		Config:       Config{StartDate: "2026-06-01", EndDate: "2026-06-05", HoldingDays: 3, Signals: []string{"趋势跟随多头"}},
		MaxPositions: 1,
	}).WithPrices(p)
	trades, equity, _, err := e.simulate()
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 {
		t.Fatalf("trades = %+v", trades)
	}
	tr := trades[0]
	if tr.ExitReason != "到期" || tr.ExitDate != "2026-06-04" || tr.ExitPrice != 11 || tr.EntryPrice != 10 || tr.Shares != 2000 {
		t.Fatalf("trade = %+v, want 到期 06-04 @11 from entry 10 (price source), 2000 shares", tr)
	}
	if len(equity) != 5 {
		t.Fatalf("equity days = %d", len(equity))
	}
	if !reflect.DeepEqual(p.prefetched, []string{"sh600000"}) {
		t.Fatalf("prefetched = %v", p.prefetched)
	}
}

func TestLastBarAndHasBarIn(t *testing.T) {
	bars := map[string]Bar{"2026-06-01": flat(1), "2026-06-03": flat(3)}
	if d, c, ok := lastBarOnOrBefore(bars, "2026-06-02"); !ok || d != "2026-06-01" || c != 1 {
		t.Errorf("lastBar = %s %v %v", d, c, ok)
	}
	if _, _, ok := lastBarOnOrBefore(bars, "2026-05-31"); ok {
		t.Error("nothing before 05-31")
	}
	if !hasBarIn(bars, "2026-06-01", "2026-06-03") || hasBarIn(bars, "2026-06-03", "2026-06-30") {
		t.Error("hasBarIn bounds wrong")
	}
}
