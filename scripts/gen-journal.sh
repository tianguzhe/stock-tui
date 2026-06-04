#!/usr/bin/env bash
# gen-journal.sh — 生成当日复盘日志
#
# Usage:
#   ./scripts/gen-journal.sh            # 今天
#   ./scripts/gen-journal.sh 2026-06-05 # 指定日期

set -euo pipefail

TODAY=${1:-$(date +%Y-%m-%d)}
YESTERDAY=$(date -v-1d -j -f "%Y-%m-%d" "$TODAY" +%Y-%m-%d 2>/dev/null \
  || date -d "$TODAY - 1 day" +%Y-%m-%d)

# 星期几
DOW=$(date -j -f "%Y-%m-%d" "$TODAY" +%u 2>/dev/null || date -d "$TODAY" +%u)
WEEKDAYS=("" "周一" "周二" "周三" "周四" "周五" "周六" "周日")
WEEKDAY="${WEEKDAYS[$DOW]}"

OUT="docs/journal/${TODAY}/journal.md"

if [[ -f "$OUT" ]]; then
  echo "Already exists: $OUT"
  exit 0
fi

mkdir -p "docs/journal/${TODAY}"

# ── 从昨日 journal 提取预判表，回填"昨日复盘"────────────────────────────────
PREV_JOURNAL="docs/journal/${YESTERDAY}/journal.md"
PREV_TABLE=""
if [[ -f "$PREV_JOURNAL" ]]; then
  in_table=0
  while IFS= read -r line; do
    if [[ "$line" =~ ^##.*明日预判 ]]; then in_table=1; continue; fi
    if [[ $in_table -eq 1 ]]; then
      [[ "$line" =~ ^## ]] && break                         # 下一章节停止，空行跳过
      [[ "$line" =~ ^\| ]] || continue                    # 只取表格行
      [[ "$line" =~ \-\-\- ]] && continue                 # 跳过分隔行
      [[ "$line" =~ 代码.*名称 ]] && continue             # 跳过表头
      # 提取前3列（代码/名称/预判），补"实际/✓✗/备注"占位
      row=$(echo "$line" | awk -F'|' '{
        for(i=2;i<=4;i++){gsub(/^[[:space:]]+|[[:space:]]+$/,"",$i)}
        printf "| %s | %s | %s | — | — | — |", $2, $3, $4
      }')
      PREV_TABLE+="${row}\n"
    fi
  done < "$PREV_JOURNAL"
fi

[[ -z "$PREV_TABLE" ]] && PREV_TABLE="| — | — | — | — | — | — |\n"

# ── 写文件 ────────────────────────────────────────────────────────────────────
cat > "$OUT" << TEMPLATE
# 日志 · ${TODAY}（${WEEKDAY}）

---

## 一、昨日复盘

| 代码 | 名称 | 预判 | 实际 | ✓/✗ | 备注 |
|------|------|------|------|-----|------|
$(echo -e "$PREV_TABLE")
止损触发：无

小结：（待填）

---

## 二、持仓（${TODAY} 收盘）

| 代码 | 名称 | 成本 | 股数 | 今收 | 今日% | 浮盈 | score | TD | ADX | SAR | OBV |
|------|------|------|------|------|-------|------|-------|----|-----|-----|-----|
| — | — | — | — | — | — | — | — | — | — | — | — |

> 成本 — | 市值 — | 浮盈 —

（各持仓信号待填）

---

## 三、明日预判 & 计划

| 代码 | 名称 | 预判 | 操作触发条件 | 止损 |
|------|------|------|-------------|------|
| — | — | — | — | — |

---

## 四、候补 & 推荐

候补：（待填）

推荐购买：

| 级别 | 代码 | 名称 | score/ADX | 今日% | PERF 核心依据 | 入场价位 | 止损 |
|------|------|------|-----------|-------|--------------|---------|------|
| — | — | — | — | — | — | — | — |
TEMPLATE

echo "Created: $OUT"
