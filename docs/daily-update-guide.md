# 每日数据自动更新指南

## 🎯 目的

每个交易日收盘后自动执行数据更新，为回测和选股提供持续的数据支持。

---

## 📋 每日更新内容

执行 `./scripts/daily-update.sh` 会自动完成以下 **6 步**（流程顺序不可调整）：

1. **更新同花顺热榜** 🔥 — 获取当日热股池，确保 instrument 表在批量保存前已更新（**必须第一步执行**）
2. **批量保存快照** — 保存所有股票当日的技术指标到 snapshot 表
3. **更新 RS 排名** — 计算相对强度百分位排名
4. **回填决策结果** — 回填 decision_log 中满10天的记录
5. **数据质量检查** 🛡️ — 自动检测快照覆盖率、RS完整性、回填进度（新增）
6. **生成选股表** — 根据 .holdings 配置生成推荐列表（可选）

---

## ⚙️ 使用方式

### 方式 1：手动执行（推荐初期）

每天收盘后（建议 15:30 之后）手动运行：

```bash
cd /Users/yikwing/vspj/tui
./scripts/daily-update.sh
```

**耗时**：约 45-120 秒（视股票数量和白盘/盘后而定）

**输出示例**：
```
=== 每日数据更新开始 ===
时间: 2026-07-06 15:35:12

=== 1/6 更新同花顺热榜 ===
热榜共 84 只，新增入库 12 只，热度更新 72 只，清理冷门 55 只

=== 2/6 批量保存快照数据 ===
共 550 只股票
开始批量保存（预计耗时 60-120 秒）...
batch-save: 550 stocks, 4 workers
progress: 10/550 ok, 0/550 err
progress: ...
batch-save: 550 success, 0 failed out of 550
✅ 批量保存完成，耗时 85 秒

=== 3/6 更新 RS 相对强度排名 ===
rs-rank: updated 548 stocks

=== 4/6 回填决策结果 ===
backfill: 8 条已回填, 3 条跳过（数据不足）

=== 5/6 数据质量检查 ===
✅ RS20 覆盖率: 100.0%
✅ 最近3个交易日数据完整
✅ 已回填: 95.7%

=== 6/6 生成选股表 ===
持仓配置: sh601138:75.406:1,...
[选股表输出]

=== 数据统计 ===
今日快照数: 550
累计交易日: 23

=== 每日数据更新完成 ===
完成时间: 2026-07-06 15:37:26
```

---

### 方式 2：自动定时执行（macOS）

使用 launchd 设置每天 15:35 自动执行：

#### 创建 plist 文件

```bash
cat > ~/Library/LaunchAgents/com.stock-tui.daily-update.plist << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.stock-tui.daily-update</string>
    
    <key>ProgramArguments</key>
    <array>
        <string>/Users/yikwing/vspj/tui/scripts/daily-update.sh</string>
    </array>
    
    <key>WorkingDirectory</key>
    <string>/Users/yikwing/vspj/tui</string>
    
    <key>StandardOutPath</key>
    <string>/Users/yikwing/vspj/tui/logs/daily-update.log</string>
    
    <key>StandardErrorPath</key>
    <string>/Users/yikwing/vspj/tui/logs/daily-update.error.log</string>
    
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>15</integer>
        <key>Minute</key>
        <integer>35</integer>
    </dict>
    
    <key>RunAtLoad</key>
    <false/>
</dict>
</plist>
EOF
```

#### 加载定时任务

```bash
# 创建日志目录
mkdir -p /Users/yikwing/vspj/tui/logs

# 加载任务
launchctl load ~/Library/LaunchAgents/com.stock-tui.daily-update.plist

# 查看任务状态
launchctl list | grep stock-tui

# 测试立即执行（不等到15:35）
launchctl start com.stock-tui.daily-update

# 查看日志
tail -f /Users/yikwing/vspj/tui/logs/daily-update.log
```

#### 管理定时任务

```bash
# 停止任务
launchctl stop com.stock-tui.daily-update

# 卸载任务
launchctl unload ~/Library/LaunchAgents/com.stock-tui.daily-update.plist

# 重新加载（修改配置后）
launchctl unload ~/Library/LaunchAgents/com.stock-tui.daily-update.plist
launchctl load ~/Library/LaunchAgents/com.stock-tui.daily-update.plist
```

---

### 方式 3：cron 定时执行（Linux/macOS 通用）

```bash
# 编辑 crontab
crontab -e

# 添加以下行（每天 15:35 执行）
35 15 * * * cd /Users/yikwing/vspj/tui && ./scripts/daily-update.sh >> logs/daily-update.log 2>&1

# 查看 crontab
crontab -l

# 注意：macOS 可能需要授予 cron 完全磁盘访问权限
# 系统偏好设置 -> 安全性与隐私 -> 隐私 -> 完全磁盘访问权限 -> 添加 /usr/sbin/cron
```

