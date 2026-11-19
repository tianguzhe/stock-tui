package main

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"stock-tui/internal/api"
)

// 「疑似消息面/事件驱动」阈值，与 CLAUDE.md「极端行情指标口径」同口径：
// 命中任一条，纯技术指标解释力明显下降，结论必须先核实基本面/消息面。
const (
	newsPctAbs     = 8.0  // 单日 |涨跌幅| ≥ 8%
	newsVolRatio   = 10.0 // 量比 ≥ 10
	newsRangeLowP  = 5.0  // range20/60 分位 ≤ 5%
	newsRangeHighP = 95.0 // range20/60 分位 ≥ 95%
)

// newsEventTriggers 返回命中的消息面条件；空切片表示未命中。
func newsEventTriggers(pct, volRatio, pos20, pos60 float64) []string {
	var hits []string
	if math.Abs(pct) >= newsPctAbs {
		hits = append(hits, fmt.Sprintf("单日%+.2f%%", pct))
	}
	if volRatio >= newsVolRatio {
		hits = append(hits, fmt.Sprintf("量比%.2f", volRatio))
	}
	for _, r := range []struct {
		name string
		pos  float64
	}{{"range20", pos20}, {"range60", pos60}} {
		if r.pos <= newsRangeLowP || r.pos >= newsRangeHighP {
			hits = append(hits, fmt.Sprintf("%s分位%.0f%%", r.name, r.pos))
		}
	}
	return hits
}

// anomalyFetcher 查询当日异动原因；测试里替换成桩。
type anomalyFetcher func(codes []string) ([]api.AnomalyReason, error)

// newsContentMax 限制解读正文长度：CLI 输出要能被 grep/agent 一行读完。
const newsContentMax = 300

// printNewsAnomaly 在命中消息面阈值时附上扶摇的当日异动解读，替代人工 WebSearch。
// 异动解读只有**当日**快照：最新 K 线不是今天时(盘前/非交易日/停牌)直接说明跳过，
// 不能拿今天的解读去解释历史日。fetch 为 nil 表示未配置 Key。
func printNewsAnomaly(code, lastDate, today string, triggers []string, fetch anomalyFetcher, fetchErr error) {
	if len(triggers) == 0 {
		return
	}
	switch {
	case fetch == nil:
		reason := "未配置"
		if fetchErr != nil && !errors.Is(fetchErr, api.ErrNoFuyaoKey) {
			reason = fetchErr.Error()
		}
		fmt.Printf("NEWS_ANOMALY 跳过：%s（%s），请人工检索消息面\n", reason, api.FuyaoKeyEnv)
		return
	case lastDate != today:
		fmt.Printf("NEWS_ANOMALY 跳过：异动解读只有当日快照，最新K线 %s 不是今天 %s\n", lastDate, today)
		return
	}
	reasons, err := fetch([]string{code})
	if err != nil {
		fmt.Printf("NEWS_ANOMALY 查询失败：%v，请人工检索消息面\n", err)
		return
	}
	found := false
	for _, r := range reasons {
		if r.Code != code {
			continue
		}
		found = true
		content := strings.Join(strings.Fields(r.Content), " ")
		if rs := []rune(content); len(rs) > newsContentMax {
			content = string(rs[:newsContentMax]) + "…"
		}
		fmt.Printf("NEWS_ANOMALY tag=%s keywords=%s | %s\n", r.Tag, strings.Join(r.Keywords, ","), content)
	}
	if !found {
		fmt.Println("NEWS_ANOMALY 当日无异动解读记录（不代表无消息面，仍需人工核实）")
	}
}
