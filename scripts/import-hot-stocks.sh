#!/usr/bin/env bash
# 从同花顺热榜拉取大盘A股代码，入库（Go 实现）
set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"
cd "$SCRIPT_DIR/.."

exec go run ./cmd/stockdb hot "$@"
