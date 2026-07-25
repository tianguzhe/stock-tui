package backtest

import (
	"math"
	"testing"
)

func TestCalculateStatsSharpeZeroStdDevNoInfOrNaN(t *testing.T) {
	e := &PortfolioEngine{}
	trades := []Trade{
		{ReturnPct: 5.0, PnL: 100},
		{ReturnPct: 5.0, PnL: 100},
	}

	stats := e.calculateStats(trades, nil)

	if math.IsInf(stats.SharpeRatio, 0) || math.IsNaN(stats.SharpeRatio) {
		t.Fatalf("SharpeRatio = %v, want finite value when all returns are identical (stdDev=0)", stats.SharpeRatio)
	}
	if stats.SharpeRatio != 0 {
		t.Errorf("SharpeRatio = %v, want 0 when stdDev=0 (undefined ratio left at zero-value)", stats.SharpeRatio)
	}
}
