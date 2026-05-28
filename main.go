package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func main() {
	codes := defaultCodes
	if len(os.Args) > 1 {
		codes = normalizeCodes(os.Args[1:])
	}

	m := ui.New(codes, 5*time.Second)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

// 自动为纯数字代码加市场前缀（6开头→sh，0/3开头→sz）
func normalizeCodes(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if strings.HasPrefix(c, "sh") || strings.HasPrefix(c, "sz") || strings.HasPrefix(c, "hk") {
			out = append(out, c)
			continue
		}
		if len(c) == 6 {
			switch c[0] {
			case '6':
				out = append(out, "sh"+c)
			case '0', '3':
				out = append(out, "sz"+c)
			default:
				out = append(out, "sh"+c)
			}
		}
	}
	return out
}
