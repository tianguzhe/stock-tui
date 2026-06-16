#!/bin/bash
# 每日收盘后数据更新脚本
# 用途：批量保存快照、更新RS排名、回填决策结果
# 时间：每个交易日收盘后执行（建议 15:30 或之后）

set -e  # 遇到错误立即退出

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

echo "=== 每日数据更新开始 ==="
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "项目: $PROJECT_ROOT"
echo ""

# 1. 批量保存快照数据
echo "=== 1/4 批量保存快照数据 ==="
echo "预编译 indicator-analyze..."
go build -o /tmp/ia ./cmd/indicator-analyze

echo "获取股票列表..."
STOCK_COUNT=$(sqlite3 data/stock.db "SELECT COUNT(*) FROM instrument;")
echo "共 $STOCK_COUNT 只股票"

echo "开始批量保存（预计耗时 60-120 秒）..."
START_TIME=$(date +%s)

sqlite3 data/stock.db "SELECT code FROM instrument;" | \
  xargs -I{} -P 4 /tmp/ia -save {}

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
echo "✅ 批量保存完成，耗时 ${DURATION} 秒"
echo ""

# 2. 更新 RS 排名
echo "=== 2/4 更新 RS 相对强度排名 ==="
go run ./cmd/stockdb rs-rank
echo ""

# 3. 回填决策结果
echo "=== 3/4 回填决策结果 ==="
go run ./cmd/stockdb backfill
echo ""

# 4. 生成选股表（如果有持仓）
echo "=== 4/4 生成选股表 ==="
if [ -f "$PROJECT_ROOT/.holdings" ]; then
  HOLDINGS=$(cat "$PROJECT_ROOT/.holdings")
  echo "持仓配置: $HOLDINGS"
  ./scripts/screen-stocks.sh --holdings "$HOLDINGS"
else
  echo "未配置持仓文件 (.holdings)，跳过选股表生成"
  echo "提示：可创建 .holdings 文件，格式如："
  echo "  sh601138:72.825:400,sh600522:50.876:200"
fi
echo ""

# 5. 统计当日数据
echo "=== 数据统计 ==="
TODAY=$(date '+%Y-%m-%d')
SNAPSHOT_COUNT=$(sqlite3 data/stock.db "SELECT COUNT(*) FROM snapshot WHERE trade_date = '$TODAY';")
TOTAL_DAYS=$(sqlite3 data/stock.db "SELECT COUNT(DISTINCT trade_date) FROM snapshot;")

echo "今日快照数: $SNAPSHOT_COUNT"
echo "累计交易日: $TOTAL_DAYS"

if [ $SNAPSHOT_COUNT -eq 0 ]; then
  echo "⚠️  今日无新快照数据，可能非交易日或数据未更新"
fi
echo ""

echo "=== 每日数据更新完成 ==="
echo "完成时间: $(date '+%Y-%m-%d %H:%M:%S')"
