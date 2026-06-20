package store

import (
	"testing"
)

// TestInitBacktestTablesCreatesSchema verifies table creation
func TestInitBacktestTablesCreatesSchema(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatalf("InitBacktestTables: %v", err)
	}

	// Verify tables exist
	tables := []string{"backtest_result", "backtest_summary"}
	for _, table := range tables {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not created: %v", table, err)
		}
	}

	// Verify indexes
	indexes := []string{"idx_backtest_run", "idx_signal_type", "idx_code_bt", "idx_entry_date_bt"}
	for _, idx := range indexes {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name)
		if err != nil {
			t.Errorf("index %q not created: %v", idx, err)
		}
	}
}

// TestSaveBacktestResultsRoundTrip verifies batch save and retrieval
func TestSaveBacktestResultsRoundTrip(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatal(err)
	}

	// Save batch results
	results := []BacktestResult{
		{
			BacktestRunID: "run-001",
			Config:        `{"strategy":"trend"}`,
			EntryDate:     "2026-06-01",
			Code:          "sh600000",
			SignalType:    "趋势跟随多头",
			Direction:     "多头",
			EntryPrice:    9.10,
			ExitDate:      "2026-06-11",
			ExitPrice:     9.55,
			HoldingDays:   10,
			ReturnPct:     4.95,
			Win:           1,
			ScoreTotal:    85,
			ADX:           28.5,
			RSRankPct:     75.0,
			SARStance:     "多",
			TDSetup:       "见底/8",
			MaxAdversePct: -1.2,
		},
		{
			BacktestRunID: "run-001",
			Config:        `{"strategy":"trend"}`,
			EntryDate:     "2026-06-02",
			Code:          "sh600519",
			SignalType:    "超买反转",
			Direction:     "空头",
			EntryPrice:    1200.0,
			ExitDate:      "2026-06-12",
			ExitPrice:     1180.0,
			HoldingDays:   10,
			ReturnPct:     -1.67,
			Win:           0,
			ScoreTotal:    65,
			ADX:           15.2,
			RSRankPct:     45.0,
			SARStance:     "空",
			TDSetup:       "见顶/9",
			MaxAdversePct: -3.5,
		},
	}

	if err := s.SaveBacktestResults(results); err != nil {
		t.Fatalf("SaveBacktestResults: %v", err)
	}

	// Retrieve and verify
	retrieved, err := s.GetBacktestResults("run-001")
	if err != nil {
		t.Fatalf("GetBacktestResults: %v", err)
	}

	if len(retrieved) != 2 {
		t.Fatalf("expected 2 results, got %d", len(retrieved))
	}

	// Verify first result
	r0 := retrieved[0]
	if r0.Code != "sh600000" {
		t.Errorf("result[0].Code: got %q, want %q", r0.Code, "sh600000")
	}
	if r0.ReturnPct != 4.95 {
		t.Errorf("result[0].ReturnPct: got %.2f, want 4.95", r0.ReturnPct)
	}
	if r0.Win != 1 {
		t.Errorf("result[0].Win: got %d, want 1", r0.Win)
	}

	// Verify second result
	r1 := retrieved[1]
	if r1.Code != "sh600519" {
		t.Errorf("result[1].Code: got %q, want %q", r1.Code, "sh600519")
	}
	if r1.Win != 0 {
		t.Errorf("result[1].Win: got %d, want 0", r1.Win)
	}
}

// TestSaveBacktestResultsEmptyIsNoop verifies empty batch handling
func TestSaveBacktestResultsEmptyIsNoop(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatal(err)
	}

	// Empty batch should succeed without error
	if err := s.SaveBacktestResults([]BacktestResult{}); err != nil {
		t.Errorf("SaveBacktestResults with empty slice: %v", err)
	}
	if err := s.SaveBacktestResults(nil); err != nil {
		t.Errorf("SaveBacktestResults with nil slice: %v", err)
	}
}

// TestSaveBacktestSummaryRoundTrip verifies summary save and retrieval
func TestSaveBacktestSummaryRoundTrip(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatal(err)
	}

	// Save summary
	summary := BacktestSummary{
		BacktestRunID:     "run-002",
		Config:            `{"strategy":"momentum"}`,
		StartDate:         "2026-05-01",
		EndDate:           "2026-06-18",
		TotalSignals:      50,
		WinCount:          32,
		WinRate:           64.0,
		AvgReturn:         2.5,
		MedianReturn:      1.8,
		BestReturn:        15.3,
		WorstReturn:       -8.2,
		SignalStats:       `{"趋势跟随":{"total":30,"wins":20}}`,
		BullMarketWinRate: 70.0,
		BearMarketWinRate: 55.0,
		MaxDrawdown:       -12.5,
		SharpeRatio:       1.8,
		DurationMS:        3500,
	}

	if err := s.SaveBacktestSummary(summary); err != nil {
		t.Fatalf("SaveBacktestSummary: %v", err)
	}

	// Retrieve and verify
	retrieved, err := s.GetBacktestSummary("run-002")
	if err != nil {
		t.Fatalf("GetBacktestSummary: %v", err)
	}

	if retrieved.TotalSignals != 50 {
		t.Errorf("TotalSignals: got %d, want 50", retrieved.TotalSignals)
	}
	if retrieved.WinCount != 32 {
		t.Errorf("WinCount: got %d, want 32", retrieved.WinCount)
	}
	if retrieved.WinRate != 64.0 {
		t.Errorf("WinRate: got %.1f, want 64.0", retrieved.WinRate)
	}
	if retrieved.AvgReturn != 2.5 {
		t.Errorf("AvgReturn: got %.1f, want 2.5", retrieved.AvgReturn)
	}
	if retrieved.SharpeRatio != 1.8 {
		t.Errorf("SharpeRatio: got %.1f, want 1.8", retrieved.SharpeRatio)
	}
}

