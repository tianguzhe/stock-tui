package ui

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"stock-tui/internal/api"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/guptarohit/asciigraph"
)

// ── 样式 ──────────────────────────────────────────────────────────────────────

var (
	red   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	green = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("237"))

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("214"))

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236"))

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))
)

// ── 列定义 ────────────────────────────────────────────────────────────────────

type tableColumn struct {
	header string
	width  int
	align  lipgloss.Position
}

var cols = []tableColumn{
	{"代码", 8, lipgloss.Left},
	{"名称", 13, lipgloss.Left},
	{"最新价", 9, lipgloss.Right},
	{"涨跌额", 9, lipgloss.Right},
	{"涨跌幅", 9, lipgloss.Right},
	{"今开", 9, lipgloss.Right},
	{"最高", 9, lipgloss.Right},
	{"最低", 9, lipgloss.Right},
	{"成交量", 11, lipgloss.Right},
	{"成交额", 11, lipgloss.Right},
}

var bossCols = []tableColumn{
	{"PID", 5, lipgloss.Right},
	{"USER", 5, lipgloss.Left},
	{"S", 1, lipgloss.Left},
	{"CUR", 8, lipgloss.Right},
	{"OPEN", 8, lipgloss.Right},
	{"HIGH", 8, lipgloss.Right},
	{"LOW", 8, lipgloss.Right},
	{"CPU%", 7, lipgloss.Right},
	{"COMMAND", 7, lipgloss.Left},
}

// ── 消息 ─────────────────────────────────────────────────────────────────────

type tickMsg time.Time
type stocksMsg []api.Stock
type stocksErrMsg struct{ err error }
type minuteMsg struct {
	code   string
	result *api.MinuteResult
}
type minuteErrMsg struct {
	code string
	err  error
}

// ── Model ────────────────────────────────────────────────────────────────────

type Model struct {
	codes        []string
	stocks       []api.Stock
	selected     int
	loading      bool
	err          error
	updated      time.Time
	startedAt    time.Time
	interval     time.Duration
	autoRefresh  bool
	width        int
	height       int
	minute       *api.MinuteResult
	minuteCode   string
	loadingChart bool
	chartErr     error
	bossMode     bool
}

