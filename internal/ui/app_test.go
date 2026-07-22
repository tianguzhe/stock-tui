package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"stock-tui/internal/api"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestMinuteChartBoundsIncludeBaselineAndPadding(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "09:31", Price: 10.10},
		{Time: "09:32", Price: 10.30},
	}

	low, high, showBaseline := minuteChartBounds(points, 10.00, 2)
	if !(low < 10.00) {
		t.Fatalf("low = %v, want below baseline", low)
	}
	if !(high > 10.30) {
		t.Fatalf("high = %v, want above highest point", high)
	}
	if !showBaseline {
		t.Fatal("showBaseline = false, want true when baseline is close to price range")
	}
}

func TestMinuteChartBoundsFlatData(t *testing.T) {
	low, high, showBaseline := minuteChartBounds([]api.MinutePoint{{Time: "09:31", Price: 10}}, 10, 2)
	if !(low < 10 && high > 10) {
		t.Fatalf("bounds = (%v, %v), want padded around flat price", low, high)
	}
	if !showBaseline {
		t.Fatal("showBaseline = false, want true for flat data at baseline")
	}
}

func TestMinuteChartBoundsDoesNotLetDistantBaselineFlattenIntradayMove(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "14:56", Price: 1275.96},
		{Time: "14:57", Price: 1275.33},
		{Time: "15:00", Price: 1275.98},
	}

	low, high, showBaseline := minuteChartBounds(points, 1303.00, 2)
	if showBaseline {
		t.Fatal("showBaseline = true, want false for distant previous close")
	}
	if high-low > 2 {
		t.Fatalf("bounds span = %v, want focused intraday scale", high-low)
	}
}

func TestMinuteChartBoundsUsesPrecisionTickInsteadOfFixedCentPadding(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "09:31", Price: 1.388},
		{Time: "09:32", Price: 1.393},
		{Time: "09:33", Price: 1.381},
	}

	low, high, showBaseline := minuteChartBounds(points, 1.386, 3)
	if !showBaseline {
		t.Fatal("showBaseline = false, want true when previous close is inside range")
	}
	if low < 1.379 {
		t.Fatalf("low = %.3f, want tight lower bound near minute low", low)
	}
	if high > 1.395 {
		t.Fatalf("high = %.3f, want tight upper bound near minute high", high)
	}
}

func TestChartYAxisWidthMatchesAsciigraphLabelPad(t *testing.T) {
	got := chartYAxisWidth(1267.12, 1307.42, 2)
	if got != 10 {
		t.Fatalf("chartYAxisWidth() = %d, want 10", got)
	}
}

func TestChartSeriesOptionsKeepsPriceContinuousAcrossBaseline(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "09:31", Price: 9},
		{Time: "09:32", Price: 10},
		{Time: "09:33", Price: 11},
	}

	series, _ := (Model{}).chartSeriesOptions(points, 10, true)
	if len(series) != 2 {
		t.Fatalf("series count = %d, want baseline + continuous price series", len(series))
	}
	for i, got := range series[0] {
		if got != 10 {
			t.Fatalf("baseline series[%d] = %v, want 10", i, got)
		}
	}
	for i, want := range []float64{9, 10, 11} {
		if series[1][i] != want {
			t.Fatalf("price series[%d] = %v, want %v; full series=%v", i, series[1][i], want, series)
		}
	}
}

func TestMinuteBaselineFallbacks(t *testing.T) {
	point := api.MinutePoint{Time: "09:31", Price: 9.8}
	if got := minuteBaseline(&api.MinuteResult{PClose: 10, Points: []api.MinutePoint{point}}, api.Stock{Close: 9.9}); got != 10 {
		t.Fatalf("baseline = %v, want previous close", got)
	}
	if got := minuteBaseline(&api.MinuteResult{Points: []api.MinutePoint{point}}, api.Stock{Close: 9.9}); got != 9.9 {
		t.Fatalf("baseline = %v, want stock close", got)
	}
	if got := minuteBaseline(&api.MinuteResult{Points: []api.MinutePoint{point}}, api.Stock{}); got != 9.8 {
		t.Fatalf("baseline = %v, want first point price", got)
	}
}

func TestRenderTimeAxisSinglePoint(t *testing.T) {
	got := stripANSI(renderTimeAxis([]api.MinutePoint{{Time: "09:31", Price: 10}}, 20, 4))
	if !strings.Contains(got, "09:31") {
		t.Fatalf("axis = %q, want single time label", got)
	}
}

