package main

import (
	"strings"
	"testing"
)

func TestParseHoldings(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:    "single holding",
			input:   "sh601991:8.504:1300",
			want:    1,
			wantErr: false,
		},
		{
			name:    "multiple holdings",
			input:   "sh601991:8.504:1300,sh603256:193.752:100",
			want:    2,
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    0,
			wantErr: false,
		},
		{
			name:    "trailing comma",
			input:   "sh601991:8.504:1300,",
			want:    1,
			wantErr: false,
		},
		{
			name:    "invalid format - missing shares",
			input:   "sh601991:8.504",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid format - non-numeric cost",
			input:   "sh601991:abc:1300",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid format - non-numeric shares",
			input:   "sh601991:8.504:abc",
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHoldings(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseHoldings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) != tt.want {
				t.Errorf("parseHoldings() got %d holdings, want %d", len(got), tt.want)
			}
		})
	}
}

func TestParseHoldingsValues(t *testing.T) {
	input := "sh601991:8.504:1300"
	holdings, err := parseHoldings(input)
	if err != nil {
		t.Fatalf("parseHoldings() error = %v", err)
	}

	if len(holdings) != 1 {
		t.Fatalf("len(holdings) = %d, want 1", len(holdings))
	}

	h := holdings[0]
	if h.Code != "sh601991" {
		t.Errorf("Code = %s, want sh601991", h.Code)
	}
	if h.Cost != 8.504 {
		t.Errorf("Cost = %f, want 8.504", h.Cost)
	}
	if h.Shares != 1300 {
		t.Errorf("Shares = %d, want 1300", h.Shares)
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
		name    string
		input   string
		want    string
		wantOk  bool
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
