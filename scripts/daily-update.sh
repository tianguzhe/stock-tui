#!/bin/bash
# 每日收盘后数据更新脚本
# 用途：更新热榜、批量保存快照、更新RS排名、回填决策结果
# 时间：每个交易日收盘后执行（建议 15:30 或之后）
# ⚠️ 流程顺序不可调整：热榜必须第一步执行

set -e  # 遇到错误立即退出

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

echo "=== 每日数据更新开始 ==="
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "项目: $PROJECT_ROOT"
echo ""

# 0. 【必须第一步】更新同花顺热榜（确保 instrument 表在批量 -save 前已更新）
echo "=== 1/5 更新同花顺热榜 ==="
if ! go run ./cmd/stockdb hot; then
  echo "❌ 热榜更新失败，请检查网络连接"
  exit 1
fi
echo ""

# 2. 批量保存快照数据
echo "=== 2/5 批量保存快照数据 ==="
echo "预编译 indicator-analyze..."
go build -o /tmp/ia ./cmd/indicator-analyze

echo "获取股票列表..."
STOCK_COUNT=$(sqlite3 data/stock.db "SELECT COUNT(*) FROM instrument;")
echo "共 $STOCK_COUNT 只股票"

echo "开始批量保存（预计耗时 90-150 秒）..."
START_TIME=$(date +%s)

# ⚠️ -P 4 会导致 SQLITE_BUSY 错误，使用 -P 1 串行执行
sqlite3 data/stock.db "SELECT code FROM instrument;" | \
  xargs -I{} -P 1 /tmp/ia -save {}

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
echo "✅ 批量保存完成，耗时 ${DURATION} 秒"
echo ""

# 3. 更新 RS 排名
echo "=== 3/5 更新 RS 相对强度排名 ==="
go run ./cmd/stockdb rs-rank
echo ""

# 4. 回填决策结果
echo "=== 4/5 回填决策结果 ==="
go run ./cmd/stockdb backfill
echo ""

# 5. 生成选股表（如果有持仓）
echo "=== 5/5 生成选股表 ==="
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

# 统计当日数据
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
