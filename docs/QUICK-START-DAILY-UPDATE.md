# ✅ 每日数据累积系统已就绪

## 🎯 核心文件

```
scripts/
├── daily-update.sh         # 每日更新脚本（可执行）

.holdings                    # 持仓配置文件（可编辑）

docs/
├── daily-update-guide.md   # 完整使用指南
└── backtest-implementation-summary.md  # 回测系统文档
```

---

## 🚀 立即开始

### 1. 每天手动执行（推荐）

**每个交易日收盘后（15:30+）运行：**

```bash
cd /Users/yikwing/vspj/tui
./scripts/daily-update.sh
```

**耗时**：60-120 秒

**输出**：
- ✅ 批量保存快照
- ✅ 更新 RS 排名
- ✅ 回填决策结果
- ✅ 生成选股表

---

### 2. 自动定时执行（可选）

**macOS launchd（推荐）：**

```bash
# 创建定时任务（每天15:35自动执行）
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
</dict>
</plist>
EOF

# 创建日志目录
mkdir -p /Users/yikwing/vspj/tui/logs

# 加载定时任务
launchctl load ~/Library/LaunchAgents/com.stock-tui.daily-update.plist

# 查看状态
launchctl list | grep stock-tui
```

---

## 📊 数据累积进度

### 当前状态

```bash
# 查看累积天数
sqlite3 data/stock.db "SELECT COUNT(DISTINCT trade_date) FROM snapshot;"

# 查看日期范围
sqlite3 data/stock.db "SELECT MIN(trade_date), MAX(trade_date) FROM snapshot;"
```

### 里程碑

| 天数 | 时间 | 可用功能 |
|------|------|---------|
| **10天** | ~2周 | ✅ 可回测10天持有期 |
| **20天** | ~1个月 | ✅ 统计初步可靠 |
| **60天** | ~3个月 | ✅✅ 完整策略回测 |
| **120天** | ~6个月 | ✅✅✅ 统计显著，季度分析 |

---

## 📈 等待期间可以做什么

### 1. 验证系统（立即）

```bash
# 测试回测系统
go run ./cmd/stockdb backtest \
  --start 2026-06-03 \
  --end 2026-06-16 \
  --days 5 \
  --signals "趋势跟随多头"
```

### 2. 配置持仓

编辑 `.holdings` 文件：

```bash
vim .holdings

# 格式：代码:成本价:股数,代码:成本价:股数,...
sh601138:72.825:400,sh600522:50.876:200
```

### 3. 更新日志模板

```bash
# 生成明日日志
./scripts/gen-journal.sh
```

---

## 🎯 10天后（2周）

**数据积累到10个交易日后，可以：**

```bash
# 完整10天回测
go run ./cmd/stockdb backtest \
  --start 2026-06-03 \
  --end 2026-06-20 \
  --signals "all"

# 组合回测
go run ./cmd/stockdb backtest-portfolio \
  --capital 100000 \
  --max-positions 5 \
  --signals "趋势跟随多头"

# 查看胜率统计
sqlite3 data/stock.db "
  SELECT signal_type, 
         COUNT(*) as total,
         SUM(win) as wins,
         ROUND(AVG(win) * 100, 1) as win_rate
  FROM backtest_result
  WHERE exit_date IS NOT NULL
  GROUP BY signal_type
  ORDER BY win_rate DESC;
"
```

---

## 🔧 维护

### 每周检查

```bash
# 查看最近更新
sqlite3 data/stock.db "
  SELECT MAX(captured_at) as last_update,
         COUNT(DISTINCT trade_date) as total_days
  FROM snapshot;
"

# 查看日志
tail -50 logs/daily-update.log
```

### 每月备份

```bash
# 备份数据库
cp data/stock.db data/backups/stock.db.$(date +%Y%m%d)

# 清理旧备份（保留30天）
find data/backups -name "stock.db.*" -mtime +30 -delete
```

---

## 📚 完整文档

- **使用指南**：`docs/daily-update-guide.md`
- **回测系统**：`docs/backtest-implementation-summary.md`
- **每日决策**：`docs/daily-decision.md`

---

## ✅ 下一步行动

1. **今天（立即）**：
   - 手动运行一次 `./scripts/daily-update.sh`
   - 验证脚本正常工作

2. **明天开始**：
   - 每天收盘后手动运行（或设置定时任务）
   - 持续2周

3. **2周后**：
   - 运行完整回测
   - 验证策略有效性
   - 根据结果优化选股规则

4. **3个月后**：
   - 拥有完整可靠的历史数据
   - 可进行季度策略分析
   - 持续优化系统

---

**记住**：每天坚持，积少成多。2周后见效，3个月后成熟！🚀
