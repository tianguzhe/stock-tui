package ui

import (
	"math"
	"regexp"
	"strings"
	"testing"

	"stock-tui/internal/api"
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

func TestSplitMinuteSeriesUsesPreviousCloseBaseline(t *testing.T) {
	points := []api.MinutePoint{
		{Time: "09:31", Price: 9},
		{Time: "09:32", Price: 11},
	}

	redS, greenS, closeS := splitMinuteSeries(points, 10, true)
	if !math.IsNaN(redS[0]) || greenS[0] != 9 {
		t.Fatalf("first point red/green = (%v, %v), want green only", redS[0], greenS[0])
	}
	if redS[1] != 11 || greenS[1] != 11 {
		t.Fatalf("crossing point red/green = (%v, %v), want bridged on both series", redS[1], greenS[1])
	}
	if closeS[0] != 10 || closeS[1] != 10 {
		t.Fatalf("close series = %v, want constant previous close", closeS)
	}
}

func TestSplitMinuteSeriesCanHidePreviousCloseLine(t *testing.T) {
	_, _, closeS := splitMinuteSeries([]api.MinutePoint{{Time: "09:31", Price: 9}}, 10, false)
	if !math.IsNaN(closeS[0]) {
		t.Fatalf("closeS[0] = %v, want NaN when baseline line is hidden", closeS[0])
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

func TestPadRightKeepsWideCharactersInsideWidth(t *testing.T) {
	got := padRight("贵州茅台ABC", 10)
	if visWidth(got) != 10 {
		t.Fatalf("visWidth(%q) = %d, want 10", got, visWidth(got))
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
	if visWidth(header) != tableWidth() {
		t.Fatalf("header width = %d, want %d", visWidth(header), tableWidth())
	}
	if visWidth(row) != tableWidth() {
		t.Fatalf("row width = %d, want %d", visWidth(row), tableWidth())
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

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}