func TestRenderTimeAxisNarrowWidthAvoidsOverlap(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "09:31", Price: 10},
		{Time: "10:30", Price: 11},
		{Time: "11:30", Price: 12},
		{Time: "14:00", Price: 13},
		{Time: "15:00", Price: 14},
	}

	got := stripANSI(renderTimeAxis(points, 12, 4))
	if strings.Count(got, ":") > 2 {
		t.Fatalf("axis = %q, want at most two narrow-width labels", got)
	}
	if !strings.Contains(got, "09:31") || !strings.Contains(got, "15:00") {
		t.Fatalf("axis = %q, want first and last labels", got)
	}
}

func TestFormatVolumeAndAmount(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{name: "small volume", got: formatVolume(9999), want: "9999手"},
		{name: "large volume", got: formatVolume(45890), want: "5万手"},
		{name: "amount wan", got: formatAmount(9999), want: "9999万"},
		{name: "amount yi", got: formatAmount(589548), want: "58.95亿"},
	}

	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestTableCellKeepsWideCharactersInsideWidth(t *testing.T) {
	got := tableCell("贵州茅台ABC", tableColumn{width: 10, align: lipgloss.Left})
	if lipgloss.Width(got) != 10 {
		t.Fatalf("lipgloss.Width(%q) = %d, want 10", got, lipgloss.Width(got))
	}
}

func TestTableHeaderAndRowsShareColumnWidth(t *testing.T) {
	stock := api.Stock{
		Code:      "sh600519",
		Name:      "贵州茅台",
		Price:     1275.96,
		Change:    -2.34,
		ChangePct: -0.18,
		Open:      1278.00,
		High:      1280.00,
		Low:       1270.00,
		Volume:    45890,
		Amount:    589548,
		Precision: 2,
	}

	header := renderHeader()
	row := renderRow(stock, false)
	if lipgloss.Width(header) != tableWidth() {
		t.Fatalf("header width = %d, want %d", lipgloss.Width(header), tableWidth())
	}
	if lipgloss.Width(row) != tableWidth() {
		t.Fatalf("row width = %d, want %d", lipgloss.Width(row), tableWidth())
	}
}

func TestUpdateClampsSelectedWhenStocksShrink(t *testing.T) {
	m := Model{
		selected: 2,
		stocks: []api.Stock{
			{Code: "sh600519", Name: "贵州茅台"},
			{Code: "sh601318", Name: "中国平安"},
			{Code: "sz000858", Name: "五粮液"},
		},
	}

	updated, _ := m.Update(stocksMsg{
		{Code: "sh600519", Name: "贵州茅台"},
	})
	got := updated.(Model)

	if got.selected != 0 {
		t.Fatalf("selected = %d, want clamped to 0", got.selected)
	}
}

func TestRenderChartHandlesStaleMinuteWithoutSelectedStock(t *testing.T) {
	m := Model{
		width:    80,
		height:   24,
		selected: 1,
		minute: &api.MinuteResult{
			PClose:    10,
			Precision: 2,
			Points: []api.MinutePoint{
				{Time: "09:31", Price: 10.1},
			},
		},
	}

	got := stripANSI(m.renderChart())
	if !strings.Contains(got, "暂无分时数据") {
		t.Fatalf("renderChart() = %q, want no-data message", got)
	}
}

func TestUpdateIgnoresStaleMinuteError(t *testing.T) {
	staleErr := errors.New("stale minute request")
	m := Model{
		stocks:       []api.Stock{{Code: "sh600519", Name: "贵州茅台"}},
		selected:     0,
		loadingChart: true,
	}

	updated, _ := m.Update(minuteErrMsg{code: "sz000001", err: staleErr})
	got := updated.(Model)

	if got.chartErr != nil {
		t.Fatalf("chartErr = %v, want nil for stale minute error", got.chartErr)
	}
	if !got.loadingChart {
		t.Fatal("loadingChart = false, want unchanged for stale minute error")
	}
}

func TestUpdateAcceptsCurrentMinuteError(t *testing.T) {
	currentErr := errors.New("current minute request")
	m := Model{
		stocks:       []api.Stock{{Code: "sh600519", Name: "贵州茅台"}},
		selected:     0,
		loadingChart: true,
	}

	updated, _ := m.Update(minuteErrMsg{code: "sh600519", err: currentErr})
	got := updated.(Model)

	if !errors.Is(got.chartErr, currentErr) {
		t.Fatalf("chartErr = %v, want %v", got.chartErr, currentErr)
	}
	if got.loadingChart {
		t.Fatal("loadingChart = true, want false for current minute error")
	}
}