---

## 📂 持仓配置

编辑 `.holdings` 文件来配置当前持仓，支持多账户分仓：

```bash
# 编辑持仓
vim .holdings

# 格式（多行，按账户分区，每行用逗号分隔）：
# 股票账户
sh600522:47.635:1,sh603039:48.958:1
# ETF账户
sz159858:0.711:90

# 或单行（向后兼容）：
sh601138:72.825:400,sh600522:50.876:200,sh603039:44.870:100
```

**格式说明**：
- 每行 `代码:成本价:手数`，多只逗号分隔
- `#` 开头的行是注释/账户标签，自动跳过
- 手数是最小单位（/100 = 手数）

**作用**：
- 生成选股表时会将持仓置顶
- 显示每只持仓的浮盈/浮亏
- 自动计算止损价和建议仓位

---

## 📊 数据累积进度

### 当前状态
```bash
# 查看累积的交易日数
sqlite3 data/stock.db "SELECT COUNT(DISTINCT trade_date) FROM snapshot;"

# 查看日期范围
sqlite3 data/stock.db "SELECT MIN(trade_date), MAX(trade_date) FROM snapshot;"

# 查看每日快照数量
sqlite3 data/stock.db "
  SELECT trade_date, COUNT(*) as count 
  FROM snapshot 
  GROUP BY trade_date 
  ORDER BY trade_date DESC 
  LIMIT 10;
"
```

### 累积里程碑

| 天数 | 时间 | 可用功能 |
|------|------|---------|
| **10天** | 2周 | ✅ 可回测10天持有期 |
| **20天** | 1个月 | ✅ 统计初步可靠 |
| **60天** | 3个月 | ✅✅ 可靠回测，策略优化 |
| **120天** | 6个月 | ✅✅✅ 统计显著，季度分析 |
| **250天** | 1年 | 🎯 完整年度回测 |

---

## 🔍 监控与维护

### 检查脚本是否正常运行

```bash
# 查看最近的日志
tail -100 logs/daily-update.log

# 检查最后一次更新时间
sqlite3 data/stock.db "
  SELECT MAX(captured_at) as last_update 
  FROM snapshot;
"

# 检查今天是否有数据
sqlite3 data/stock.db "
  SELECT COUNT(*) as today_count
  FROM snapshot 
  WHERE trade_date = date('now');
"
```

### 常见问题

**Q: 今日无新快照数据？**
- A: 可能是非交易日（周末/节假日），或盘中运行（数据未更新）

**Q: 脚本运行失败？**
- A: 检查日志 `logs/daily-update.error.log`，常见原因：网络问题、Go编译失败

**Q: 累积速度太慢？**
- A: 可以每天手动运行多次（盘中也可以），会覆盖当日数据

---

## 🎯 最佳实践

### 建议的执行时间

- **15:30** - A股收盘后30分钟（推荐）
- **16:00** - 确保数据已稳定
- **晚上21:00** - 如果白天忘记执行

### 周末/节假日

- 脚本会跳过非交易日（不会产生新数据）
- 不会影响系统，可以保持定时任务运行

### 备份建议

```bash
# 每周备份数据库
cp data/stock.db data/backups/stock.db.$(date +%Y%m%d)

# 清理旧备份（保留最近30天）
find data/backups -name "stock.db.*" -mtime +30 -delete
```

---

## 📈 数据使用

### 回测示例（10天后）

```bash
# 基础回测
go run ./cmd/stockdb backtest \
  --start 2026-06-03 \
  --end 2026-06-17 \
  --signals "趋势跟随多头"

# 组合回测
go run ./cmd/stockdb backtest-portfolio \
  --capital 100000 \
  --max-positions 5 \
  --signals "趋势跟随多头"
```

### 查看历史演变

```bash
# 查看某股近期演变
go run ./cmd/stockdb history sh600522 -n 20

# 导出所有历史数据
sqlite3 data/stock.db "
  SELECT trade_date, code, close, score_total, adx 
  FROM snapshot 
  ORDER BY trade_date, code
" > history.csv
```

---

## 🚀 下一步

1. **立即执行一次** - 验证脚本正常工作
   ```bash
   ./scripts/daily-update.sh
   ```

2. **设置定时任务** - 选择 launchd 或 cron

3. **配置持仓** - 编辑 `.holdings` 文件

4. **持续观察** - 2周后开始回测验证

5. **优化策略** - 根据回测结果调整选股规则

---

**记住**：每天坚持执行，1-2周后就能看到回测效果！3个月后拥有完整可靠的策略验证系统。
