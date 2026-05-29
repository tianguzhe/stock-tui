package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/guptarohit/asciigraph"
	"stock-tui/internal/api"
)

// ── 样式 ──────────────────────────────────────────────────────────────────────

var (
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
	white  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))

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

type tableAlign int

const (
	alignLeft tableAlign = iota
	alignRight
)

type tableColumn struct {
	header string
	width  int
	align  tableAlign
}

var cols = []tableColumn{
	{"代码", 8, alignLeft},
	{"名称", 13, alignLeft},
	{"最新价", 9, alignRight},
	{"涨跌额", 9, alignRight},
	{"涨跌幅", 9, alignRight},
	{"今开", 9, alignRight},
	{"最高", 9, alignRight},
	{"最低", 9, alignRight},
	{"成交量", 11, alignRight},
	{"成交额", 11, alignRight},
}

var bossCols = []tableColumn{
	{"PID", 5, alignRight},
	{"USER", 5, alignLeft},
	{"S", 1, alignLeft},
	{"CUR", 8, alignRight},
	{"OPEN", 8, alignRight},
	{"HIGH", 8, alignRight},
	{"LOW", 8, alignRight},
	{"CPU%", 7, alignRight},
	{"COMMAND", 7, alignLeft},
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
	if m.selected < 0 {
		m.selected = 0
		return
	}
	if m.selected >= len(m.stocks) {
		m.selected = len(m.stocks) - 1
	}
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
		if stock, ok := m.selectedStock(); ok {
			m.loadingChart = true
			return m, fetchMinute(stock.Code)
		}
		m.minute = nil
		m.minuteCode = ""
		m.loadingChart = false
		m.chartErr = nil

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
				var chartCmd tea.Cmd
				if stock, ok := m.selectedStock(); ok {
					m.loadingChart = true
					chartCmd = fetchMinute(stock.Code)
				} else {
					m.loadingChart = false
				}
				return m, tea.Batch(fetchStocks(m.codes), chartCmd, tick(m.interval))
			}
		case "up", "k":
			if m.selected > 0 {
				m.selected--
				m.minute = nil
				m.loadingChart = true
				if stock, ok := m.selectedStock(); ok {
					return m, fetchMinute(stock.Code)
				}
				m.loadingChart = false
			}
		case "down", "j":
			if m.selected < len(m.stocks)-1 {
				m.selected++
				m.minute = nil
				m.loadingChart = true
				if stock, ok := m.selectedStock(); ok {
					return m, fetchMinute(stock.Code)
				}
				m.loadingChart = false
			}
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
	var refreshTag string
	titleText := "股票实时行情"
	loadingText := "刷新中..."
	updatedText := "更新: "
	helpText := "  ↑↓/jk 切换股票   r 自动刷新开/关   q 退出"
	if m.autoRefresh {
		refreshTag = green.Render("[自动刷新:开]")
	} else {
		refreshTag = dim.Render("[自动刷新:关]")
	}
	status := refreshTag + " " + dim.Render(loadingText)
	if !m.loading && !m.updated.IsZero() {
		status = refreshTag + " " + dim.Render(updatedText+m.updated.Format("15:04:05"))
	}
	title := titleStyle.Render(titleText)
	gap := m.width - visWidth(title) - visWidth(status) - 2
	if gap < 0 {
		gap = 0
	}
	sb.WriteString(title + strings.Repeat(" ", gap) + status + "\n")

	if m.err != nil {
		sb.WriteString(red.Render("  ✗ "+m.err.Error()) + "\n")
	}
	sb.WriteString("\n")

	// ── 报价表格 ──────────────────────────────────────────────────────────
	columns := m.tableColumns()
	sb.WriteString(renderHeaderFor(columns) + "\n")
	sb.WriteString(dim.Render(strings.Repeat("─", tableWidthFor(columns))) + "\n")

	if len(m.stocks) == 0 {
		sb.WriteString(dim.Render("  正在加载...") + "\n")
	} else {
		for i, s := range m.stocks {
			if m.bossMode {
				sb.WriteString(renderBossRow(s, i, i == m.selected) + "\n")
			} else {
				sb.WriteString(renderRow(s, i == m.selected) + "\n")
			}
		}
	}

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

	status := green.Render("[采样:自动]")
	if !m.autoRefresh {
		status = dim.Render("[采样:暂停]")
	}
	syncText := "采样中..."
	if !m.loading && !m.updated.IsZero() {
		syncText = "同步: " + m.updated.Format("15:04:05")
	}
	title := titleStyle.Render("htop - system monitor")
	right := status + " " + dim.Render(syncText)
	gap := m.width - visWidth(title) - visWidth(right) - 2
	if gap < 0 {
		gap = 0
	}
	sb.WriteString(title + strings.Repeat(" ", gap) + right + "\n")

	if m.err != nil {
		sb.WriteString(red.Render("  ! 链路异常: 采样失败") + "\n")
	}

	sb.WriteString(m.renderBossMeters())
	sb.WriteString("\n")

	sb.WriteString(renderHeaderFor(bossCols) + "\n")
	sb.WriteString(dim.Render(strings.Repeat("─", tableWidthFor(bossCols))) + "\n")
	if len(m.stocks) == 0 {
		sb.WriteString(dim.Render("  collecting samples...") + "\n")
	} else {
		for i, s := range m.stocks {
			sb.WriteString(renderBossRow(s, i, i == m.selected) + "\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(m.renderChart())
	sb.WriteString("\n")
	sb.WriteString(dim.Render("  F1 Help  F2 Setup  F3 Search  F5 Tree  r Refresh  q Quit"))

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
	if elapsed < 0 {
		elapsed = 0
	}
	hours := int(elapsed / time.Hour)
	elapsed -= time.Duration(hours) * time.Hour
	minutes := int(elapsed / time.Minute)
	elapsed -= time.Duration(minutes) * time.Minute
	seconds := int(elapsed / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func renderBossMeter(label string, fill float64, text string) string {
	const width = 18
	filled := int(math.Round(clamp01(fill) * width))
	if filled > width {
		filled = width
	}
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
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
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
	leftW := (totalW - visWidth(chartTitle)) / 2
	if leftW < 0 {
		leftW = 0
	}
	divider := dim.Render(strings.Repeat("─", leftW)) +
		sectionStyle.Render(chartTitle) +
		dim.Render(strings.Repeat("─", max(0, totalW-leftW-visWidth(chartTitle))))
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

	baseline := m.minute.PClose
	if baseline == 0 {
		baseline = stock.Close
	}
	if baseline == 0 && len(points) > 0 {
		baseline = points[0].Price
	}
	lower, upper, showBaseline := minuteChartBounds(points, baseline, prec)

	// 计算图表尺寸，Y 轴宽度按 asciigraph 实际标签宽度对齐。
	yAxisW := chartYAxisWidth(lower, upper, prec)
	chartW := m.width - yAxisW - 2
	if chartW < 30 {
		chartW = 30
	}
	fixedRows := 3 + 2 + len(m.stocks) + 3
	if m.bossMode {
		fixedRows += 6
	}
	chartH := m.height - fixedRows
	if chartH < 6 {
		chartH = 6
	}
	if chartH > 18 {
		chartH = 18
	}

	chartOptions := []asciigraph.Option{
		asciigraph.Width(chartW),
		asciigraph.Height(chartH),
		asciigraph.Precision(uint(prec)),
		asciigraph.LowerBound(lower),
		asciigraph.UpperBound(upper),
	}
	var series [][]float64
	if m.bossMode {
		series = [][]float64{minutePrices(points)}
	} else {
		redS, greenS, closeS := splitMinuteSeries(points, baseline, showBaseline)
		series = [][]float64{redS, greenS}
		colors := []asciigraph.AnsiColor{
			asciigraph.AnsiColor(211), // Mocha red: 高于昨收
			asciigraph.AnsiColor(151), // Mocha green: 低于等于昨收
		}
		chars := []asciigraph.CharSet{
			asciigraph.DefaultCharSet,
			asciigraph.DefaultCharSet,
		}
		if showBaseline {
			series = append(series, closeS)
			colors = append(colors, asciigraph.AnsiColor(183)) // Mocha mauve: 昨收参考线
			chars = append(chars, asciigraph.CreateCharSet("┈"))
		}
		chartOptions = append(chartOptions,
			asciigraph.SeriesColors(colors...),
			asciigraph.SeriesChars(chars...),
			asciigraph.AxisColor(asciigraph.AnsiColor(60)),
			asciigraph.LabelColor(asciigraph.AnsiColor(103)),
		)
	}

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

func (m Model) noChartDataText() string {
	if m.bossMode {
		return "  暂无采样数据"
	}
	return "  暂无分时数据（可能为非交易时间）"
}

func splitMinuteSeries(points []api.MinutePoint, baseline float64, showBaseline bool) ([]float64, []float64, []float64) {
	nan := math.NaN()
	redS := make([]float64, len(points))
	greenS := make([]float64, len(points))
	closeS := make([]float64, len(points))

	for i, p := range points {
		above := p.Price > baseline
		if above {
			redS[i] = p.Price
			greenS[i] = nan
		} else {
			greenS[i] = p.Price
			redS[i] = nan
		}
		if showBaseline {
			closeS[i] = baseline
		} else {
			closeS[i] = nan
		}

		if i > 0 {
			prevAbove := points[i-1].Price > baseline
			if prevAbove != above {
				redS[i] = p.Price
				greenS[i] = p.Price
			}
		}
	}

	return redS, greenS, closeS
}

func minutePrices(points []api.MinutePoint) []float64 {
	prices := make([]float64, len(points))
	for i, p := range points {
		prices[i] = p.Price
	}
	return prices
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

		spaces := xPos - pos
		if spaces < 0 {
			spaces = 0
		}
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
	if c.align == alignRight {
		return padLeft(s, c.width)
	}
	return padRight(s, c.width)
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

func padRight(s string, w int) string {
	s = truncateWidth(s, w)
	vw := visWidth(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vw)
}

func padLeft(s string, w int) string {
	s = truncateWidth(s, w)
	vw := visWidth(s)
	if vw >= w {
		return s
	}
	return strings.Repeat(" ", w-vw) + s
}

func truncateWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var sb strings.Builder
	used := 0
	for _, c := range s {
		cw := charWidth(c)
		if used+cw > w {
			break
		}
		sb.WriteRune(c)
		used += cw
	}
	return sb.String()
}

func charWidth(c rune) int {
	if c > 0x2E80 {
		return 2
	}
	return 1
}

func runeWidth(r []rune) int {
	w := 0
	for _, c := range r {
		w += charWidth(c)
	}
	return w
}

func visWidth(s string) int {
	// 去掉 ANSI escape codes 再计算视觉宽度
	inEsc := false
	w := 0
	for _, c := range s {
		if c == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if c == 'm' {
				inEsc = false
			}
			continue
		}
		w += charWidth(c)
	}
	return w
}

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
	mn, mx := data[0], data[0]
	for _, v := range data[1:] {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	return mn, mx
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
