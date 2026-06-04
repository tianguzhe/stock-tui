#!/usr/bin/env bash
# 多因子选股：持仓 + 优质候选，合计最多 10 只，直接贴入日志"四、候补&推荐"
#
# Usage:
#   ./scripts/screen-stocks.sh --holdings sh601991:8.504:1300,sh603256:193.752:100,sh605589:53.176:200
#   ./scripts/screen-stocks.sh --holdings ... --max 12   # 调整上限

set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
DB="${SCRIPT_DIR}/../data/stock.db"
python3 "${SCRIPT_DIR}/screen-stocks.py" "$DB" "$@"
