package main

import (
	"reflect"
	"testing"

	"stock-tui/internal/market"
)

func TestParseConfigUsesCommaSeparatedCodes(t *testing.T) {
	cfg, err := parseConfig([]string{"-c", "000010,515180"})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}

	want := []string{"sz000010", "sh515180"}
	if !reflect.DeepEqual(cfg.codes, want) {
		t.Fatalf("codes = %v, want %v", cfg.codes, want)
	}
}

func TestParseConfigDefaultsToBossMode(t *testing.T) {
	cfg, err := parseConfig(nil)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}

	if !cfg.bossMode {
		t.Fatal("bossMode = false, want true by default")
	}
}

func TestParseConfigDisablesBossModeWithN(t *testing.T) {
	cfg, err := parseConfig([]string{"-b", "n"})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}

	if cfg.bossMode {
		t.Fatal("bossMode = true, want false for -b n")
	}
}

func TestParseConfigKeepsPositionalCodes(t *testing.T) {
	cfg, err := parseConfig([]string{"000010", "515180"})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}

	want := []string{"sz000010", "sh515180"}
	if !reflect.DeepEqual(cfg.codes, want) {
		t.Fatalf("codes = %v, want %v", cfg.codes, want)
	}
}

func TestNormalizeCodesMarketPrefix(t *testing.T) {
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := market.NormalizeCodes([]string{tc.in})
			if !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("normalizeCodes(%q) = %v, want [%s]", tc.in, got, tc.want)
			}
		})
	}
}
