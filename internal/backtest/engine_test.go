package backtest

import (
	"path/filepath"
	"testing"

	"stock-tui/internal/store"
)

// TestSimulateTradeIntradayStop verifies the stop fires on the day's intraday
// low (not just its close): a day that closes only -2% but traded -6% intraday
// must trigger a -5% stop. The pre-fix close-only logic would have missed it.
func TestSimulateTradeIntradayStop(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertInstrument("sh600000", "测试", "sh", ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Entry at 100 on 06-10; next day closes 98 (-2%) but low 94 (-6% intraday).
	for _, s := range []store.Snapshot{
		{Code: "sh600000", TradeDate: "2026-06-10", Close: 100, Low: 99, High: 101},
		{Code: "sh600000", TradeDate: "2026-06-11", Close: 98, Low: 94, High: 100},
		{Code: "sh600000", TradeDate: "2026-06-12", Close: 97, Low: 96, High: 99},
	} {
		if err := st.SaveSnapshot(s); err != nil {
			t.Fatalf("save snapshot %s: %v", s.TradeDate, err)
		}
	}

	e := NewEngine(st.DB(), st, Config{StopLoss: 5, HoldingDays: 10})
	res := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if res.ExitDate != "2026-06-11" {
		t.Fatalf("ExitDate = %q, want 2026-06-11 (intraday stop day)", res.ExitDate)
	}
	if res.ReturnPct != -5 {
		t.Errorf("ReturnPct = %.2f, want -5 (filled at stop price)", res.ReturnPct)
	}
	if res.ExitPrice != 95 {
		t.Errorf("ExitPrice = %.2f, want 95 (entry*(1-5%%))", res.ExitPrice)
	}
	if res.MaxAdversePct > -6 { // intraday low recorded -6 before the stop
		t.Errorf("MaxAdversePct = %.2f, want ≤ -6 (intraday low)", res.MaxAdversePct)
	}
	if res.Win != 0 {
		t.Errorf("Win = %d, want 0", res.Win)
	}
}

// TestSimulateTradeNoStopHoldsToExpiry verifies a position whose intraday low
// never breaches the stop holds to the holding-period exit at close.
func TestSimulateTradeNoStopHoldsToExpiry(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bt2.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertInstrument("sh600000", "测试", "sh", ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for _, s := range []store.Snapshot{
		{Code: "sh600000", TradeDate: "2026-06-11", Close: 101, Low: 99, High: 102},
		{Code: "sh600000", TradeDate: "2026-06-12", Close: 103, Low: 100, High: 104},
	} {
		if err := st.SaveSnapshot(s); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	e := NewEngine(st.DB(), st, Config{StopLoss: 5, HoldingDays: 2})
	res := e.simulateTrade(Signal{Date: "2026-06-10", Code: "sh600000", SignalType: "test", Direction: "多头", Close: 100})

	if res.ExitDate != "2026-06-12" {
		t.Fatalf("ExitDate = %q, want 2026-06-12 (expiry)", res.ExitDate)
	}
	if res.ExitPrice != 103 || res.ReturnPct != 3 {
		t.Errorf("exit at close: price=%.2f ret=%.2f, want 103 / +3", res.ExitPrice, res.ReturnPct)
	}
	if res.Win != 1 {
		t.Errorf("Win = %d, want 1", res.Win)
	}
}
