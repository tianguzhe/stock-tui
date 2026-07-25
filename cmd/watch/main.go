// Command watch 盘中深度监控 — 实时行情 + SQLite 技术面快照 + K线量能
//
// 输出每只持仓的:
//   - 实时价格/涨跌/浮盈
//   - 趋势 stance (SAR/ST/ADX/CMI/CHOP)
//   - 动量 (KDJ/RSI/WR/MACD)
//   - 量能 (MFI/量比/vol_ratio_rt)
//   - 信号 (背离/超买超卖/突破)
//   - 价位 (BOLL/SAR/ST/乖离)
//   - 综合诊断
//
// Usage:
//
//	go run ./cmd/watch
package main

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/store"
)

// ── 类型定义 ──

type holding struct {
	Code     string
	Cost     float64
	Quantity int // 手数
}

// deepSnapshot 从 SQLite snapshot 表读取的完整技术面
type deepSnapshot struct {
	TradeDate                 string
	Close                     float64
	ScoreTotal                int
	ScoreDelta                int
	ScoreAdj                  int
	ScoreLabel                string
	KDJ_J, RSI6, WR14         float64
	ADX, CMI, CHOP            float64
	MACD_Dif, MACD_Hist       float64
	BOLL_PB, BOLL_BW          float64
	MFI                       float64
	SAR_Long, ST_Long         bool
	SigOB, SigOS, SigTrend    bool
	DivBull, DivBear          bool
	Squeeze, Donch20, Donch55 bool
	SAR_Value, ST_Value       float64
	Bias6, Bias24, ATR_Pct    float64
	Ret20, RS20               float64
	InsideVol, OutsideVol     float64
	Turnover                  float64
}

type klineVolResult struct {
	ratio    float64 // 今日量比
	label    string  // 描述
	todayVol float64 // 今日成交量(股)
	avgVol   float64 // 20日均量(股)
	recent   []struct {
		date  string
		vol   float64
		ratio float64
	}
}

type alert struct {
	Code     string
	Name     string
	Icon     string
	Desc     string
	Pct      float64
	Severity int // 3=critical 2=major 1=minor
	Reason   string
}

type stockReport struct {
	api  api.Stock
	snap *deepSnapshot
	vol  *klineVolResult
	h    holding
}