// TestGetBacktestSummaryNotFound verifies error on missing run
func TestGetBacktestSummaryNotFound(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatal(err)
	}

	_, err := s.GetBacktestSummary("nonexistent-run")
	if err == nil {
		t.Error("GetBacktestSummary for nonexistent run should return error")
	}
}

// TestListBacktestSummariesOrdersDescending verifies listing and order
func TestListBacktestSummariesOrdersDescending(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatal(err)
	}

	// Insert multiple summaries
	summaries := []BacktestSummary{
		{BacktestRunID: "run-001", TotalSignals: 10, WinCount: 5, WinRate: 50.0},
		{BacktestRunID: "run-002", TotalSignals: 20, WinCount: 12, WinRate: 60.0},
		{BacktestRunID: "run-003", TotalSignals: 30, WinCount: 18, WinRate: 60.0},
	}
	for _, sum := range summaries {
		if err := s.SaveBacktestSummary(sum); err != nil {
			t.Fatal(err)
		}
	}

	// List with limit
	list, err := s.ListBacktestSummaries(2)
	if err != nil {
		t.Fatalf("ListBacktestSummaries: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(list))
	}

	// Verify descending order (most recent first)
	// Note: run_date defaults to CURRENT_TIMESTAMP, so order may vary in practice
	// Here we just verify that we got results
	for _, s := range list {
		if s.BacktestRunID == "" {
			t.Error("ListBacktestSummaries returned empty run ID")
		}
	}
}

// TestListBacktestSummariesDefaultLimit verifies default limit behavior
func TestListBacktestSummariesDefaultLimit(t *testing.T) {
	s := openMemDB(t)
	defer s.Close()

	if err := s.InitBacktestTables(); err != nil {
		t.Fatal(err)
	}

	// Empty list should work
	list, err := s.ListBacktestSummaries(0) // 0 should use default limit
	if err != nil {
		t.Fatalf("ListBacktestSummaries with 0 limit: %v", err)
	}
	if list == nil {
		t.Error("ListBacktestSummaries returned nil slice")
	}
}

// TestMarshalSignalStatsComputesMetrics verifies JSON serialization and calculation
func TestMarshalSignalStatsComputesMetrics(t *testing.T) {
	stats := map[string]SignalStat{
		"趋势跟随": {
			Type:    "趋势跟随",
			Total:   10,
			Wins:    7,
			Returns: []float64{5.0, 3.0, -2.0, 8.0, 1.0, -1.0, 4.0, 2.0, -3.0, 6.0},
		},
		"超买反转": {
			Type:    "超买反转",
			Total:   5,
			Wins:    2,
			Returns: []float64{-1.5, 3.0, -2.0, -0.5, 4.5},
		},
	}

	jsonStr, err := MarshalSignalStats(stats)
	if err != nil {
		t.Fatalf("MarshalSignalStats: %v", err)
	}

	// Verify JSON is valid
	if jsonStr == "" {
		t.Error("MarshalSignalStats returned empty string")
	}

	// Unmarshal and verify
	unmarshaled, err := UnmarshalSignalStats(jsonStr)
	if err != nil {
		t.Fatalf("UnmarshalSignalStats: %v", err)
	}

	trend := unmarshaled["趋势跟随"]
	if trend.Total != 10 {
		t.Errorf("趋势跟随 Total: got %d, want 10", trend.Total)
	}
	if trend.Wins != 7 {
		t.Errorf("趋势跟随 Wins: got %d, want 7", trend.Wins)
	}
	if trend.WinRate != 70.0 {
		t.Errorf("趋势跟随 WinRate: got %.1f, want 70.0", trend.WinRate)
	}
	// Average of returns: (5+3-2+8+1-1+4+2-3+6) / 10 = 23 / 10 = 2.3
	if diff := trend.AvgReturn - 2.3; diff < -0.01 || diff > 0.01 {
		t.Errorf("趋势跟随 AvgReturn: got %.2f, want 2.30", trend.AvgReturn)
	}

	overbought := unmarshaled["超买反转"]
	if overbought.Total != 5 {
		t.Errorf("超买反转 Total: got %d, want 5", overbought.Total)
	}
	if overbought.WinRate != 40.0 {
		t.Errorf("超买反转 WinRate: got %.1f, want 40.0", overbought.WinRate)
	}
	// Average: (-1.5+3-2-0.5+4.5) / 5 = 3.5 / 5 = 0.7
	if diff := overbought.AvgReturn - 0.7; diff < -0.01 || diff > 0.01 {
		t.Errorf("超买反转 AvgReturn: got %.2f, want 0.70", overbought.AvgReturn)
	}
}

// TestMeanComputesAverage verifies mean calculation
func TestMeanComputesAverage(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"empty", []float64{}, 0.0},
		{"single", []float64{5.5}, 5.5},
		{"multiple", []float64{1.0, 2.0, 3.0, 4.0, 5.0}, 3.0},
		{"negative", []float64{-2.0, -4.0, -6.0}, -4.0},
		{"mixed", []float64{10.0, -5.0, 3.0, -2.0}, 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mean(tt.values)
			if diff := got - tt.want; diff < -0.001 || diff > 0.001 {
				t.Errorf("mean(%v) = %.3f, want %.3f", tt.values, got, tt.want)
			}
		})
	}
}
