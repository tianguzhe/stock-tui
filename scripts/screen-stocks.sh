#!/usr/bin/env bash
# 多因子选股：持仓 + 7 个优质候选，直接贴入日志"四、候补&推荐"
#
# 🚀 现已迁移到 Go！性能更快、类型安全、统一技术栈
#
# Usage:
#   ./scripts/screen-stocks.sh --holdings sh601991:8.504:1300,sh603256:193.752:100,sh605589:53.176:200
#   ./scripts/screen-stocks.sh --holdings ... --max 14   # 调整上限（默认：持仓数+7）
#   ./scripts/screen-stocks.sh --dry-run                 # 仅输出，不写 decision_log

set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
PROJECT_ROOT="${SCRIPT_DIR}/.."

# 使用 Go 版本（更快、类型安全）
cd "$PROJECT_ROOT"
go run ./cmd/stockdb screen "$@"