// ── 主入口 ──

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. 读取持仓
	holdings, err := parseHoldings()
	if err != nil {
		return err
	}
	if len(holdings) == 0 {
		fmt.Println("⚠️ 无可监控的持仓")
		return nil
	}

	// 2. 启动 SQLite
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 3. 实时行情
	codes := make([]string, len(holdings))
	for i, h := range holdings {
		codes[i] = h.Code
	}
	stocks, err := api.FetchStocks(codes)
	if err != nil {
		return fmt.Errorf("获取实时行情失败: %w", err)
	}

	// 4. K线量能分析 (用于量比对比)
	client := &http.Client{Timeout: 8 * time.Second}
	klineCache := make(map[string]*klineVolResult)
	for _, h := range holdings {
		v, err := calcVolume(client, h.Code)
		if err == nil {
			klineCache[h.Code] = v
		}
	}

	// 5. 拉取 SQLite 技术面快照
	snapCache := make(map[string]*deepSnapshot)
	for _, h := range holdings {
		s, err := queryDeepSnapshot(st.DB(), h.Code)
		if err == nil {
			snapCache[h.Code] = s
		}
	}

	// 6. 构建输出
	now := time.Now()
	bjt := now.Add(15 * time.Hour) // PDT → BJT
	fmt.Printf("📡 盘中深度监控 · %s BJT\n", bjt.Format("15:04:05"))
	fmt.Println(strings.Repeat("━", 54))

	// code→holding map
	hMap := make(map[string]holding)
	for _, h := range holdings {
		hMap[h.Code] = h
	}

	var alerts []alert
	var reports []stockReport

	for _, s := range stocks {
		h, ok := hMap[s.Code]
		if !ok {
			continue
		}
		snap := snapCache[s.Code]
		vol := klineCache[s.Code]

		// ── 异动检测 ──
		pct := s.ChangePct

		// 涨跌幅异动
		switch {
		case pct <= -9.0:
			alerts = append(alerts, alert{s.Code, s.Name, "💥", "跌停封死", pct, 3, ""})
		case pct <= -7.0:
			alerts = append(alerts, alert{s.Code, s.Name, "💥", "逼近跌停", pct, 3, ""})
		case pct <= -5.0:
			alerts = append(alerts, alert{s.Code, s.Name, "🔻", "大跌", pct, 2, ""})
		case pct <= -3.0:
			alerts = append(alerts, alert{s.Code, s.Name, "⬇️", "明显下跌", pct, 1, ""})
		case pct >= 9.0:
			alerts = append(alerts, alert{s.Code, s.Name, "🚀", "涨停", pct, 3, ""})
		case pct >= 7.0:
			alerts = append(alerts, alert{s.Code, s.Name, "🚀", "逼近涨停", pct, 3, ""})
		case pct >= 5.0:
			alerts = append(alerts, alert{s.Code, s.Name, "⬆️", "大涨", pct, 2, ""})
		case pct >= 3.0:
			alerts = append(alerts, alert{s.Code, s.Name, "⬆️", "明显拉升", pct, 1, ""})
		}

		// 量比异动
		if vol != nil {
			switch {
			case vol.ratio > 3.0:
				alerts = append(alerts, alert{s.Code, s.Name, "🔥", "爆量", pct, 2,
					fmt.Sprintf("量比%.1f", vol.ratio)})
			case vol.ratio > 2.0:
				alerts = append(alerts, alert{s.Code, s.Name, "🔴", "放量", pct, 1,
					fmt.Sprintf("量比%.1f", vol.ratio)})
			case vol.ratio < 0.3 && pct < 0:
				alerts = append(alerts, alert{s.Code, s.Name, "🟢", "地量下跌", pct, 1,
					fmt.Sprintf("量比%.1f惜售", vol.ratio)})
			}
		}

		reports = append(reports, stockReport{api: s, snap: snap, vol: vol, h: h})
	}

	// 按严重度排序
	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].Severity != alerts[j].Severity {
			return alerts[i].Severity > alerts[j].Severity
		}
		return math.Abs(alerts[i].Pct) > math.Abs(alerts[j].Pct)
	})

	// ── 输出预警 ──
	if len(alerts) > 0 {
		fmt.Println()
		fmt.Printf("🚨 异动预警 (%d条)\n", len(alerts))
		seen := make(map[string]bool)
		for _, a := range alerts {
			key := a.Code + a.Desc
			if seen[key] {
				continue
			}
			seen[key] = true
			line := fmt.Sprintf("  %s %s %s  %+.2f%%", a.Icon, a.Code, a.Name, a.Pct)
			if a.Reason != "" {
				line += "  " + a.Reason
			}
			switch {
			case a.Severity >= 3:
				fmt.Println("\033[1;31m" + line + "\033[0m")
			case a.Severity >= 2:
				fmt.Println("\033[31m" + line + "\033[0m")
			default:
				fmt.Println(line)
			}
		}
	}

	// ── 逐标深度分析 ──
	for _, r := range reports {
		fmt.Println()
		fmt.Println(strings.Repeat("━", 54))
		printDeepCard(r)
	}

	// ── 汇总 ──
	fmt.Println(strings.Repeat("━", 54))
	totalFPL := 0.0
	up, down, flat := 0, 0, 0
	for _, r := range reports {
		p := r.api.ChangePct
		switch {
		case p > 0.5:
			up++
		case p < -0.5:
			down++
		default:
			flat++
		}
		totalFPL += (r.api.Price - r.h.Cost) * float64(r.h.Quantity) * 100
	}
	fplColor := "32"
	if totalFPL >= 0 {
		fplColor = "31"
	}
	fmt.Printf("总浮盈: \033[%sm%+.0f\033[0m | 涨%d/跌%d/平%d | 异动%d条\n",
		fplColor, totalFPL, up, down, flat, len(alerts))
	fmt.Println()

	return nil
}