func TestNameColumnShowsHongliETFName(t *testing.T) {
	stock := api.Stock{
		Code:      "sh515180",
		Name:      "红利ETF易方达",
		Price:     1.384,
		Change:    -0.003,
		ChangePct: -0.22,
		Open:      1.386,
		High:      1.394,
		Low:       1.382,
		Volume:    45890,
		Amount:    589548,
		Precision: 3,
	}

	row := stripANSI(renderRow(stock, false))
	if !strings.Contains(row, "红利ETF易方达") {
		t.Fatalf("row = %q, want full fund name", row)
	}
}

func TestBossModeMasksStockTextAndShowsNetworkMetrics(t *testing.T) {
	m := Model{
		bossMode:    true,
		width:       120,
		height:      28,
		autoRefresh: true,
		updated:     time.Date(2026, 5, 28, 14, 30, 0, 0, time.Local),
		stocks: []api.Stock{
			{
				Code:      "sh600519",
				Name:      "贵州茅台",
				Price:     1275.96,
				Open:      1278.00,
				Close:     1303.00,
				High:      1280.00,
				Low:       1270.00,
				ChangePct: 5.00,
				Volume:    45890,
				Precision: 2,
			},
		},
		minute: &api.MinuteResult{
			PClose:    1303.00,
			Precision: 2,
			Points: []api.MinutePoint{
				{Time: "09:31", Price: 1274.50},
				{Time: "09:32", Price: 1275.96},
			},
		},
	}

	got := stripANSI(m.View())
	for _, secret := range []string{"股票", "贵州茅台", "sh600519", "涨跌", "成交"} {
		if strings.Contains(got, secret) {
			t.Fatalf("boss view contains %q: %q", secret, got)
		}
	}
	for _, want := range []string{
		"htop - system monitor",
		"CPU[",
		"Mem[",
		"Net[",
		"Tasks:",
		"Load average:",
		"Uptime:",
		"PID",
		"USER",
		"CPU%",
		"COMMAND",
		"net.rx0",
		"1275.96",
		"1278.00",
		"1280.00",
		"1270.00",
		"+5.00%",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("boss view = %q, want %q", got, want)
		}
	}
	for _, oldText := range []string{"状态", "变化%"} {
		if strings.Contains(got, oldText) {
			t.Fatalf("boss view contains old column text %q: %q", oldText, got)
		}
	}
	if tableWidthFor(bossCols) > 80 {
		t.Fatalf("boss table width = %d, want <= 80", tableWidthFor(bossCols))
	}
}

func TestBossModeSummaryUsesNeutralLabels(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "09:31", Price: 9.8},
		{Time: "09:32", Price: 10.2},
	}

	got := stripANSI(renderMinuteSummary(points, 10, 9.9, 2, true))
	for _, secret := range []string{"最新", "较昨收", "分时高", "分时低"} {
		if strings.Contains(got, secret) {
			t.Fatalf("boss summary contains %q: %q", secret, got)
		}
	}
	for _, want := range []string{"当前 10.20", "基线 9.90", "峰值 10.20", "谷值 9.80"} {
		if !strings.Contains(got, want) {
			t.Fatalf("boss summary = %q, want %q", got, want)
		}
	}
}

func TestBossModeMasksErrors(t *testing.T) {
	m := Model{
		bossMode: true,
		width:    80,
		height:   24,
		err:      errors.New("无此股票数据: sh600519"),
		chartErr: errors.New("无此股票数据: sh600519"),
	}

	got := stripANSI(m.View())
	if strings.Contains(got, "sh600519") || strings.Contains(got, "股票") {
		t.Fatalf("boss error view leaks sensitive text: %q", got)
	}
	for _, want := range []string{"链路异常", "采样失败"} {
		if !strings.Contains(got, want) {
			t.Fatalf("boss error view = %q, want %q", got, want)
		}
	}
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}

// TestSimpleViewRowsLeftAligned 验证 simple 视图每行左对齐在第 0 列
func TestSimpleViewRowsLeftAligned(t *testing.T) {
	m := Model{
		simpleMode:  true,
		width:       120,
		height:      24,
		autoRefresh: true,
		labelMode:   "sys",
		updated:     time.Date(2026, 6, 30, 23, 4, 26, 0, time.Local),
		stocks: []api.Stock{
			{Code: "sh600522", Name: "中天科技", Price: 55.27, ChangePct: -8.92, Precision: 2},
			{Code: "sh601138", Name: "工业富联", Price: 70.04, ChangePct: -2.92, Precision: 2},
		},
	}
	got := stripANSI(m.View())
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d:\n%s", len(lines), got)
	}
	// status / stocks / help 都应从第 0 列开始
	for i, want := range []string{"updated", "CPU", "[b]"} {
		line := lines[i]
		if !strings.HasPrefix(line, want) {
			t.Errorf("line %d = %q, want prefix %q (left-align broken)", i, line, want)
		}
	}
	// 帮助行不应有前导空格
	if strings.HasPrefix(lines[2], " ") {
		t.Errorf("help line %q has leading space", lines[2])
	}
}

