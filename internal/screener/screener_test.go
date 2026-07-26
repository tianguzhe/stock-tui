package screener

import (
	"database/sql"
	"math"
	"testing"
)

// baseCandidate returns a candidate that passes all filters (⭐⭐⭐).
func baseCandidate() Candidate {
	return Candidate{
		Code:            "sh600000",
		Name:            "测试股份",
		HotScore:        5,
		ScoreTotal:      72,
		ADX:             42.0,
		ChangePct:       1.5,
		Close:           10.0,
		RS20:            sql.NullFloat64{Float64: 85, Valid: true},
		RS60:            sql.NullFloat64{Float64: 85, Valid: true},
		RS120:           sql.NullFloat64{Float64: 85, Valid: true},
		Bias24:          5.0,
		ATRPct:          5.0,
		Streak:          2,
		MA20:            9.0,
		SARLong:         true,
		SuperTrendLong:  true,
		OBVUp:           true,
		OBVUp3Day:       true,
		MACDHist:        1.0,
		DivBear:         false,
		SigOverbought:   false,
		TDSetup:         "见顶/3",
		TDCountdown:     "-/0",
		VolRatio:        1.0,
		TurnoverRate:    10.0,
		MarketCap:       300,
		PE:              15.0,
		KeltnerSqueeze:  false,
		SARValue:        sql.NullFloat64{Float64: 9.5, Valid: true},
		SuperTrendValue: sql.NullFloat64{Float64: 9.3, Valid: true},
	}
}