func New(codes []string, interval time.Duration, bossMode bool) Model {
	return Model{
		codes:       codes,
		loading:     true,
		interval:    interval,
		autoRefresh: true,
		bossMode:    bossMode,
		startedAt:   time.Now(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStocks(m.codes), tick(m.interval))
}

func (m *Model) clampSelected() {
	if len(m.stocks) == 0 {
		m.selected = 0
		return
	}
	m.selected = min(max(m.selected, 0), len(m.stocks)-1)
}

func (m Model) selectedStock() (api.Stock, bool) {
	if len(m.stocks) == 0 || m.selected < 0 || m.selected >= len(m.stocks) {
		return api.Stock{}, false
	}
	return m.stocks[m.selected], true
}

func (m Model) tableColumns() []tableColumn {
	if m.bossMode {
		return bossCols
	}
	return cols
}

func (m Model) displayTableWidth() int {
	return tableWidthFor(m.tableColumns())
}

func (m *Model) clearMinute() {
	m.minute = nil
	m.minuteCode = ""
	m.loadingChart = false
	m.chartErr = nil
}

func (m *Model) startSelectedMinuteFetch() tea.Cmd {
	if stock, ok := m.selectedStock(); ok {
		m.loadingChart = true
		return fetchMinute(stock.Code)
	}
	m.clearMinute()
	return nil
}

func (m *Model) moveSelection(delta int) tea.Cmd {
	next := m.selected + delta
	if next < 0 || next >= len(m.stocks) {
		return nil
	}
	m.selected = next
	m.minute = nil
	return m.startSelectedMinuteFetch()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		// 自动刷新关闭时丢弃 tick，不再续期
		if !m.autoRefresh {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(fetchStocks(m.codes), tick(m.interval))

	case stocksMsg:
		m.stocks = []api.Stock(msg)
		m.clampSelected()
		m.loading = false
		m.err = nil
		m.updated = time.Now()
		// 刷新选中股票的分时数据
		return m, m.startSelectedMinuteFetch()

	case stocksErrMsg:
		m.err = msg.err
		m.loading = false

	case minuteMsg:
		// 只接受当前选中股票的结果，丢弃过期响应
		if stock, ok := m.selectedStock(); ok && msg.code == stock.Code {
			m.minute = msg.result
			m.minuteCode = msg.code
			m.loadingChart = false
			m.chartErr = nil
		}

	case minuteErrMsg:
		if stock, ok := m.selectedStock(); ok && msg.code == stock.Code {
			m.chartErr = msg.err
			m.loadingChart = false
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.autoRefresh = !m.autoRefresh
			if m.autoRefresh {
				// 开启时立即刷新并重启 tick 循环
				m.loading = true
				return m, tea.Batch(fetchStocks(m.codes), m.startSelectedMinuteFetch(), tick(m.interval))
			}
		case "up", "k":
			return m, m.moveSelection(-1)
		case "down", "j":
			return m, m.moveSelection(1)
		}
	}

	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "正在加载..."
	}
	if m.bossMode {
		return m.renderBossView()
	}

	var sb strings.Builder

	// ── 标题栏 ─────────────────────────────────────────────────────────────
	helpText := "  ↑↓/jk 切换股票   r 自动刷新开/关   q 退出"
	sb.WriteString(m.statusLine("股票实时行情", "[自动刷新:开]", "[自动刷新:关]", "刷新中...", "更新: ") + "\n")

	if m.err != nil {
		sb.WriteString(red.Render("  ✗ "+m.err.Error()) + "\n")
	}
	sb.WriteString("\n")

	// ── 报价表格 ──────────────────────────────────────────────────────────
	sb.WriteString(m.renderTable("  正在加载..."))

	// ── 分时走势图 ────────────────────────────────────────────────────────
	sb.WriteString("\n")
	chartSection := m.renderChart()
	sb.WriteString(chartSection)

	// ── 帮助栏 ────────────────────────────────────────────────────────────
	sb.WriteString("\n")
	sb.WriteString(dim.Render(helpText))

	return sb.String()
}

func (m Model) renderBossView() string {
	var sb strings.Builder

	sb.WriteString(m.statusLine("htop - system monitor", "[采样:自动]", "[采样:暂停]", "采样中...", "同步: ") + "\n")

	if m.err != nil {
		sb.WriteString(red.Render("  ! 链路异常: 采样失败") + "\n")
	}

	sb.WriteString(m.renderBossMeters())
	sb.WriteString("\n")

	sb.WriteString(m.renderTable("  collecting samples..."))

	sb.WriteString("\n")
	sb.WriteString(m.renderChart())
	sb.WriteString("\n")
	sb.WriteString(dim.Render("  F1 Help  F2 Setup  F3 Search  F5 Tree  r Refresh  q Quit"))

	return sb.String()
}

func (m Model) statusLine(titleText, activeTag, pausedTag, loadingText, updatedPrefix string) string {
	refreshTag := green.Render(activeTag)
	if !m.autoRefresh {
		refreshTag = dim.Render(pausedTag)
	}
	statusText := loadingText
	if !m.loading && !m.updated.IsZero() {
		statusText = updatedPrefix + m.updated.Format("15:04:05")
	}
	title := titleStyle.Render(titleText)
	status := refreshTag + " " + dim.Render(statusText)
	gap := max(0, m.width-lipgloss.Width(title)-lipgloss.Width(status)-2)
	return title + strings.Repeat(" ", gap) + status
}

