package main

import (
	"reflect"
	"testing"
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
