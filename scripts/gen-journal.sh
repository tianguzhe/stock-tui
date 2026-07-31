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
  seen_header=0
  fmt=new
  while IFS= read -r line; do
    # 锚定章节序号而非标题文字：历史上第三章标题写法多变（明日计划 / 明日预判 & 计划 /
    # 明日（07-08）操作计划），按文字匹配会静默失效；而"明日"二字又会误中
    # "## 二、持仓…按明日优先排序"与三级标题"### 明日重点"
    if [[ "$line" =~ ^##[[:space:]]三、 ]]; then in_table=1; continue; fi
    if [[ $in_table -eq 1 ]]; then
      [[ "$line" =~ ^## ]] && break                         # 下一章节停止，空行跳过
      [[ "$line" =~ ^\| ]] || continue                    # 只取表格行
      [[ "$line" =~ \-\-\- ]] && continue                 # 跳过分隔行
      # 表格首行必为表头：跳过它，并据其判断列布局
      # old(2026-07-13 及以前): 代码|名称|预判|…   new(2026-07-14 起): 标的|判断|触发条件
      if [[ $seen_header -eq 0 ]]; then
        seen_header=1
        [[ "$line" =~ 代码.*名称 ]] && fmt=old
        continue
      fi
      # 触发条件属操作计划、不属预判，故不带入；实际/✓✗/备注留待手工填
      if [[ "$fmt" == "old" ]]; then
        row=$(echo "$line" | awk -F'|' '{
          for(i=2;i<=4;i++){gsub(/^[[:space:]]+|[[:space:]]+$/,"",$i)}
          gsub(/\*\*/,"",$2)
          printf "| %s | %s | %s | — | — | — |", $2, $3, $4
        }')
      else
        row=$(echo "$line" | awk -F'|' '{
          for(i=2;i<=3;i++){gsub(/^[[:space:]]+|[[:space:]]+$/,"",$i)}
          gsub(/\*\*/,"",$2)
          printf "| %s | — | %s | — | — | — |", $2, $3
        }')
      fi
      PREV_TABLE+="${row}\n"
    fi
  done < "$PREV_JOURNAL"
fi

if [[ -z "$PREV_TABLE" ]]; then
  # 回填失效过一次且无人察觉（标题从"明日预判"改成"明日计划"后静默空表），故显式告警
  [[ -f "$PREV_JOURNAL" ]] && echo "WARN: 未能从 $PREV_JOURNAL 的「三、」章节提取预判，昨日复盘表留空" >&2
  PREV_TABLE="| — | — | — | — | — | — |\n"
fi

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

| 代码 | 名称 | 今收 | 今日% | 浮盈 | score | 状态 | 止损 |
|------|------|------|-------|------|-------|------|------|
| — | — | — | — | — | — | — | — |

> 成本 — | 市值 — | 浮盈 —

**信号摘要**（每只1行，格式：\`信号 | 趋势 | 关键触发\`）：
- ——

---

## 三、明日计划

| 标的 | 判断（1句） | 触发条件 → 动作 |
|------|------------|----------------|
| — | — | — |

---

## 四、候补 & 推荐

> 市场广度 —%（</>40%），—

| 级别 | 代码 | 名称 | score | 今日% | 核心依据 | 止损 |
|------|------|------|-------|-------|---------|------|
| — | — | — | — | — | — | — |
TEMPLATE

echo "Created: $OUT"