func (m Model) renderTable(emptyText string) string {
	var sb strings.Builder
	columns := m.tableColumns()
	sb.WriteString(renderHeaderFor(columns) + "\n")
	sb.WriteString(dim.Render(strings.Repeat("─", tableWidthFor(columns))) + "\n")
	if len(m.stocks) == 0 {
		sb.WriteString(dim.Render(emptyText) + "\n")
		return sb.String()
	}
	for i, stock := range m.stocks {
		if m.bossMode {
			sb.WriteString(renderBossRow(stock, i, i == m.selected) + "\n")
		} else {
			sb.WriteString(renderRow(stock, i == m.selected) + "\n")
		}
	}
	return sb.String()
}

func (m Model) renderBossMeters() string {
	selected, ok := m.selectedStock()
	if !ok {
		selected = api.Stock{Precision: 2}
	}

	avgChange := averageAbsChangePct(m.stocks)
	cpuFill := clamp01(math.Abs(selected.ChangePct) / 10)
	memFill := pricePosition(selected)
	netFill := volumeShare(selected, m.stocks)

	active := min(len(m.stocks), 1)
	taskLine := fmt.Sprintf(
		"Tasks: %d total, %d running, %d selected",
		len(m.stocks),
		active,
		active,
	)
	loadLine := fmt.Sprintf(
		"Load average: %.2f%%   Uptime: %s",
		avgChange,
		m.uptimeText(),
	)

	p := selected.Precision
	if p == 0 {
		p = 2
	}
	current := fmt.Sprintf("%.*f", p, selected.Price)
	open := fmt.Sprintf("%.*f", p, selected.Open)
	high := fmt.Sprintf("%.*f", p, selected.High)
	low := fmt.Sprintf("%.*f", p, selected.Low)
	cpuText := formatSignedPercent(selected.ChangePct, p)
	volumeText := formatSamples(selected.Volume)
	maxVolume := formatSamples(maxStockVolume(m.stocks))

	lines := []string{
		taskLine,
		loadLine,
		renderBossMeter("CPU", cpuFill, cpuText),
		renderBossMeter("Mem", memFill, current+" / "+open),
		renderBossMeter("Net", netFill, volumeText+" / "+maxVolume),
		dim.Render("Range: " + low + ".." + high),
	}

	return strings.Join(lines, "\n") + "\n"
}

