package market

import (
	"reflect"
	"testing"
)

func TestNormalizeCodesCommaSeparated(t *testing.T) {
	got := NormalizeCodes([]string{"000010,515180", " 600580 "})
	want := []string{"sz000010", "sh515180", "sh600580"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeCodes() = %v, want %v", got, want)
	}
}

func TestNormalizeCodeMarketPrefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"沪市股票", "600580", "sh600580"},
		{"沪市科创板", "688981", "sh688981"},
		{"沪市ETF", "515180", "sh515180"},
		{"沪市可转债", "113050", "sh113050"},
		{"深市主板", "000010", "sz000010"},
		{"深市创业板", "300750", "sz300750"},
		{"深市可转债", "123456", "sz123456"},
		{"深市ETF", "159915", "sz159915"},
		{"深市LOF", "160632", "sz160632"},
		{"深市封基", "184688", "sz184688"},
		{"北交所920", "920819", "bj920819"},
		{"北交所平移83", "831445", "bj831445"},
		{"北交所43", "430047", "bj430047"},
		{"北交所优先股82", "820001", "bj820001"},
		{"北交所87", "870299", "bj870299"},
		{"北交所88", "880001", "bj880001"},
		{"已带前缀放行", "bj920819", "bj920819"},
		{"大写前缀归一", "SH600900", "sh600900"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NormalizeCode(tc.in)
			if !ok {
				t.Fatalf("NormalizeCode(%q) ok = false, want true", tc.in)
			}
			if got != tc.want {
				t.Fatalf("NormalizeCode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeCodeRejectsMalformedBareCode(t *testing.T) {
	if got, ok := NormalizeCode("abc"); ok || got != "" {
		t.Fatalf("NormalizeCode(malformed) = %q, %v; want empty,false", got, ok)
	}
}

func TestPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"sh600580", "sh"},
		{"sz000010", "sz"},
		{"bj920819", "bj"},
		{"hk00700", "hk"},
		{"", ""},
		{"6", ""},
	}
	for _, tc := range cases {
		if got := Prefix(tc.in); got != tc.want {
			t.Errorf("Prefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsST(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"ST天际", true},
		{"*ST萃华", true},
		{"SST前锋", true},
		{"贵州茅台", false},
		{"工业富联", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsST(tc.name); got != tc.want {
			t.Errorf("IsST(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPriceLimitPct(t *testing.T) {
	cases := []struct {
		desc string
		code string
		name string
		want float64
	}{
		{"沪市主板", "sh600519", "贵州茅台", 10},
		{"深市主板", "sz002916", "深南电路", 10},
		{"创业板", "sz300750", "宁德时代", 20},
		{"创业板301", "sz301029", "怡合达", 20},
		{"科创板", "sh688981", "中芯国际", 20},
		{"北交所", "bj920819", "颖泰生物", 30},
		{"主板ST按5%", "sz002759", "ST天际", 5},
		{"创业板ST仍按板块20%", "sz300100", "ST双林", 20},
		{"ETF按10%", "sh513260", "恒生科技ETF汇添富", 10},
		{"未知代码回退最严格的10%", "", "", 10},
	}
	for _, tc := range cases {
		if got := PriceLimitPct(tc.code, tc.name); got != tc.want {
			t.Errorf("%s: PriceLimitPct(%q,%q) = %v, want %v", tc.desc, tc.code, tc.name, got, tc.want)
		}
	}
}

// TestPriceLimitPctNoLimitMarkets 港股无日涨跌幅限制,必须返回 NoPriceLimit 让
// 调用方跳过闸门,而不是给个百分比——否则一只涨 15% 的港股会被当成涨停排除。
func TestPriceLimitPctNoLimitMarkets(t *testing.T) {
	if got := PriceLimitPct("hk00700", "腾讯控股"); got != NoPriceLimit {
		t.Errorf("PriceLimitPct(hk) = %v, want NoPriceLimit(%v)", got, NoPriceLimit)
	}
	// ST 判断不得凌驾于"无限制"之上
	if got := PriceLimitPct("hk00001", "ST某港股"); got != NoPriceLimit {
		t.Errorf("PriceLimitPct(hk, ST名) = %v, want NoPriceLimit", got)
	}
	// 科创板 ETF(588 段全部跟踪科创板)按 20%
	if got := PriceLimitPct("sh588000", "科创50ETF"); got != 20 {
		t.Errorf("PriceLimitPct(sh588) = %v, want 20", got)
	}
	// sz159 混杂 10%/20% 产品,代码前缀无法区分 → 保守按 10%
	if got := PriceLimitPct("sz159949", "创业板50ETF"); got != 10 {
		t.Errorf("PriceLimitPct(sz159) = %v, want 10 (保守)", got)
	}
}
