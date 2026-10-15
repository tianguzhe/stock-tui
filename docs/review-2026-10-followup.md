# 2026-10 系统审查：数据侧待办

2026-10-15 审查改了指标口径与回测引擎（提交 `62df4b3` / `4499c71` / `c36325b`）。代码已合入，但**库内历史数据仍是旧口径**，须在有真实 `data/stock.db` 的机器上补做下列事项。完成一项勾一项，全部完成后可删除本文件。

## 为什么必须做

| 改动 | 对库内数据的影响 |
|---|---|
| DMI 改为通达信口径（`ADX = MA(DX,6)`，原 Wilder RMA 平均低 9.7 点） | 历史行的 `adx`/`adxr`/`pdi`/`mdi` 及依赖 ADX 的 `score_total`/`score_adj`/`sig_trend_bull` 都是旧口径，与新写入的行不可比 |
| 回测：掉出热榜的标的不再被丢弃；组合回测扣印花税；组合回测过滤看空信号 | 2026-10-15 之前的回测结果都有乐观偏差：亏损被漏算，还混进了看空信号的买入，**一律作废** |
| CYC/CYQ 成交额折算到前复权价位 | 不落库，无需处理；但 journal / 持仓监控里引用过的 CYC∞ 支撑位偏高（例：富联 38.648 → 正确值 37.205），后续引用以新值为准 |

## 待办

### ☐ 1. 全库按新口径重算

```bash
# 备份
cp data/stock.db data/stock.db.bak-$(date +%Y%m%d-%H%M)

# 重算全部历史行的指标与评分（约 4 分钟）
go run ./cmd/stockdb repair-scores --all -P 4

# ret20 等随之变化，重排全部交易日的 RS（rs-rank 默认只排最新一天）
for d in $(sqlite3 data/stock.db "SELECT DISTINCT trade_date FROM snapshot ORDER BY trade_date;"); do
  go run ./cmd/stockdb rs-rank --date "$d"
done
```

**验收**
- 任选一只当日行情，比对库内 `adx` 与同花顺/通达信 DMI（参数 14/6）的 ADX，应一致：
  ```bash
  sqlite3 data/stock.db "SELECT trade_date, adx, pdi, mdi FROM snapshot WHERE code='sh601138' ORDER BY trade_date DESC LIMIT 3;"
  ```
- RS 覆盖率每天都应 ≥ 90%（下面这条查询应返回空）：
  ```bash
  sqlite3 data/stock.db "SELECT trade_date, COUNT(*) n, SUM(CASE WHEN rs20>0 THEN 1 ELSE 0 END) ranked FROM snapshot GROUP BY trade_date HAVING 100.0*ranked/n < 90;"
  ```

### ☐ 2. 重跑回测

必须在第 1 项之后做，否则回测读到的仍是旧口径的 ADX 与信号。

```bash
go run ./cmd/stockdb backtest --start 2026-06-01
go run ./cmd/stockdb backtest-portfolio --start 2026-06-01 --capital 100000 --max-positions 5
```

- 单信号回测末尾的「行情中断」笔数，和组合回测退出原因里的「出池平仓」占比，就是旧版漏算的部分，记下来作对照。
- 组合回测的佣金默认万 3，但没有命令行参数可改；国泰实际费率是万 0.854（`docs/monthly-review/2026-08.md` §五.3）。结果略偏保守。
- 结果仍是**绝对收益**、样本高度重叠（见 CLAUDE.md「decision_log 表结构关键点」）。引用前先与同期全池基准对照，看有效交易日数。

### ☐ 3. 用真实热榜池做模型有效性检验

审查时只能用沪深 300 成分股做检验（工作区的库是空的）。结论是**技术面评分没有统计上可证的预测力**：

| 检验（10 日超额收益，不重叠截面 t 检验） | 均值 | t | 为正截面 |
|---|---|---|---|
| score_total 最高五分位 − 最低五分位 | +0.56% | 0.84 | 33/50 |
| coreTech 闸门（SAR+ST 多、OBV 升、MACD 柱>0） | +0.56% | 1.14 | 31/50 |
| 星级技术面（score≥65、ADX≥35、coreTech、收红） | +1.27% | 1.30 | 22/47 |
| Overbought（超买，评分对它**扣分**） | +1.29% | 1.12 | 23/49 |

Rank IC：score_total 对 5 日超额收益为 −0.017（t=−0.87），对 10 日为 −0.013（t=−0.45）。

注意：当时 ADX 还是 Wilder 口径，而且沪深 300 不是实际的热榜池，也没有包含 RS、基本面、末端降级这几层。所以要用真实库重做一次：

- **样本**：snapshot 里的真实热榜池，用第 1 项重算后的 `score_total`/`score_adj` 与各 `sig_*` 列
- **标签**：信号日后 5/10 个**全市场交易日**的收益，减去同日全池等权平均（超额收益）
- **统计**：每个交易日内算 Spearman Rank IC，以及五分位、闸门的差值；10 日标签每隔 10 个交易日取一个截面，避免窗口重叠；用截面序列做 t 检验，|t| ≥ 2 才算显著
- ⚠️ **幸存者偏差**：标的掉出热榜后 snapshot 里没有后续行情，前向收益要用 `api.FetchDailyKline` 补齐，不能直接丢弃（与 `c36325b` 修回测时的道理相同）
- ⚠️ 前复权是相对最新价倒推的，前向收益要用同一次拉取的 K 线计算，不要混用库里不同日期写入的 `close`

### ☐ 4. 根据 2、3 的结果决定是否调整模型

只在**显著**（|t| ≥ 2）时才改，以免用一组噪音替换另一组：
- 超买扣分的方向（`analysis.ScoreResult` 的 KdjWr/RSI/BIAS 项及 `ApplyPerfAdaptive`）
- 选股阈值：ADX 35/38、score 65/70、RS 60/45/80（`internal/screener` 的 `ComputeTier`）。现有阈值所依据的数据只有约 2 个月
- ADX 阈值改通达信口径后数值没变，闸门因此比以前宽松，星级候选数可能增加，留意选股表里的数量变化

## 审查中记录、尚未处理的问题

- ATR 用 Wilder RMA、BOLL 用总体标准差，与通达信（`MA(TR,14)`、样本标准差）仍有差异：ATR 平均绝对偏差约 10%，BOLL 带宽约窄 2.6%。不影响内部一致性，但和 app 对不上
- 回测里跳空低开穿过止损价时，按止损价成交（实际成交价应为开盘价），低估亏损；snapshot 没有 `open` 列，修复需要先加列
- 回测按信号当日收盘价入场，涨停封板的日子实际上买不到
- `internal/api/tencent.go` 的 `parseStocksPayload` 解析失败时静默跳过该标的，盯盘 TUI 可能少显示一只持仓且没有任何提示