// ── 深度展示 ──

func printDeepCard(r stockReport) {
	s := r.api
	h := r.h
	snap := r.snap
	vol := r.vol

	fpl := (s.Price - h.Cost) * float64(h.Quantity) * 100

	// 涨跌色
	pctStr := fmt.Sprintf("%+.2f%%", s.ChangePct)
	if s.ChangePct > 0 {
		pctStr = "\033[31m" + pctStr + "\033[0m"
	} else if s.ChangePct < 0 {
		pctStr = "\033[32m" + pctStr + "\033[0m"
	}

	// ── 第1行: 标题 ──
	fmt.Printf("【%s】%s\n", s.Code, s.Name)
	fmt.Printf("  实时  %.3f  %s  成交额%.0f万  换手%.2f%%  总市值%.0f亿\n",
		s.Price, pctStr, s.Amount, s.TurnoverRate, s.MarketCap)

	// 仓位
	fplColor := "32"
	if fpl >= 0 {
		fplColor = "31"
	}
	fmt.Printf("  仓位  成本%.3f ×%d手  浮盈\033[%sm%+.0f\033[0m  (距成本%+.2f%%)\n",
		h.Cost, h.Quantity, fplColor, fpl, (s.Price-h.Cost)/h.Cost*100)

	if snap == nil {
		fmt.Printf("  技术面: 暂无昨日快照\n")
		return
	}

	// ── 第2行: 趋势 ──
	sarS := "多"
	if !snap.SAR_Long {
		sarS = "空"
	}
	stS := "多"
	if !snap.ST_Long {
		stS = "空"
	}
	trendTag := ""
	switch {
	case snap.SAR_Long && snap.ST_Long:
		trendTag = "✅"
	case !snap.SAR_Long && !snap.ST_Long:
		trendTag = "⚠️双空"
	case snap.SAR_Long && !snap.ST_Long:
		trendTag = "⚡SAR多ST空"
	default:
		trendTag = "⚡SAR空ST多"
	}

	adxL := "弱向"
	switch {
	case snap.ADX > 40:
		adxL = "极强趋势"
	case snap.ADX > 25:
		adxL = "强趋势"
	case snap.ADX > 20:
		adxL = "趋势酝酿"
	}

	cmiL := "震荡"
	if snap.CMI > 50 {
		cmiL = "趋势高效"
	} else if snap.CMI > 30 {
		cmiL = "偏趋势"
	}

	chopL := "震荡"
	switch {
	case snap.CHOP < 30:
		chopL = "趋势极强"
	case snap.CHOP < 40:
		chopL = "强趋势"
	case snap.CHOP > 60:
		chopL = "高震荡"
	}

	fmt.Printf("  趋势  SAR%s·ST%s | ADX %.1f(%s) | CMI %.0f(%s) | CHOP %.0f(%s) %s\n",
		sarS, stS, snap.ADX, adxL, snap.CMI, cmiL, snap.CHOP, chopL, trendTag)

	// ── 第3行: 动量 ──
	rsiL := ""
	switch {
	case snap.RSI6 < 20:
		rsiL = "极端超卖"
	case snap.RSI6 < 30:
		rsiL = "超卖"
	case snap.RSI6 > 80:
		rsiL = "极端超买"
	case snap.RSI6 > 70:
		rsiL = "超买"
	}
	wrL := ""
	switch {
	case snap.WR14 >= 80:
		wrL = "超卖"
	case snap.WR14 >= 60:
		wrL = "偏卖"
	case snap.WR14 <= 20:
		wrL = "超买"
	case snap.WR14 <= 40:
		wrL = "偏买"
	}

	macdM := "↘"
	if snap.MACD_Hist > 0 {
		macdM = "↗"
	}

	fmt.Printf("  动量  KDJ-J %.1f | RSI6 %.1f(%s) | WR %.0f(%s) | MACD柱%.3f%s\n",
		snap.KDJ_J, snap.RSI6, rsiL, snap.WR14, wrL, snap.MACD_Hist, macdM)

	// ── 第4行: 量能 ──
	mfiL := ""
	switch {
	case snap.MFI > 80:
		mfiL = "超买"
	case snap.MFI < 20:
		mfiL = "超卖"
	case snap.MFI < 30:
		mfiL = "偏低"
	}

	volLine := fmt.Sprintf("  MFI %.1f(%s)", snap.MFI, mfiL)
	if vol != nil {
		volLine += fmt.Sprintf(" | 今日K线量比%.2f(%s)", vol.ratio, vol.label)
	}
	fmt.Println(volLine)

	// ── 第5行: 信号 ──
	var sigs []string
	if snap.SigTrend {
		sigs = append(sigs, "趋势多头✓")
	}
	if snap.SigOB {
		sigs = append(sigs, "超买⚠️")
	}
	if snap.SigOS {
		sigs = append(sigs, "超卖⚠️")
	}
	if snap.DivBull {
		sigs = append(sigs, "底背离✓")
	}
	if snap.DivBear {
		sigs = append(sigs, "顶背离⚠️")
	}
	if snap.Donch20 {
		sigs = append(sigs, "Donch20突")
	}
	if snap.Donch55 {
		sigs = append(sigs, "Donch55突")
	}
	if snap.Squeeze {
		sigs = append(sigs, "BOLL收窄")
	}
	sigStr := "无"
	if len(sigs) > 0 {
		sigStr = strings.Join(sigs, " ")
	}

	scoreShow := snap.ScoreTotal
	adjNote := ""
	if snap.ScoreAdj != 0 && snap.ScoreAdj != snap.ScoreTotal {
		scoreShow = snap.ScoreAdj
		adjNote = fmt.Sprintf("(adj%d)", snap.ScoreAdj)
	}
	fmt.Printf("  SCORE %d(%+d)%s %s | %s\n", scoreShow, snap.ScoreDelta, adjNote, snap.ScoreLabel, sigStr)

	// ── 第6行: 价位 ──
	todayBias := 0.0
	if snap.Close > 0 {
		todayBias = (s.Price - snap.Close) / snap.Close * 100
	}
	sarDistStr, stDistStr := "—", "—"
	if snap.SAR_Value > 0 {
		sarDistStr = fmt.Sprintf("%.2f%%", (s.Price-snap.SAR_Value)/snap.SAR_Value*100)
	}
	if snap.ST_Value > 0 {
		stDistStr = fmt.Sprintf("%.2f%%", (s.Price-snap.ST_Value)/snap.ST_Value*100)
	}

	fmt.Printf("  价位  昨收%.3f → 现%.3f(%+.2f%%) | BIAS6 %.1f%% BIAS24 %.1f%%\n",
		snap.Close, s.Price, todayBias, snap.Bias6, snap.Bias24)
	fmt.Printf("        SAR %.2f(距%s) | ST %.2f(距%s) | ATRpct %.1f%%\n",
		snap.SAR_Value, sarDistStr, snap.ST_Value, stDistStr, snap.ATR_Pct)

	// ── 第7行: 综合诊断 ──
	var diag []string

	// 趋势判断
	switch {
	case snap.SAR_Long && snap.ST_Long:
		if todayBias < -5 {
			diag = append(diag, "趋势多头·今日大跌·破位风险")
		} else if todayBias < -2 {
			diag = append(diag, "趋势多头·短期回调")
		} else {
			diag = append(diag, "趋势多头·持有")
		}
	case !snap.SAR_Long && !snap.ST_Long:
		if todayBias < -5 {
			diag = append(diag, "双空·暴跌·观望")
		} else {
			diag = append(diag, "双空·反弹减仓")
		}
	default:
		diag = append(diag, "趋势分歧")
	}

	// 极端 + 信号叠加
	if snap.RSI6 < 25 && todayBias < -5 {
		diag = append(diag, "极端超卖→反弹预警")
	}
	if snap.DivBear {
		diag = append(diag, "顶背离风险在")
	}
	if snap.DivBull && todayBias > -3 {
		diag = append(diag, "底背离修复中")
	}
	if vol != nil {
		switch {
		case vol.ratio > 2 && todayBias < -5:
			diag = append(diag, "放量杀跌·谨慎")
		case vol.ratio < 0.5 && todayBias < -5:
			diag = append(diag, "缩量暴跌·抛压衰竭")
		case vol.ratio > 2 && todayBias > 3:
			diag = append(diag, "量价齐升·趋势健康")
		}
	}
	if len(diag) == 0 {
		diag = append(diag, "正常波动·观望")
	}
	fmt.Printf("  诊断  %s\n", strings.Join(diag, "｜"))

	// ── 第8行: 量能明细 ──
	if vol != nil && len(vol.recent) > 0 {
		fmt.Print("  近5日量(万手):")
		for i, v := range vol.recent {
			if i >= 5 {
				break
			}
			m := ""
			switch {
			case v.ratio > 2.0:
				m = "🔥"
			case v.ratio > 1.5:
				m = "🔴"
			case v.ratio < 0.5:
				m = "🟢"
			case v.ratio < 0.7:
				m = "🔵"
			}
			date := v.date
			if len(date) > 5 {
				date = date[len(date)-5:]
			}
			fmt.Printf(" %s%s%.0f(%.2f)", m, date, v.vol/10000, v.ratio)
		}
		fmt.Println()
	}
	fmt.Println()
}

