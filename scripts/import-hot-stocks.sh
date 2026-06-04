#!/usr/bin/env bash
# 从同花顺热榜拉取大盘A股代码，INSERT OR IGNORE 入库
set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
DB="${1:-${SCRIPT_DIR}/../data/stock.db}"
python3 "${SCRIPT_DIR}/import-hot-stocks.py" "$DB"
