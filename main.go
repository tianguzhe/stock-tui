package main

import (
	"flag"
	"fmt"
	"io"
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

type appConfig struct {
	codes    []string
	bossMode bool
}

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "参数错误: %v\n", err)
		os.Exit(2)
	}

	m := ui.New(cfg.codes, 5*time.Second, cfg.bossMode)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func parseConfig(args []string) (appConfig, error) {
	fs := flag.NewFlagSet("stock-tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	codeList := fs.String("c", "", "comma-separated stock codes")
	bossMode := fs.String("b", "boss", "boss mode")

	if err := fs.Parse(args); err != nil {
		return appConfig{}, err
	}

	codes := defaultCodes
	if *codeList != "" {
		codes = normalizeCodes([]string{*codeList})
	} else if fs.NArg() > 0 {
		codes = normalizeCodes(fs.Args())
	}

	boss, err := parseBossMode(*bossMode)
	if err != nil {
		return appConfig{}, err
	}

	return appConfig{codes: codes, bossMode: boss}, nil
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

// 自动为纯数字代码加市场前缀（沪市 6/5/11，深市 0/3/12/15/16/18，北交所 92/43/8x）
func normalizeCodes(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, group := range raw {
		for _, c := range strings.Split(group, ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if strings.HasPrefix(c, "sh") || strings.HasPrefix(c, "sz") ||
				strings.HasPrefix(c, "bj") || strings.HasPrefix(c, "hk") {
				out = append(out, c)
				continue
			}
			if len(c) == 6 {
				// 多数市场看首位即可，但 1 开头(沪市可转债 11 / 深市基金债 12、15、16、18)与
				// 北交所(92、43、8x)必须看前两位才能区分，故先按前两位匹配，再回退到首位大类。
				switch c[:2] {
				case "11": // 沪市可转债
					out = append(out, "sh"+c)
				case "12", "15", "16", "18": // 深市可转债 12 / 基金 LOF/ETF 15、16 / 封基 18
					out = append(out, "sz"+c)
				case "43", "82", "83", "87", "88", "92": // 北交所：新三板平移 43/8x、920 新号段(前两位 92)、82 优先股
					out = append(out, "bj"+c)
				default:
					switch c[0] {
					case '6', '5': // 沪市：股票 6 / 基金 ETF 5
						out = append(out, "sh"+c)
					case '0', '3': // 深市：股票 0 / 创业板 3
						out = append(out, "sz"+c)
					default:
						out = append(out, "sh"+c)
					}
				}
			}
		}
	}
	return out
}