// TestSimpleViewKeyChips 验证帮助行使用 [b] [r] [q] 键位 chip
func TestSimpleViewKeyChips(t *testing.T) {
	m := Model{
		simpleMode: true,
		width:      80,
		height:     24,
		updated:    time.Date(2026, 6, 30, 12, 0, 0, 0, time.Local),
	}
	got := stripANSI(m.View())
	for _, chip := range []string{"[b]", "[r]", "[q]"} {
		if !strings.Contains(got, chip) {
			t.Errorf("help line missing chip %q in:\n%s", chip, got)
		}
	}
}

// TestRDoesNotStackTicks 回归测试：反复按 r 不能叠加多个 tick
// 旧实现里 r handler 在「开启」时无条件再调度一个 tick(interval)，
// 与已在飞的 tick 并存，长期会指数增长。
func TestRDoesNotStackTicks(t *testing.T) {
	var scheduleCount int
	tickObserver = func(kind string) {
		if kind == tickObsSchedule {
			scheduleCount++
		}
	}
	defer func() { tickObserver = nil }()

	// 用 1 小时间隔：测试期间没有真 tick 触发
	m := New([]string{"sh600519"}, nil, time.Hour, false, true)
	_ = m.Init()

	afterInit := scheduleCount
	if afterInit != 1 {
		t.Fatalf("Init 后 schedule=%d, want 1", afterInit)
	}

	const toggles = 20
	m2 := m
	for i := 0; i < toggles; i++ {
		updated, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		m2 = updated.(Model)
	}

	afterToggles := scheduleCount
	if afterToggles != afterInit {
		t.Errorf("r 切换叠加了 tick: schedule %d → %d（预期不变）", afterInit, afterToggles)
	}
}

// TestTickSchedulerSelfSustains 验证 tickMsg 自维持：处理后仍会调度下一个 tick
func TestTickSchedulerSelfSustains(t *testing.T) {
	var scheduleCount, processCount int
	tickObserver = func(kind string) {
		switch kind {
		case tickObsSchedule:
			scheduleCount++
		case tickObsProcess:
			processCount++
		}
	}
	defer func() { tickObserver = nil }()

	m := New([]string{"sh600519"}, nil, time.Hour, false, true)
	_ = m.Init()
	initial := scheduleCount

	m.autoRefresh = false
	updated, _ := m.Update(tickMsg{})
	m2 := updated.(Model)
	if m2.autoRefresh != false {
		t.Fatal("autoRefresh 被改写")
	}
	if got := scheduleCount; got != initial+1 {
		t.Errorf("关闭状态下 tickMsg 应仍续期: schedule %d → %d", initial, got)
	}
	if processCount != 1 {
		t.Errorf("tickMsg 未被处理")
	}
}

// TestRImmediateFetchOnReenable 验证重新开启自动刷新时立即拉数据（行为保持）
func TestRImmediateFetchOnReenable(t *testing.T) {
	tickObserver = func(string) {}
	defer func() { tickObserver = nil }()

	m := New([]string{"sh600519"}, nil, time.Hour, false, true)
	_ = m.Init()
	m.autoRefresh = false

	// 关闭→开启：r 应翻为 true 并立即 fetch
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m2 := updated.(Model)
	if !m2.autoRefresh {
		t.Fatal("r 一次后 autoRefresh=false, want true")
	}
	if cmd == nil {
		t.Fatal("r 开启后应返回 fetch cmd, got nil")
	}
}

// TestBKeyOnlyInSimpleMode 验证 b 键仅在 simple 模式下生效
func TestBKeyOnlyInSimpleMode(t *testing.T) {
	m := Model{
		simpleMode: false,
		labelMode:  "name",
		stocks:     []api.Stock{{Code: "sh600519", Name: "贵州茅台"}},
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m2 := updated.(Model)
	if m2.labelMode != "name" {
		t.Errorf("非 simple 模式下 b 键不应改 labelMode: got %q, want %q", m2.labelMode, "name")
	}

	// simple 模式：b 应正常切换
	sm := Model{simpleMode: true, labelMode: "name"}
	updated, _ = sm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	sm2 := updated.(Model)
	if sm2.labelMode != "sys" {
		t.Errorf("simple 模式下 b 键应切到 sys: got %q", sm2.labelMode)
	}
}