// ── K线量能 ──

func calcVolume(client *http.Client, code string) (*klineVolResult, error) {
	kdata, err := api.FetchDailyKline(client, code, 25)
	if err != nil {
		return nil, err
	}
	candles := kdata.Candles
	if len(candles) < 5 {
		return nil, fmt.Errorf("K线不足5根")
	}
	// candles: [0]=最旧, [len-1]=最新
	// 用最新20根完整K线(exclude today)
	n := 20
	if len(candles) < n+1 {
		n = len(candles) - 1
	}
	end := len(candles) - 1
	start := end - n
	if start < 0 {
		start = 0
	}

	var sumVol float64
	cnt := 0
	for i := start; i < end; i++ {
		sumVol += candles[i].Volume
		cnt++
	}
	avgVol := sumVol / float64(cnt)
	if avgVol <= 0 {
		return nil, fmt.Errorf("近%d日均量为0(疑似停牌)", cnt)
	}

	todayVol := candles[end].Volume
	if todayVol < 1 {
		return nil, fmt.Errorf("今日量0")
	}
	ratio := todayVol / avgVol

	label := "平量"
	switch {
	case ratio > 3.0:
		label = "爆量"
	case ratio > 1.5:
		label = "放量"
	case ratio < 0.4:
		label = "地量"
	case ratio < 0.8:
		label = "缩量"
	}

	res := &klineVolResult{
		ratio:    ratio,
		label:    label,
		todayVol: todayVol,
		avgVol:   avgVol,
	}
	for i := end - 1; i >= 0 && i >= end-6; i-- {
		d := ""
		if i < len(kdata.Dates) {
			d = kdata.Dates[i]
		}
		v := candles[i].Volume
		r := v / avgVol
		res.recent = append(res.recent, struct {
			date  string
			vol   float64
			ratio float64
		}{d, v, r})
	}
	return res, nil
}

