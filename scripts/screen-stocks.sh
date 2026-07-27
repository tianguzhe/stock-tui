#!/usr/bin/env bash
# 多因子选股：持仓 + 7 个优质候选，直接贴入日志"四、候补&推荐"
#
# 🚀 现已迁移到 Go！性能更快、类型安全、统一技术栈
#
# Usage:
#   ./scripts/screen-stocks.sh --capital 80000           # 自动读取 .holdings（推荐）
#   ./scripts/screen-stocks.sh --max 14                  # 调整上限（默认：持仓数+7）
#   ./scripts/screen-stocks.sh --dry-run                 # 仅输出，不写 decision_log
#
# 持仓默认取自 .holdings（格式：代码:成本:手数，1手=100股；# 开头为注释行）。
# 同一代码分散在多个账户会按手数加权自动合并，无需手工算合并成本。
# 仅在需要临时覆盖时才显式传参：
#   ./scripts/screen-stocks.sh --holdings sh601991:8.504:13,sh603256:193.752:1

set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
PROJECT_ROOT="${SCRIPT_DIR}/.."

# 使用 Go 版本（更快、类型安全）
cd "$PROJECT_ROOT"
go run ./cmd/stockdb screen "$@"