func (m Model) uptimeText() string {
	if m.startedAt.IsZero() {
		if m.updated.IsZero() {
			return "00:00:00"
		}
		return m.updated.Format("15:04:05")
	}
	elapsed := time.Since(m.startedAt).Round(time.Second)
	elapsed = max(0, elapsed)
	hours := int(elapsed / time.Hour)
	elapsed -= time.Duration(hours) * time.Hour
	minutes := int(elapsed / time.Minute)
	elapsed -= time.Duration(minutes) * time.Minute
	seconds := int(elapsed / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func renderBossMeter(label string, fill float64, text string) string {
	const width = 18
	filled := min(width, int(math.Round(clamp01(fill)*width)))
	bar := strings.Repeat("|", filled) + strings.Repeat(" ", width-filled)
	return fmt.Sprintf("%-3s[%s] %s", label, bar, text)
}

func averageAbsChangePct(stocks []api.Stock) float64 {
	if len(stocks) == 0 {
		return 0
	}
	total := 0.0
	for _, stock := range stocks {
		total += math.Abs(stock.ChangePct)
	}
	return total / float64(len(stocks))
}

func pricePosition(stock api.Stock) float64 {
	if stock.High <= stock.Low {
		return 0
	}
	return clamp01((stock.Price - stock.Low) / (stock.High - stock.Low))
}

func volumeShare(selected api.Stock, stocks []api.Stock) float64 {
	maxVolume := maxStockVolume(stocks)
	if maxVolume <= 0 {
		return 0
	}
	return clamp01(selected.Volume / maxVolume)
}

func maxStockVolume(stocks []api.Stock) float64 {
	maxVolume := 0.0
	for _, stock := range stocks {
		if stock.Volume > maxVolume {
			maxVolume = stock.Volume
		}
	}
	return maxVolume
}

func clamp01(v float64) float64 {
	return min(1, max(0, v))
}

// ── 折线图渲染 ───────────────────────────────────────────────────────────────

func (m Model) renderChart() string {
	var sb strings.Builder

	// 分隔标题
	var chartTitle string
	if m.bossMode {
		chartTitle = " History "
	} else if stock, ok := m.selectedStock(); ok {
		chartTitle = fmt.Sprintf(" 分时走势  %s (%s) ", stock.Name, stock.Code)
	} else {
		chartTitle = " 分时走势 "
	}
	totalW := m.displayTableWidth()
	leftW := max(0, (totalW-lipgloss.Width(chartTitle))/2)
	divider := dim.Render(strings.Repeat("─", leftW)) +
		sectionStyle.Render(chartTitle) +
		dim.Render(strings.Repeat("─", max(0, totalW-leftW-lipgloss.Width(chartTitle))))
	sb.WriteString(divider + "\n")

	// 错误或加载状态
	if m.loadingChart {
		loadingText := "  正在加载分时数据..."
		if m.bossMode {
			loadingText = "  正在加载采样数据..."
		}
		sb.WriteString(dim.Render(loadingText) + "\n")
		return sb.String()
	}
	if m.chartErr != nil {
		if m.bossMode {
			sb.WriteString(red.Render("  ! 采样异常") + "\n")
		} else {
			sb.WriteString(red.Render("  ✗ "+m.chartErr.Error()) + "\n")
		}
		return sb.String()
	}
	if m.minute == nil || len(m.minute.Points) == 0 {
		sb.WriteString(dim.Render(m.noChartDataText()) + "\n")
		return sb.String()
	}

	points := m.minute.Points

	stock, ok := m.selectedStock()
	if !ok {
		sb.WriteString(dim.Render(m.noChartDataText()) + "\n")
		return sb.String()
	}
	prec := m.minute.Precision
	if prec == 0 {
		prec = stock.Precision
	}

	baseline := minuteBaseline(m.minute, stock)
	lower, upper, showBaseline := minuteChartBounds(points, baseline, prec)

	yAxisW := chartYAxisWidth(lower, upper, prec)
	chartW, chartH := m.chartSize(yAxisW)
	series, chartOptions := m.chartSeriesOptions(points, baseline, showBaseline)
	chartOptions = append(chartOptions, chartBoundsOptions(chartW, chartH, prec, lower, upper)...)

	chartStr := asciigraph.PlotMany(series, chartOptions...)
	sb.WriteString(chartStr + "\n")

	// X 轴时间标签
	sb.WriteString(renderTimeAxis(points, chartW, yAxisW) + "\n")
	open := stock.Open
	if open == 0 {
		open = baseline
	}
	sb.WriteString(renderMinuteSummary(points, baseline, open, prec, m.bossMode) + "\n")

	return sb.String()
}

func minuteBaseline(minute *api.MinuteResult, stock api.Stock) float64 {
	if minute.PClose != 0 {
		return minute.PClose
	}
	if stock.Close != 0 {
		return stock.Close
	}
	if len(minute.Points) > 0 {
		return minute.Points[0].Price
	}
	return 0
}

func (m Model) chartSize(yAxisW int) (int, int) {
	chartW := max(30, m.width-yAxisW-2)
	fixedRows := 3 + 2 + len(m.stocks) + 3
	if m.bossMode {
		fixedRows += 6
	}
	chartH := min(18, max(6, m.height-fixedRows))
	return chartW, chartH
}

func chartBoundsOptions(width, height, prec int, lower, upper float64) []asciigraph.Option {
	return []asciigraph.Option{
		asciigraph.Width(width),
		asciigraph.Height(height),
		asciigraph.Precision(uint(prec)),
		asciigraph.LowerBound(lower),
		asciigraph.UpperBound(upper),
	}
}

func (m Model) chartSeriesOptions(points []api.MinutePoint, baseline float64, showBaseline bool) ([][]float64, []asciigraph.Option) {
	if m.bossMode {
		return [][]float64{minutePrices(points)}, nil
	}

	priceS := minutePrices(points)
	priceColor := asciigraph.AnsiColor(151) // Mocha green: 低于等于昨收
	if len(points) > 0 && points[len(points)-1].Price > baseline {
		priceColor = asciigraph.AnsiColor(211) // Mocha red: 高于昨收
	}
	series := [][]float64{priceS}
	colors := []asciigraph.AnsiColor{
		priceColor,
	}
	chars := []asciigraph.CharSet{
		asciigraph.DefaultCharSet,
	}
	if showBaseline {
		// Draw the reference line as a background series. The price series remains
		// a single continuous line so crossing the reference line never splits it.
		series = [][]float64{baselineSeries(len(points), baseline), priceS}
		colors = []asciigraph.AnsiColor{
			asciigraph.AnsiColor(183), // Mocha mauve: 昨收参考线
			priceColor,
		}
		chars = []asciigraph.CharSet{
			asciigraph.CreateCharSet("┈"),
			asciigraph.DefaultCharSet,
		}
	}

	return series, []asciigraph.Option{
		asciigraph.SeriesColors(colors...),
		asciigraph.SeriesChars(chars...),
		asciigraph.AxisColor(asciigraph.AnsiColor(60)),
		asciigraph.LabelColor(asciigraph.AnsiColor(103)),
	}
}

func (m Model) noChartDataText() string {
	if m.bossMode {
		return "  暂无采样数据"
	}
	return "  暂无分时数据（可能为非交易时间）"
}

func minutePrices(points []api.MinutePoint) []float64 {
	prices := make([]float64, len(points))
	for i, p := range points {
		prices[i] = p.Price
	}
	return prices
}

func baselineSeries(length int, baseline float64) []float64 {
	series := make([]float64, length)
	for i := range series {
		series[i] = baseline
	}
	return series
}

func minuteChartBounds(points []api.MinutePoint, baseline float64, prec int) (float64, float64, bool) {
	values := make([]float64, 0, len(points)+2)
	for _, p := range points {
		if p.Price > 0 {
			values = append(values, p.Price)
		}
	}
	if len(values) == 0 && baseline > 0 {
		values = append(values, baseline)
	}

	low, high := minMax(values)
	if low == 0 && high == 0 {
		return 0, 1, false
	}

	span := high - low
	tick := priceTick(prec)
	if span <= 0 {
		base := math.Abs(high)
		if base < 1 {
			base = 1
		}
		span = math.Max(base*0.0002, tick*4)
	}
	padding := span * 0.08
	minPadding := math.Max(math.Abs(baseline)*0.0002, tick)
	if padding < minPadding {
		padding = minPadding
	}

	lower := low - padding
	upper := high + padding

	showBaseline := baseline >= lower && baseline <= upper
	if !showBaseline && baseline > 0 {
		focusedSpan := upper - lower
		expandedLow := math.Min(lower, baseline)
		expandedHigh := math.Max(upper, baseline)
		expandedSpan := expandedHigh - expandedLow
		if expandedSpan <= focusedSpan*2.2 {
			extraPadding := math.Max(padding, focusedSpan*0.05)
			lower = expandedLow - extraPadding
			upper = expandedHigh + extraPadding
			showBaseline = true
		}
	}

	return lower, upper, showBaseline
}

func chartYAxisWidth(lower, upper float64, prec int) int {
	maxLabelWidth := len(fmt.Sprintf("%.*f", prec, upper))
	if w := len(fmt.Sprintf("%.*f", prec, lower)); w > maxLabelWidth {
		maxLabelWidth = w
	}
	return maxLabelWidth + 3
}

func priceTick(prec int) float64 {
	if prec <= 0 {
		return 1
	}
	return math.Pow10(-prec)
}

func renderMinuteSummary(points []api.MinutePoint, baseline, open float64, prec int, bossMode bool) string {
	if len(points) == 0 {
		return ""
	}
	last := points[len(points)-1]
	low, high := minutePointRange(points)
	fp := func(v float64) string { return fmt.Sprintf("%.*f", prec, v) }
	if bossMode {
		parts := []string{
			"当前 " + fp(last.Price),
			"基线 " + fp(open),
			"峰值 " + fp(high),
			"谷值 " + fp(low),
			"采样 " + last.Time,
		}
		return "  " + dim.Render(strings.Join(parts, "  "))
	}

	change := last.Price - baseline
	changePct := 0.0
	if baseline != 0 {
		changePct = change / baseline * 100
	}
	sign := "+"
	if change < 0 {
		sign = ""
	}
	style := red
	if change < 0 {
		style = green
	}

	parts := []string{
		"最新 " + fp(last.Price),
		fmt.Sprintf("较昨收 %s%.*f (%s%.*f%%)", sign, prec, change, sign, prec, changePct),
		"分时高 " + fp(high),
		"分时低 " + fp(low),
		"时间 " + last.Time,
	}
	return "  " + style.Render(parts[0]+"  "+parts[1]) + dim.Render("  "+strings.Join(parts[2:], "  "))
}

func minutePointRange(points []api.MinutePoint) (float64, float64) {
	if len(points) == 0 {
		return 0, 0
	}
	low, high := points[0].Price, points[0].Price
	for _, p := range points[1:] {
		if p.Price < low {
			low = p.Price
		}
		if p.Price > high {
			high = p.Price
		}
	}
	return low, high
}

// ── X 轴时间标签 ─────────────────────────────────────────────────────────────

func renderTimeAxis(points []api.MinutePoint, chartW, yAxisW int) string {
	n := len(points)
	if n == 0 {
		return ""
	}
	if chartW <= 0 {
		chartW = 1
	}
	if n == 1 {
		return strings.Repeat(" ", yAxisW) + dim.Render(points[0].Time)
	}

	// 从实际数据中均匀取时间标签，窄屏时自动减少，避免重叠。
	labelCount := min(5, max(2, chartW/9))
	row := strings.Repeat(" ", yAxisW)
	pos := yAxisW
	used := map[int]bool{}

	for i := 0; i < labelCount; i++ {
		// 数据中对应的点索引
		dataIdx := i * (n - 1) / (labelCount - 1)
		if used[dataIdx] {
			continue
		}
		used[dataIdx] = true
		label := points[dataIdx].Time

		// 该点在图表宽度中的 x 像素位置
		xPos := yAxisW + int(float64(dataIdx)/float64(n-1)*float64(chartW))
		// 居中标签
		xPos -= len(label) / 2
		if xPos < pos {
			xPos = pos
		}
		if xPos+len(label) > yAxisW+chartW {
			xPos = yAxisW + chartW - len(label)
		}

		spaces := max(0, xPos-pos)
		row += strings.Repeat(" ", spaces) + dim.Render(label)
		pos = xPos + len(label)
	}

	return row
}

// ── 表格渲染 ─────────────────────────────────────────────────────────────────

func renderHeader() string {
	return renderHeaderFor(cols)
}

func renderHeaderFor(columns []tableColumn) string {
	var parts []string
	for _, c := range columns {
		parts = append(parts, headerStyle.Render(tableCell(c.header, c)))
	}
	return strings.Join(parts, " ")
}

func renderRow(s api.Stock, selected bool) string {
	// 超过开盘价显示红色，否则绿色
	aboveOpen := s.Price > s.Open
	priceStyle := red
	if !aboveOpen {
		priceStyle = green
	}
	changeUp := s.Change >= 0
	changeStyle := red
	if !changeUp {
		changeStyle = green
	}
	sign := "+"
	if s.Change < 0 {
		sign = ""
	}

	p := s.Precision
	fp := func(v float64) string { return fmt.Sprintf("%.*f", p, v) }

	cells := []string{
		tableCell(s.Code, cols[0]),
		tableCell(s.Name, cols[1]),
		priceStyle.Render(tableCell(fp(s.Price), cols[2])),
		changeStyle.Render(tableCell(fmt.Sprintf("%s%.*f", sign, p, s.Change), cols[3])),
		changeStyle.Render(tableCell(fmt.Sprintf("%s%.*f%%", sign, p, s.ChangePct), cols[4])),
		tableCell(fp(s.Open), cols[5]),
		red.Render(tableCell(fp(s.High), cols[6])),
		green.Render(tableCell(fp(s.Low), cols[7])),
		dim.Render(tableCell(formatVolume(s.Volume), cols[8])),
		dim.Render(tableCell(formatAmount(s.Amount), cols[9])),
	}

	row := strings.Join(cells, " ")
	if selected {
		row = selectedStyle.Render(row)
	}
	return row
}

func renderBossRow(s api.Stock, index int, selected bool) string {
	p := s.Precision
	if p == 0 {
		p = 2
	}
	fp := func(v float64) string { return fmt.Sprintf("%.*f", p, v) }
	state := "S"
	if selected {
		state = "R"
	}

	cells := []string{
		tableCell(fmt.Sprintf("%d", 1000+index), bossCols[0]),
		tableCell(fmt.Sprintf("svc%02d", index+1), bossCols[1]),
		tableCell(state, bossCols[2]),
		tableCell(fp(s.Price), bossCols[3]),
		tableCell(fp(s.Open), bossCols[4]),
		tableCell(fp(s.High), bossCols[5]),
		tableCell(fp(s.Low), bossCols[6]),
		tableCell(formatSignedPercent(s.ChangePct, p), bossCols[7]),
		tableCell(fmt.Sprintf("net.rx%d", index), bossCols[8]),
	}

	row := strings.Join(cells, " ")
	if selected {
		row = selectedStyle.Render(row)
	}
	return row
}

func formatSignedPercent(v float64, prec int) string {
	sign := "+"
	if v < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s%.*f%%", sign, prec, v)
}

func tableWidth() int {
	return tableWidthFor(cols)
}

func tableWidthFor(columns []tableColumn) int {
	w := 0
	for i, c := range columns {
		w += c.width
		if i < len(columns)-1 {
			w++
		}
	}
	return w
}

func tableCell(s string, c tableColumn) string {
	return lipgloss.NewStyle().
		Inline(true).
		MaxWidth(c.width).
		Width(c.width).
		Align(c.align).
		Render(s)
}

// ── 命令 ─────────────────────────────────────────────────────────────────────

func fetchStocks(codes []string) tea.Cmd {
	return func() tea.Msg {
		stocks, err := api.FetchStocks(codes)
		if err != nil {
			return stocksErrMsg{err}
		}
		return stocksMsg(stocks)
	}
}

func fetchMinute(code string) tea.Cmd {
	return func() tea.Msg {
		result, err := api.FetchMinute(code)
		if err != nil {
			return minuteErrMsg{code: code, err: err}
		}
		return minuteMsg{code: code, result: result}
	}
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// ── 辅助函数 ─────────────────────────────────────────────────────────────────

func formatVolume(v float64) string {
	if v >= 1e4 {
		return fmt.Sprintf("%.0f万手", v/1e4)
	}
	return fmt.Sprintf("%.0f手", v)
}

func formatAmount(a float64) string {
	if a >= 1e4 {
		return fmt.Sprintf("%.2f亿", a/1e4)
	}
	return fmt.Sprintf("%.0f万", math.Round(a))
}

func formatSamples(v float64) string {
	if v >= 1e4 {
		return fmt.Sprintf("%.0fk", v/1e3)
	}
	return fmt.Sprintf("%.0f", v)
}

func minMax(data []float64) (float64, float64) {
	if len(data) == 0 {
		return 0, 0
	}
	return slices.Min(data), slices.Max(data)
}