// TestPerfFiltering tests PERF historical win rate filtering.
func TestPerfFiltering(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Candidate)
		want bool
	}{
		{
			name: "trend follow significantly bad - exclude",
			mod: func(c *Candidate) {
				c.PerfTrendFollowBullWin10 = sql.NullFloat64{Float64: 20, Valid: true}
				c.PerfTrendFollowBullN = sql.NullInt64{Int64: 100, Valid: true}
			},
			want: false,
		},
		{
			name: "trend follow small sample - pass",
			mod: func(c *Candidate) {
				c.PerfTrendFollowBullWin10 = sql.NullFloat64{Float64: 20, Valid: true}
				c.PerfTrendFollowBullN = sql.NullInt64{Int64: 5, Valid: true}
			},
			want: true,
		},
		{
			name: "trend follow avg not profitable - exclude",
			mod: func(c *Candidate) {
				c.PerfTrendFollowBullWin10 = sql.NullFloat64{Float64: 55, Valid: true}
				c.PerfTrendFollowBullN = sql.NullInt64{Int64: 20, Valid: true}
				c.PerfTrendFollowBullAvg10 = sql.NullFloat64{Float64: 0, Valid: true}
			},
			want: false,
		},
		{
			name: "overbought significantly effective - exclude",
			mod: func(c *Candidate) {
				c.SigOverbought = true
				c.PerfOverboughtBearWin10 = sql.NullFloat64{Float64: 65, Valid: true}
				c.PerfOverboughtBearN = sql.NullInt64{Int64: 100, Valid: true}
			},
			want: false,
		},
		{
			name: "overbought not significant - pass",
			mod: func(c *Candidate) {
				c.SigOverbought = true
				c.PerfOverboughtBearWin10 = sql.NullFloat64{Float64: 58.3, Valid: true}
				c.PerfOverboughtBearN = sql.NullInt64{Int64: 48, Valid: true}
			},
			want: true,
		},
		{
			name: "overbought not triggered - pass",
			mod: func(c *Candidate) {
				c.SigOverbought = false
				c.PerfOverboughtBearWin10 = sql.NullFloat64{Float64: 70, Valid: true}
				c.PerfOverboughtBearN = sql.NullInt64{Int64: 100, Valid: true}
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCandidate()
			tt.mod(&c)
			got := perfOK(&c)
			if got != tt.want {
				t.Errorf("perfOK() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDivBearState tests divergence bearish state logic.
func TestDivBearState(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Candidate)
		want string
	}{
		{
			name: "no divergence - ok",
			mod:  func(c *Candidate) { c.DivBear = false },
			want: "ok",
		},
		{
			name: "divergence no sample - watch",
			mod: func(c *Candidate) {
				c.DivBear = true
				c.PerfDivBearWin10 = sql.NullFloat64{Valid: false}
				c.PerfDivBearN = sql.NullInt64{Valid: false}
			},
			want: "watch",
		},
		{
			name: "divergence significantly effective - exclude",
			mod: func(c *Candidate) {
				c.DivBear = true
				c.PerfDivBearWin10 = sql.NullFloat64{Float64: 68, Valid: true}
				c.PerfDivBearN = sql.NullInt64{Int64: 100, Valid: true}
			},
			want: "exclude",
		},
		{
			name: "divergence not significant strong trend - ok",
			mod: func(c *Candidate) {
				c.DivBear = true
				c.PerfDivBearWin10 = sql.NullFloat64{Float64: 60, Valid: true}
				c.PerfDivBearN = sql.NullInt64{Int64: 50, Valid: true}
				c.ADX = 42
				c.SARLong = true
				c.SuperTrendLong = true
			},
			want: "ok",
		},
		{
			name: "divergence not significant weak trend - watch",
			mod: func(c *Candidate) {
				c.DivBear = true
				c.PerfDivBearWin10 = sql.NullFloat64{Float64: 45, Valid: true}
				c.PerfDivBearN = sql.NullInt64{Int64: 30, Valid: true}
				c.ADX = 36
			},
			want: "watch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCandidate()
			tt.mod(&c)
			got := divBearState(&c)
			if got != tt.want {
				t.Errorf("divBearState() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLateStageRisk tests late-stage chase risk detection.
func TestLateStageRisk(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Candidate)
		want bool
	}{
		{
			name: "high bias/atr ratio",
			mod: func(c *Candidate) {
				c.Bias24 = 10.0
				c.ATRPct = 2.0 // 10/2 = 5 > 4
			},
			want: true,
		},
		{
			name: "streak >= 5",
			mod:  func(c *Candidate) { c.Streak = 5 },
			want: true,
		},
		{
			name: "turnover 15-20%",
			mod:  func(c *Candidate) { c.TurnoverRate = 17.0 },
			want: true,
		},
		{
			// 15.0% is the lower edge of the "15-20%" 末端降级 band — closed interval,
			// consistent with streak >= 5 above. Guards against regressing to tr > 15.
			name: "turnover 边界 15.0% 触发(闭区间)",
			mod:  func(c *Candidate) { c.TurnoverRate = 15.0 },
			want: true,
		},
		{
			name: "large gain + volume spike",
			mod: func(c *Candidate) {
				c.ChangePct = 5.5
				c.VolRatio = 1.6
			},
			want: true,
		},
		{
			name: "gain + divergence",
			mod: func(c *Candidate) {
				c.ChangePct = 3.5
				c.DivBear = true
			},
			want: true,
		},
		{
			name: "TD top count >= 5 + divergence",
			mod: func(c *Candidate) {
				c.TDSetup = "见顶/8"
				c.DivBear = true
			},
			want: true,
		},
		{
			name: "TD top count >= 5 without divergence — not risky alone",
			mod:  func(c *Candidate) { c.TDSetup = "见顶/8" },
			want: false,
		},
		{
			name: "normal - no risk",
			mod:  func(c *Candidate) {},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCandidate()
			tt.mod(&c)
			got := lateStageRisk(&c)
			if got != tt.want {
				t.Errorf("lateStageRisk() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestComputeTier tests tier assignment logic.
func TestComputeTier(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Candidate)
		want Tier
	}{
		{
			name: "perfect star3",
			mod: func(c *Candidate) {
				c.ChangePct = 2.8
				c.ScoreTotal = 72
				c.ADX = 46.7
				c.RS20 = sql.NullFloat64{Float64: 94, Valid: true}
			},
			want: TierStar3,
		},
		{
			name: "late stage demotion to watch",
			mod: func(c *Candidate) {
				c.Bias24 = 10.0
				c.ATRPct = 2.0
			},
			want: TierWatch,
		},
		{
			name: "strong stock pullback watch",
			mod: func(c *Candidate) {
				c.ChangePct = -4.5
				c.RS20 = sql.NullFloat64{Float64: 92, Valid: true}
				c.VolRatio = 1.0
			},
			want: TierWatch,
		},
		{
			name: "market cap too low - exclude",
			mod:  func(c *Candidate) { c.MarketCap = 15 },
			want: TierNone,
		},
		{
			name: "ST 风险警示股一律排除",
			mod:  func(c *Candidate) { c.Code, c.Name = "sz002759", "ST天际" },
			want: TierNone,
		},
		{
			name: "*ST 同样排除",
			mod:  func(c *Candidate) { c.Code, c.Name = "sz002731", "*ST萃华" },
			want: TierNone,
		},
		// 主板 10% 涨跌停: 9.5 起视为接近涨停
		{
			name: "主板涨 9.6% 视为涨停排除",
			mod:  func(c *Candidate) { c.ChangePct = 9.6 },
			want: TierNone,
		},
		// 创业板 20% 涨跌停: 12% 是普通波动,旧的固定 ±9.5 会误杀
		{
			name: "创业板涨 12% 属正常波动不排除",
			mod: func(c *Candidate) {
				c.Code, c.Name = "sz300750", "宁德时代"
				c.ChangePct = 12.0
			},
			want: TierStar3,
		},
		{
			name: "创业板涨 19.6% 才算涨停排除",
			mod: func(c *Candidate) {
				c.Code, c.Name = "sz300750", "宁德时代"
				c.ChangePct = 19.6
			},
			want: TierNone,
		},
		{
			name: "北交所涨 25% 不算涨停",
			mod: func(c *Candidate) {
				c.Code, c.Name = "bj920819", "颖泰生物"
				c.ChangePct = 25.0
			},
			want: TierStar3,
		},
		// 港股无涨跌停,闸门整体跳过——不能拿任何百分比去卡它
		{
			name: "港股涨 15% 不触发涨停闸门",
			mod: func(c *Candidate) {
				c.Code, c.Name = "hk00700", "腾讯控股"
				c.ChangePct = 15.0
			},
			want: TierStar3,
		},
		{
			name: "RS20 too low - exclude",
			mod:  func(c *Candidate) { c.RS20 = sql.NullFloat64{Float64: 55, Valid: true} },
			want: TierNone,
		},
		{
			name: "TD setup 见顶/6 alone — still passes to star tier",
			mod:  func(c *Candidate) { c.TDSetup = "见顶/6" },
			want: TierStar3,
		},
		{
			// TD Sequential is not a coreTech hard gate (CLAUDE.md) — 见顶/8 alone,
			// without divBear, must not exclude. Its only screener role is the
			// tdTop>=5 && divBear joint condition inside lateStageRisk (see
			// TestLateStageRisk for that branch in isolation).
			name: "TD setup 见顶/8 alone — still passes to star tier",
			mod:  func(c *Candidate) { c.TDSetup = "见顶/8" },
			want: TierStar3,
		},
		{
			name: "large drop - exclude",
			mod: func(c *Candidate) {
				c.ChangePct = -3.5
				c.RS20 = sql.NullFloat64{Float64: 79, Valid: true}
			},
			want: TierNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCandidate()
			tt.mod(&c)
			got := ComputeTier(&c)
			if got != tt.want {
				t.Errorf("ComputeTier() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMarketBreadth tests market breadth calculation.
func TestMarketBreadth(t *testing.T) {
	tests := []struct {
		name       string
		candidates []Candidate
		want       float64
	}{
		{
			name: "30% above MA20",
			candidates: []Candidate{
				{Close: 10, MA20: 9},
				{Close: 10, MA20: 9},
				{Close: 10, MA20: 9},
				{Close: 8, MA20: 9},
				{Close: 8, MA20: 9},
				{Close: 8, MA20: 9},
				{Close: 8, MA20: 9},
				{Close: 8, MA20: 9},
				{Close: 8, MA20: 9},
				{Close: 8, MA20: 9},
			},
			want: 30.0,
		},
		{
			// 广度未知时必须向保护侧失败: 返回 100 会在信息最少时
			// 悄悄关掉推荐数减半的风控闸门。
			name:       "无可测样本时返回 0 以触发风控",
			candidates: []Candidate{},
			want:       0.0,
		},
		{
			name:       "有候选但缺 close/MA20 同样视为未知",
			candidates: []Candidate{{Close: 0, MA20: 0}, {Close: 10, MA20: 0}},
			want:       0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarketBreadth(tt.candidates)
			if math.Abs(got-tt.want) > 0.1 {
				t.Errorf("MarketBreadth() = %.1f, want %.1f", got, tt.want)
			}
		})
	}
}

// TestSortKey tests sorting key calculation.
func TestSortKey(t *testing.T) {
	c1 := baseCandidate()
	c1.Code = "A"
	c1.RS20 = sql.NullFloat64{Float64: 90, Valid: true}
	c1.RS60 = sql.NullFloat64{Float64: 85, Valid: true}
	c1.RS120 = sql.NullFloat64{Float64: 80, Valid: true}
	c1.ScoreTotal = 75
	c1.ADX = 40
	c1.Tier = TierStar3

	c2 := baseCandidate()
	c2.Code = "B"
	c2.RS20 = sql.NullFloat64{Float64: 85, Valid: true}
	c2.RS60 = sql.NullFloat64{Float64: 90, Valid: true}
	c2.RS120 = sql.NullFloat64{Float64: 85, Valid: true}
	c2.ScoreTotal = 80
	c2.ADX = 45
	c2.Tier = TierStar3

	c3 := baseCandidate()
	c3.Code = "C"
	c3.RS20 = sql.NullFloat64{Float64: 95, Valid: true}
	c3.RS60 = sql.NullFloat64{Float64: 80, Valid: true}
	c3.RS120 = sql.NullFloat64{Float64: 75, Valid: true}
	c3.ScoreTotal = 70
	c3.ADX = 35
	c3.Tier = TierStar3

	// RS composite: A=0.3*90+0.5*85+0.2*80=85.5
	//                B=0.3*85+0.5*90+0.2*85=87.5
	//                C=0.3*95+0.5*80+0.2*75=83.5
	// Expected order: B > A > C
	k1 := SortKey(&c1)
	k2 := SortKey(&c2)
	k3 := SortKey(&c3)

	if k2 <= k1 || k1 <= k3 {
		t.Errorf("SortKey order incorrect: A=%.2f, B=%.2f, C=%.2f, want B>A>C", k1, k2, k3)
	}
}

// TestStopText tests stop-loss text formatting.
func TestStopText(t *testing.T) {
	tests := []struct {
		name string
		c    Candidate
		want string
	}{
		{
			name: "normal stop",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 9.5, Valid: true},
			},
			want: "9.50(-5.0%)",
		},
		{
			name: "missing sar value",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Valid: false},
			},
			want: "—",
		},
		// SAR above close (bearish stance / SAR 反手线) must not be shown as
		// a stop — it is the short-cover line, meaningless for an open long.
		// Fall back to the 20-day low (跌破前低就跑 价格行为止损).
		{
			name: "sar above close falls back to low20",
			c: Candidate{
				Close:    9.43,
				SARValue: sql.NullFloat64{Float64: 12.11, Valid: true},
				Low20:    9.10, // 近20日低点,在现价下方
			},
			want: "9.10(-3.5%)", // 回退 Low20,距现价 -3.5%
		},
		{
			name: "sar above close and no low20 fallback",
			c: Candidate{
				Close:    9.43,
				SARValue: sql.NullFloat64{Float64: 12.11, Valid: true},
				Low20:    0, // 无历史低点数据
			},
			want: "—",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StopText(&tt.c)
			if got != tt.want {
				t.Errorf("StopText() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPositionHint tests position size hint calculation.
func TestPositionHint(t *testing.T) {
	tests := []struct {
		name    string
		c       Candidate
		capital float64
		want    string
	}{
		{
			name: "normal position",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 9.5, Valid: true},
			},
			capital: 68000,
			want:    "建议≤1300股",
		},
		{
			name: "no capital",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 9.5, Valid: true},
			},
			capital: 0,
			want:    "",
		},
		{
			name: "stop above price",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 10.5, Valid: true},
				Low20:    0, // SAR 失效且无 Low20 回退
			},
			capital: 68000,
			want:    "止损距离过宽，建议观望",
		},
		// SAR above close now falls back to Low20: riskPerShare = Close - Low20.
		{
			name: "sar above price uses low20 fallback",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 10.5, Valid: true},
				Low20:    9.5, // riskPerShare = 10.0 - 9.5 = 0.5
			},
			capital: 68000,
			// shares = floor(68000*0.01/0.5/100)*100 = floor(13.6/100)*100... see实现
			want: "建议≤1300股",
		},
		{
			name: "sar above price no low20 fallback",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 10.5, Valid: true},
				Low20:    0, // 无 Low20,无法算风险距离
			},
			capital: 68000,
			want:    "止损距离过宽，建议观望",
		},
		// 止损贴身(SAR 刚翻多时的常态)会让 1% 风险除出天量仓位。
		// 实测 2026-07-24 sh603927: close=12.80 SAR=12.795(距离 0.04%),
		// 旧实现建议 145200 股 ≈ 185.8 万元,而总资金只有 6.8 万。
		{
			name: "stop too tight is rejected, not sized",
			c: Candidate{
				Close:    12.80,
				SARValue: sql.NullFloat64{Float64: 12.795, Valid: true},
			},
			capital: 68000,
			want:    "止损贴身(0.04%)，等回踩确认",
		},
		// 1.5% < MinStopPct(2%): 仍拒绝——日内噪音就能打掉。
		{
			name: "stop below min distance rejected",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 9.85, Valid: true},
			},
			capital: 68000,
			want:    "止损贴身(1.50%)，等回踩确认",
		},
		// 恰好 2.0%: 边界通过。shares=int(680/0.2/100)*100=3400,
		// 金额 34000 = 总资金的 50%(MinStopPct 保证的上限)。
		{
			name: "stop exactly at min distance is accepted",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 9.8, Valid: true},
			},
			capital: 68000,
			want:    "建议≤3400股",
		},
		// Low20 回退路径同样受最小距离约束(SAR 翻空时的持仓行)。
		{
			name: "low20 fallback also honours min distance",
			c: Candidate{
				Close:    10.0,
				SARValue: sql.NullFloat64{Float64: 10.5, Valid: true},
				Low20:    9.9, // 仅 1% 距离
			},
			capital: 68000,
			want:    "止损贴身(1.00%)，等回踩确认",
		},
		// 资金不足以买满一手: 止损距离合规(20%),但 1% 风险只够 6 股 → 不足 100
		{
			name: "capital too small for one lot",
			c: Candidate{
				Close:    400.0,
				SARValue: sql.NullFloat64{Float64: 320.0, Valid: true},
			},
			capital: 5000, // 5000*0.01/80 = 0.625 股
			want:    "止损距离过宽，建议观望",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PositionHint(&tt.c, tt.capital)
			if got != tt.want {
				t.Errorf("PositionHint() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOBV3DayFilter tests OBV 3-day sustained inflow as a tier requirement.
//
// Design: 1-day OBV is the coreTech baseline (passes filter); 3-day sustained
// is required for star tiers (⭐⭐/⭐⭐⭐). 1-day-only demotes to 👁️观察.
// Backtest 2026-06: 1-day-only group 70.2% / +8.87% vs 3-day 82.6% / +21.02%.
// Keep 1-day-only as watch (visible, sortable) instead of hard-exclude.
func TestOBV3DayFilter(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Candidate)
		want Tier
	}{
		{
			name: "3-day sustained OBV - pass star3",
			mod: func(c *Candidate) {
				c.OBVUp = true
				c.OBVUp3Day = true
			},
			want: TierStar3,
		},
		{
			name: "single-day OBV - demote to watch",
			mod: func(c *Candidate) {
				c.OBVUp = true
				c.OBVUp3Day = false
			},
			want: TierWatch,
		},
		{
			name: "OBV outflow - exclude",
			mod: func(c *Candidate) {
				c.OBVUp = false
				c.OBVUp3Day = false
			},
			want: TierNone,
		},
		{
			name: "3-day OBV but red day pullback with strong RS - watch",
			mod: func(c *Candidate) {
				c.OBVUp = true
				c.OBVUp3Day = true
				c.ChangePct = -1.0
				c.ScoreTotal = 70
				c.ADX = 40
				c.RS20 = sql.NullFloat64{Float64: 88, Valid: true}
				c.VolRatio = 0.9
			},
			want: TierWatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := baseCandidate()
			tt.mod(&c)
			got := ComputeTier(&c)
			if got != tt.want {
				t.Errorf("ComputeTier() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTDFunctions tests TD Sequential helper functions (CdwnTopN).
func TestTDFunctions(t *testing.T) {
	t.Run("CdwnTopN", func(t *testing.T) {
		tests := []struct {
			countdown string
			want      int
		}{
			{"见顶/5", 5},
			{"见底/3", 0},
			{"-/0", 0},
			{"", 0},
		}
		for _, tt := range tests {
			got := CdwnTopN(tt.countdown)
			if got != tt.want {
				t.Errorf("CdwnTopN(%q) = %d, want %d", tt.countdown, got, tt.want)
			}
		}
	})
}