// ── SQLite 快照查询 ──

func queryDeepSnapshot(db *sql.DB, code string) (*deepSnapshot, error) {
	q := `
	SELECT trade_date, close,
	       COALESCE(score_total,0), COALESCE(score_delta,0),
	       COALESCE(score_adj,0), COALESCE(score_label,''),
	       COALESCE(kdj_j,0), COALESCE(rsi6,0), COALESCE(wr14,0),
	       COALESCE(adx,0), COALESCE(cmi,0), COALESCE(chop,0),
	       COALESCE(macd_dif,0), COALESCE(macd_hist,0),
	       COALESCE(boll_pb,0), COALESCE(boll_bw,0), COALESCE(mfi,0),
	       COALESCE(sar_long,0), COALESCE(supertrend_long,0),
	       COALESCE(sig_overbought,0), COALESCE(sig_oversold,0),
	       COALESCE(sig_trend_bull,0),
	       COALESCE(div_bull,0), COALESCE(div_bear,0),
	       COALESCE(keltner_squeeze,0),
	       COALESCE(donch_break20_bull,0), COALESCE(donch_break55_bull,0),
	       COALESCE(sar_value,0), COALESCE(supertrend_value,0),
	       COALESCE(bias6,0), COALESCE(bias24,0), COALESCE(atr_pct,0),
	       COALESCE(ret20,0), COALESCE(rs20,0),
	       COALESCE(inside_vol,0), COALESCE(outside_vol,0),
	       COALESCE(turnover_rate,0)
	FROM snapshot WHERE code = ? ORDER BY trade_date DESC LIMIT 1`
	var s deepSnapshot
	err := db.QueryRow(q, code).Scan(
		&s.TradeDate, &s.Close,
		&s.ScoreTotal, &s.ScoreDelta, &s.ScoreAdj, &s.ScoreLabel,
		&s.KDJ_J, &s.RSI6, &s.WR14,
		&s.ADX, &s.CMI, &s.CHOP,
		&s.MACD_Dif, &s.MACD_Hist,
		&s.BOLL_PB, &s.BOLL_BW, &s.MFI,
		&s.SAR_Long, &s.ST_Long,
		&s.SigOB, &s.SigOS, &s.SigTrend,
		&s.DivBull, &s.DivBear,
		&s.Squeeze, &s.Donch20, &s.Donch55,
		&s.SAR_Value, &s.ST_Value,
		&s.Bias6, &s.Bias24, &s.ATR_Pct,
		&s.Ret20, &s.RS20,
		&s.InsideVol, &s.OutsideVol,
		&s.Turnover,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ── 持仓解析 ──

func parseHoldings() ([]holding, error) {
	data, err := os.ReadFile(".holdings")
	if err != nil {
		return nil, fmt.Errorf("读取.holdings: %w", err)
	}

	seen := make(map[string]bool)
	var hh []holding

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, item := range strings.Split(line, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			parts := strings.Split(item, ":")
			if len(parts) != 3 {
				continue
			}
			code := strings.TrimSpace(parts[0])
			cost := 0.0
			qty := 0
			if _, err := fmt.Sscanf(parts[1], "%f", &cost); err != nil || cost <= 0 {
				fmt.Fprintf(os.Stderr, "warn: 跳过持仓行,成本解析失败: %q\n", item)
				continue
			}
			if _, err := fmt.Sscanf(parts[2], "%d", &qty); err != nil || qty <= 0 {
				fmt.Fprintf(os.Stderr, "warn: 跳过持仓行,手数解析失败: %q\n", item)
				continue
			}

			if seen[code] {
				for i := range hh {
					if hh[i].Code == code {
						tq := hh[i].Quantity + qty
						if tq > 0 {
							hh[i].Cost = (hh[i].Cost*float64(hh[i].Quantity) + cost*float64(qty)) / float64(tq)
							hh[i].Quantity = tq
						}
						break
					}
				}
				continue
			}
			seen[code] = true
			hh = append(hh, holding{Code: code, Cost: cost, Quantity: qty})
		}
	}
	return hh, nil
}
