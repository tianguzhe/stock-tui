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
