#!/usr/bin/env bash
# gen-journal.sh — 生成下一个交易日的日志文件夹
#
# Usage:
#   ./scripts/gen-journal.sh            # 生成今天的日志（today）
#   ./scripts/gen-journal.sh 2026-06-04 # 指定日期
#
# 结构：docs/journal/YYYY-MM-DD/journal.md
#   Section 1 — 昨日复盘  （自动填入昨日预判表，等手动填写实际结果）
#   Section 2 — 今日分析  （自动填入 DB 最新 snapshot 快照表）

set -euo pipefail

TODAY=${1:-$(date +%Y-%m-%d)}
YESTERDAY=$(date -v-1d -j -f "%Y-%m-%d" "$TODAY" +%Y-%m-%d 2>/dev/null \
  || date -d "$TODAY - 1 day" +%Y-%m-%d)   # macOS / Linux 兼容

DIR="docs/journal/${TODAY}"
OUT="${DIR}/journal.md"
DB="data/stock.db"

if [[ -f "$OUT" ]]; then
  echo "Already exists: $OUT"
  exit 0
fi

mkdir -p "$DIR"

# ── 从 DB 读取最新快照（全量，按 score 降序）────────────────────────────
SNAP_TABLE=""
while IFS='|' read -r code name close chg score td_s td_c sar_l st_l adx streak; do
  sar_st="$( [[ "$sar_l" -eq 1 ]] && echo '多' || echo '空')/$( [[ "$st_l" -eq 1 ]] && echo '多' || echo '空')"
  # 优先展示 countdown，否则展示 setup
  td="$td_s"
  [[ "$td_c" != "-/0" && -n "$td_c" ]] && td="$td_c"
  chg_fmt=$(printf "%+.2f%%" "$chg")
  SNAP_TABLE+="| $code | $name | $close | $chg_fmt | $score | $td | $(printf '%.1f' "$adx") | $sar_st | — | — |\n"
done < <(sqlite3 "$DB" "
SELECT i.code, i.name, s.close, s.change_pct, s.score_total,
       s.td_setup, s.td_countdown, s.sar_long, s.supertrend_long, s.adx, s.streak
FROM snapshot s JOIN instrument i ON s.code=i.code
WHERE s.trade_date=(SELECT MAX(trade_date) FROM snapshot)
ORDER BY s.score_total DESC;")

# ── 从昨日 journal 提取预判表（自动回填"实际"列留空）──────────────────
PREV_JOURNAL="docs/journal/${YESTERDAY}/journal.md"
PREV_TABLE=""
if [[ -f "$PREV_JOURNAL" ]]; then
  # 抓取"明日预判"章节的表格行
  in_table=0
  while IFS= read -r line; do
    if [[ "$line" =~ ^##.*明日预判 ]]; then in_table=1; continue; fi
    if [[ $in_table -eq 1 && "$line" =~ ^\| ]]; then
      # 跳过表头和分隔行
      [[ "$line" =~ \-\-\- ]] && continue
      [[ "$line" =~ 代码.*名称 ]] && continue
      # 追加"实际涨跌"和"对否"空列
      PREV_TABLE+="${line%|} — | — |\n"
    elif [[ $in_table -eq 1 && -z "$line" ]]; then
      in_table=0
    fi
  done < "$PREV_JOURNAL"
fi

if [[ -z "$PREV_TABLE" ]]; then
  PREV_TABLE="| — | — | — | — | — | — |\n"
fi

# ── 写文件 ────────────────────────────────────────────────────────────────
cat > "$OUT" << TEMPLATE
# 日志 · ${TODAY}

---

## 一、昨日复盘（${YESTERDAY}）

> 对照昨日"明日预判"表，填写实际结果

### 1.1 预判对比

| 代码 | 名称 | 预判方向 | 实际涨跌 | 对否 | 备注 |
|------|------|----------|----------|------|------|
$(echo -e "$PREV_TABLE")

### 1.2 止损触发

（无 / 手动填写）

### 1.3 复盘小结

（待填写：哪里判断准了、哪里出了偏差、下次改进点）

---

## 二、今日分析（${TODAY}）

### 2.1 大盘环境

| 指数 | 涨跌 | 备注 |
|------|------|------|
| 上证 | — | |
| 深成 | — | |
| 创业板 | — | |

### 2.2 全量快照（DB 最新，按 score 排序）

| 代码 | 名称 | 收盘 | 今日% | score | TD | ADX | SAR/ST | 止损价 | 仓位 |
|------|------|------|-------|-------|----|-----|--------|--------|------|
$(echo -e "$SNAP_TABLE")

### 2.3 核心持仓信号解读

（待填写：针对持仓各只的今日信号变化）

### 2.4 候补标的

| 代码 | 名称 | score | 等待条件 |
|------|------|-------|----------|
| — | — | — | — |

### 2.5 今日操作

| 时间 | 代码 | 操作 | 价格 | 理由 |
|------|------|------|------|------|
| — | — | — | — | — |

### 2.6 明日计划

- [ ] 待填写

---

## 三、明日预判（给次日复盘用）

| 代码 | 名称 | 预判方向 | 依据 |
|------|------|----------|------|
| — | — | — | — |
TEMPLATE

echo "Created: $OUT"