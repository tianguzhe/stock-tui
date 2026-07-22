package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"stock-tui/internal/market"
	"stock-tui/internal/ui"
)

// 默认自选股（支持 sh/sz 前缀，或纯6位代码自动识别）
var defaultCodes = []string{
	"sh600519", // 贵州茅台
	"sh601318", // 中国平安
	"sz000858", // 五粮液
	"sz300750", // 宁德时代
	"sh688599", // 天合光能
	"sz000001", // 平安银行
}

type appConfig struct {
	codes         []string
	watchingCodes []string
	bossMode      bool
	simpleMode    bool
}

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "参数错误: %v\n", err)
		os.Exit(2)
	}

	m := ui.New(cfg.codes, cfg.watchingCodes, 5*time.Second, cfg.bossMode, cfg.simpleMode)
	// simple 模式伪装系统监控输出，需内联（非全屏）写入终端滚动区
	// 表格 / boss 模式仍是全屏 TUI，必须保留 AltScreen
	var opts []tea.ProgramOption
	if !cfg.simpleMode {
		opts = append(opts, tea.WithAltScreen())
	}
	p := tea.NewProgram(m, opts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func parseConfig(args []string) (appConfig, error) {
	fs := flag.NewFlagSet("stock-tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	codeList := fs.String("c", "", "comma-separated stock codes (holdings)")
	watchList := fs.String("w", "", "comma-separated watching stock codes")
	bossMode := fs.String("b", "boss", "boss mode")
	simpleMode := fs.Bool("simple", false, "simple one-line mode (mutually exclusive with -b)")

	if err := fs.Parse(args); err != nil {
		return appConfig{}, err
	}

	if *simpleMode {
		bossExplicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "b" {
				bossExplicit = true
			}
		})
		if bossExplicit {
			return appConfig{}, fmt.Errorf("-simple 与 -b 互斥，不能同时使用")
		}
	}

	codes := defaultCodes
	if *codeList != "" {
		codes = market.NormalizeCodes([]string{*codeList})
	} else if fs.NArg() > 0 {
		codes = market.NormalizeCodes(fs.Args())
	}

	var watching []string
	if *watchList != "" {
		watching = market.NormalizeCodes([]string{*watchList})
		// 把观察中的代码也加入拉取列表，避免被遗漏
		seen := make(map[string]bool, len(codes))
		for _, c := range codes {
			seen[c] = true
		}
		for _, c := range watching {
			if !seen[c] {
				codes = append(codes, c)
			}
		}
	}

	boss, err := parseBossMode(*bossMode)
	if err != nil {
		return appConfig{}, err
	}

	return appConfig{codes: codes, watchingCodes: watching, bossMode: boss, simpleMode: *simpleMode}, nil
}

func parseBossMode(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "boss", "y", "yes", "on", "true", "1":
		return true, nil
	case "n", "no", "off", "false", "0", "normal":
		return false, nil
	default:
		return false, fmt.Errorf("-b 仅支持 boss/y/yes/on/true/1 或 n/no/off/false/0")
	}
}
