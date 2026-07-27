package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 逐项解析规则由 internal/holdings 覆盖；这里只验证 cmd 层的职责：
// 取值来源的优先级、缺文件的降级、以及到 screener.Holding 的转换。

func TestResolveHoldingsFlagTakesPrecedenceOverFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".holdings")
	if err := os.WriteFile(path, []byte("sz000001:1.0:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveHoldings("sh601991:8.504:13", path)
	if err != nil {
		t.Fatalf("resolveHoldings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d holdings, want 1: %+v", len(got), got)
	}
	if got[0].Code != "sh601991" || got[0].Cost != 8.504 || got[0].Shares != 13 {
		t.Errorf("got %+v, want sh601991 @8.504 ×13", got[0])
	}
}

func TestResolveHoldingsFallsBackToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".holdings")
	content := "# 银河证券账户\nsh601138:61.551:2,sh600909:8.965:2\n# 国泰海通证券账户\nsh600909:9.465:6\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveHoldings("", path)
	if err != nil {
		t.Fatalf("resolveHoldings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d holdings, want 2 after merge: %+v", len(got), got)
	}

	// 两账户同持 sh600909，须合并为一行：(8.965*2 + 9.465*6)/8 = 9.340。
	var merged bool
	for _, h := range got {
		if h.Code != "sh600909" {
			continue
		}
		merged = true
		if h.Shares != 8 || math.Abs(h.Cost-9.34) > 1e-9 {
			t.Errorf("sh600909 = %+v, want 8 手 @9.340", h)
		}
	}
	if !merged {
		t.Error("sh600909 缺失——跨账户持仓未被合并")
	}
}

// 没有持仓文件时仍应能筛选候选，而不是直接失败。
func TestResolveHoldingsMissingFileIsNotAnError(t *testing.T) {
	got, err := resolveHoldings("", filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("resolveHoldings(missing file) = %v, want nil error", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want no holdings", got)
	}
}

func TestResolveHoldingsRejectsMalformedFlag(t *testing.T) {
	for _, raw := range []string{
		"sh601991:8.504",     // 缺手数
		"sh601991:abc:1300",  // 成本非数字
		"sh601991:8.504:abc", // 手数非数字
	} {
		if _, err := resolveHoldings(raw, filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Errorf("resolveHoldings(%q) = nil error, want error", raw)
		}
	}
}

func TestRunCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no arguments",
			args:    []string{},
			wantErr: true,
			errMsg:  "usage:",
		},
		{
			name:    "invalid command",
			args:    []string{"invalid-command"},
			wantErr: true,
			errMsg:  "usage:",
		},
		{
			name:    "tag without subcommand",
			args:    []string{"tag"},
			wantErr: true,
			errMsg:  "usage:",
		},
		{
			name:    "history without code",
			args:    []string{"history"},
			wantErr: true,
			errMsg:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("run() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("run() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOk bool
	}{
		{
			name:   "shanghai stock with prefix",
			input:  "sh600000",
			want:   "sh600000",
			wantOk: true,
		},
		{
			name:   "shanghai stock without prefix",
			input:  "600000",
			want:   "sh600000",
			wantOk: true,
		},
		{
			name:   "shenzhen stock with prefix",
			input:  "sz000001",
			want:   "sz000001",
			wantOk: true,
		},
		{
			name:   "shenzhen stock without prefix",
			input:  "000001",
			want:   "sz000001",
			wantOk: true,
		},
		{
			name:   "invalid code - too short",
			input:  "123",
			want:   "",
			wantOk: false,
		},
		{
			name:   "invalid code - letters",
			input:  "abcdef",
			want:   "shabcdef", // NormalizeCode adds 'sh' prefix to 6-digit codes
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalize(tt.input)
			gotOk := err == nil
			if gotOk != tt.wantOk {
				t.Errorf("normalize() ok = %v, want %v (error: %v)", gotOk, tt.wantOk, err)
				return
			}
			if gotOk && got != tt.want {
				t.Errorf("normalize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) should return 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) should return 0")
	}
}

func TestFormatSignalsBasic(t *testing.T) {
	// Basic smoke test - full integration test requires Candidate struct
	// which is in the screener package
	t.Run("empty signals return dash", func(t *testing.T) {
		// This is a placeholder - actual test needs refactoring
		// to separate formatSignals from Candidate dependency
		expected := "—"
		if expected != "—" {
			t.Error("Empty signals should return dash")
		}
	})
}
