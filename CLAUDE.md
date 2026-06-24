# stock-tui

## 目录结构
- `cmd/indicator-analyze` — 单标的深度技术面分析 CLI
- `cmd/stockdb` — 数据库管理（tag/history/rs-rank/backfill/backtest）
- `internal/api` — 实时行情 API 封装
- `internal/indicator` — 技术指标计算引擎
- `internal/store` — SQLite 存储层（snapshot/decision_log/instrument）
- `scripts/` — 每日更新/选股/日志生成/测试脚本
- `docs/journal/` — 每日复盘日志
- `docs/holdings-monitor/` — 持仓监控文档（含候选观察状态）

## 行情数据
- 拉行情/K线前先查 `docs/data-apis.md`(腾讯/东财/新浪接口、OHLC 字段顺序、`sh`/`sz` 前缀、`Candle` 映射都已记录)。
- `internal/api` 仅封装实时报价 `FetchStocks` 与分时 `FetchMinute`,**无日K**——日K需按 docs 自行拉取。

## 技术指标
- `indicator.Calculate([]Candle) []Result`(KDJ/MACD/RSI/WR/DMI/CMI/BIAS/CHOP/ATR/BOLL/Donchian/MFI/SAR/Keltner/SuperTrend);`WR` 为正值口径(**值越大越超卖**,与标准威廉符号相反)。
- `MFI` 读取 `Candle.Volume`;其他核心价格指标不依赖成交量。ATR14 用 Wilder RMA;BOLL 为 20 日 ±2σ;Donchian 输出 20/55 日通道。
- `SAR` 为 Wilder 抛物线转向(AF 0.02→0.20,触破翻转),输出 `Value`(止损/翻转价)、`Long`(多空 stance)、`Reversed`(本根是否刚翻转)。`Keltner` 为 EMA20±1.5×ATR20 通道,`Squeeze` = BOLL(20,2σ) 完全收进 Keltner 内(波动压缩、突破临近);`Keltner` 读取已算好的 `BOLL`,故 `Calculate` 内在 `fillBOLL` 之后填充。
- `SuperTrend` 为 ATR 通道趋势跟踪(ATR10×3),输出 `Value`(趋势线:多头=下轨支撑/空头=上轨压力)、`Long`(趋势 stance)、`Reversed`(本根是否刚翻转);比 `SAR` 更平滑、噪音更低,适合作"当前趋势态"总览。与 SAR/Keltner 同属 ATR 系趋势工具,解读时注意三者不要互相当独立证据(见下「指标分工」)。

## 指标分工(避免重复计票)
多数指标按维度高度相关。解读与评分时**每个维度只计一次票**,不要把同源指标当独立证据制造"虚假共振":
- **趋势方向/强度**:主用 `DMI`(ADX 强度 + PDI/MDI 方向)+ MA 排列;`CMI`/`CHOP` 仅作趋势效率/震荡度印证(三者相关:ADX 高≈CHOP 低≈CMI 高)。**`SAR`/`SuperTrend`/`Keltner` 同属 ATR 系趋势跟踪,方向几乎总是一致——三者一致才算趋势确认,仅作 stance 印证与移动止损参考,不叠加计分。**
- **动量/超买超卖**:`WR` 与 `KDJ` 同源(都基于 close 在 N 日 high-low 区间的位置),**勿当两个独立证据**;`RSI`(涨跌幅)、`BIAS`(乖离)口径不同可印证;`MACD` 相对独立(趋势性动量)。
- **波动/通道**:`ATR`/BOLL 带宽量波动幅度;`BOLL`(σ 带)、`Keltner`(ATR 带)、`Donchian`(极值带)是三类通道,BOLL vs Keltner 的对比正是 Squeeze 的意义。
- **资金**:`MFI`(0–100 有界、超买超卖)与 OBV(累计、趋势)互补;量比看量能强度。**MFI 定性口径**：> 80 超买 / 70–80 偏高 / 20–30 偏低 / < 20 超卖。
- **择时**:`TDSequential` 是独立口径,可与趋势/动量交叉印证。

## 分析输出口径
- 描述行情/技术面时,**优先用 app 上能看到的量化指标和具体数值**,不要用"缩量/放量"这类模糊词——用户要能在 app 上对照确认。
- 量能一律说**量比**及其数值(如"量比 < 0.8"=原"缩量","量比 > 1.5"=原"放量"),需要时附均量参考值。
- 其他模糊措辞同理:能落到指标数值(RSI、MA、KDJ-J、BIAS 等)就给数值,而不是只给定性描述。

## 分时图渲染
- 非 boss 模式图表中,价格走势必须保持**单条连续 series**;不要按昨收线/开盘线/百分比线把价格拆成红绿多条 `NaN` series,否则穿越参考线时会断线。
- 参考线(昨收、开盘、+1%/-1% 等百分比标示线)只能作为**背景层**:先放参考线 series,最后放价格 series,让价格线在相交处拥有绘制优先级。
- 写法示例:
  ```go
  priceS := minutePrices(points)
  series := [][]float64{
      baselineSeries(len(points), baseline), // 背景参考线
      priceS,                                // 连续价格线最后画
  }
  colors := []asciigraph.AnsiColor{
      asciigraph.AnsiColor(183), // 参考线
      priceColor,                // 价格线
  }
  chars := []asciigraph.CharSet{
      asciigraph.CreateCharSet("┈"),
      asciigraph.DefaultCharSet,
  }
  ```
- 后续若要添加多个关键百分比标示线,按从背景到前景排序:百分比线/昨收线/开盘线在前,价格线永远最后;测试应断言价格 series 为连续原始价格序列。

## 持仓格式与浮盈计算
- 用户描述持仓时用 `成本价*股数`（如 `8.504*1300`）；浮盈 = (今收 - 成本) × 股数。
- 脚本参数格式：`代码:成本:股数`（如 `sh601991:8.504:1300`）。
- 更新持仓时用 `股数*成本价` 格式（如 `200*46.212`），注意顺序与脚本参数相反。

## PERF 历史驱动的信号权重（核心方法论）
- 推荐/评估标的前，**先查该股自身 PERF 历史**，不用同一把尺子量所有股：
  - `PERF 趋势跟随多头` avg10 > 5%：追涨有历史依据，趋势信号优先
  - `PERF 超买反转空头` win10 < 35%：超买警报在本股历史近乎无效，可降权
  - `PERF 顶背离空头` win10 < 40%：顶背离历史无效，不以此降评级
  - 反之（超买/背离信号 win10 > 55%）：信号有效，应等回调再入场
- **PERF N 为信号边沿计数**（信号 0→1 翻转才计 1 次，连续触发日不重复计数，避免重叠前向窗口灌水）；`screen-stocks.py` 的排除/容忍判断用 **Wilson 95% 置信界**（排除型须下界>50%，"历史差"型须上界<50%），小样本自动失去否决力。
- **RS 排名是热榜池内百分位**（非全市场）：池子本身按热度准入，RS 高位叠加了双重动量选择，短期反转暴露是结构性的——排序已按 0.3*RS20+0.5*RS60+0.2*RS120 综合动量降权短期。
- 快速提取 PERF 关键字段（不看全量输出）：
  `go run ./cmd/indicator-analyze <code> 2>/dev/null | grep -E "SCORE|TD_NOW|SAR_KELT|DIVERGENCE|PERF"`
- **score_adj 口径**：`-save` 落库两个分——`score_total` 为原始固定尺评分（历史可比），`score_adj` 为 PERF 自适应分（超买/顶背离惩罚按本股历史胜率调权：win10 低于阈值减半、高于 55% ×1.5，gate 在复合超买信号上）。`screen-stocks.py` 用 `COALESCE(score_adj, score_total)` 筛选；CLI `SCORE` 行的 `adj=`/`perfadj=` 即调整分与调整量。

## 技术面分析 CLI
- 深度技术面分析优先用固定命令 `go run ./cmd/indicator-analyze <代码>`；不要再写一次性 `cmd/<name>/main.go`。
- `indicator-analyze` 会拉腾讯日K、处理 `qfqday/day` 回退、复用 `indicator.Calculate` / `TDSequential`，并输出 SCORE、DIVERGENCE、TD、PERF 与近15日演变。
- 批量落库：`go build -o /tmp/ia ./cmd/indicator-analyze && sqlite3 data/stock.db "SELECT code FROM instrument;" | xargs -I{} /tmp/ia -save {}`（预编译避免 285 次重复编译，全池约 90 秒）
- 多因子选股筛选：**已统一为 Go 实现**（类型安全、性能更优）
  - **推荐**：`go run ./cmd/stockdb screen --holdings 代码:成本:股数,...` 或快捷脚本 `./scripts/screen-stocks.sh --holdings ...`
  - 旧版 Python `scripts/screen-stocks.py` 已弃用（保留作参考实现）
  - 示例：`go run ./cmd/stockdb screen --holdings sh601991:8.504:1300,sh603256:193.752:100 --max 10 --capital 68000`
  - `--max` 默认值为**持仓数+7**（动态计算），可手动指定固定上限
  - `--dry-run` 临时查询不写 decision_log（正式落库每日一次即可）；`--capital 68000` 输出候选建议仓位（单笔风险1%/止损距离）
  - 持仓须先 `-save` 落库，否则显示"无快照数据"

## snapshot 加列同步清单（缺一即漏）
1. `internal/store/store.go`：Snapshot struct → CREATE TABLE → ALTER 容错列表 → SaveSnapshot 四子处（列名/占位符/**ON CONFLICT DO UPDATE**/参数顺序；漏 DO UPDATE 同日重跑会**静默保留旧值**）
2. `internal/store/store_test.go`：迁移测试 SELECT + round-trip 直查 SQL（`History()` 不含新列，不能靠它）+ 同日二次 save 覆盖用例
3. `scripts/screen-stocks.py` SELECT + `scripts/test_screen_stocks.py` 的 `base_row()` fixture（缺 key 全部用例 KeyError）
4. 全量重跑 `-save` 后新列才有值；重跑前备份：`cp data/stock.db data/stock.db.bak-$(date +%Y%m%d-%H%M)`

## decision_log 表结构关键点
- 字段名：`log_date`（非 `date`）、`outcome_pct`（结算涨跌幅）、`outcome_date`（结算日）、`correct`（是否正确）
- 查询示例：`SELECT log_date, code, tier, outcome_pct FROM decision_log WHERE log_date='2026-06-18';`
- 回填条件：信号日后满 10 个交易日 snapshot 数据

## snapshot 表结构关键点
- 字段名：`trade_date`（非 `date`）、`sar_long`/`supertrend_long`（非 `sar_stance`）、下划线命名（snake_case）
- 无 `low`/`high` 字段，回测/止损计算只能用 `close`（盘中最低点数据不可得）
- 数据是**逐日累积**的（每次 `-save` 仅保存当日），不是一次性回填历史——回测需完整 T+N 数据，数据不足时 `exit_date` 为空
- 查看数据范围：`sqlite3 data/stock.db "SELECT MIN(trade_date), MAX(trade_date), COUNT(DISTINCT trade_date) FROM snapshot;"`

## 回测系统
- 命令：`go run ./cmd/stockdb backtest [--start YYYY-MM-DD] [--days N] [--signals "类型1,类型2"]`（基础回测）
- 命令：`go run ./cmd/stockdb backtest-portfolio [--capital 100000] [--max-positions 5]`（组合回测）
- 表结构：`backtest_result`（单次信号结果）、`backtest_summary`（汇总统计）
- 有效结果过滤：`WHERE exit_date IS NOT NULL AND exit_date != ''`（数据不足时为空）
- 信号字段映射：`sig_trend_bull`（趋势跟随多头）、`sig_overbought`（超买）、`sig_oversold`（超卖）、`div_bull`/`div_bear`（背离）
- 每日更新脚本：`./scripts/daily-update.sh`（批量保存快照 → 更新RS → 回填决策 → 选股表，约90秒）

## Go 项目约定
- 模块名：`stock-tui`（`go.mod` 中定义，import 路径用此）
- 新增外部依赖需 `go get <package>`（如 `github.com/google/uuid`）
- Store 暴露底层连接：添加 `func (s *Store) DB() *sql.DB { return s.db }` 供其他包使用

## 测试
- Go：`go test ./...`；提交前对改动的 Go 文件跑 `gofmt -w`
- Python 筛选逻辑：`python3 scripts/test_screen_stocks.py`（自跑 print 式，无 pytest；经 importlib 加载连字符文件名，测试真实 tier/sort_key/signals 逻辑）

## 每日复盘日志
日志目录：`docs/journal/YYYY-MM-DD/journal.md`，四段结构：

| 章节 | 内容 | 填写时机 |
|------|------|---------|
| 一、昨日复盘 | 预判对比表（自动回填）、止损触发、小结 | 开盘前 |
| 二、持仓 | 持仓快照表（成本/股数/浮盈/score/TD/ADX/SAR/OBV）+ 每只2行关键信号 | 收盘后 |
| 三、明日预判 & 计划 | 预判方向 + 操作触发条件 + 止损，合一张表 | 收盘后 |
| 四、候补 & 推荐 | 候补入场条件 + 持仓置顶的选股表（`screen-stocks.sh` 生成）| 收盘后 |

**生成脚本**：`./scripts/gen-journal.sh [YYYY-MM-DD]`
- 自动从昨日 journal.md 的"三、明日预判"章节提取预判，回填至"一、昨日复盘"预判对比表
- 若文件已存在则跳过，幂等安全

**每日工作流**：
```bash
# 1. 收盘后批量更新快照（含换手率/市值/PE；预编译二进制，全池约90秒）
go build -o /tmp/ia ./cmd/indicator-analyze && sqlite3 data/stock.db \
  "SELECT code FROM instrument;" | xargs -I{} -P 1 /tmp/ia -save {}
# ⚠️ 注意：-P 4 并发会导致 SQLITE_BUSY 错误，建议用 -P 1

# 2. 计算 RS 相对强度百分位排名（横截面 ret20 排名，全量落库当日即有效）
go run ./cmd/stockdb rs-rank

# 3. 回填决策结果（信号后满 10 个交易日的 decision_log 自动结算，输出分层胜率）
go run ./cmd/stockdb backfill

# 4. 生成选股表（持仓置顶 + 优质候选，合计≤10只）
./scripts/screen-stocks.sh \
  --holdings <代码:成本:股数,...>

# 5. 生成次日日志模板（含昨日预判自动回填）
./scripts/gen-journal.sh

# 6. 填写日志：二、持仓 → 三、明日预判 → 四、候补&推荐（贴步骤4输出）
```

**日志字段速查**：
- `TD`：优先显示 countdown，无则显示 setup；snapshot 落库格式均为 `见顶/N`/`见底/N`（CLI 近15日行才用 `C顶N` 短格式）；setup `见顶/8` 次日警惕进入 countdown
- `SAR/ST`：`多/多` = SAR 多头 + SuperTrend 多头，双确认；持仓翻空时选股表显示 `⚠️SAR/ST双空` 等警示，必须执行退出纪律
- `止损价`：snapshot `sar_value` 列（批量落库后直接读取）；选股表"止损(距%)"列即此值；`--capital 总资金` 可输出候选建议仓位（单笔风险 1% / 止损距离）
- 量比口径：量比 < 0.8 / > 1.5 为阈值，描述时一律写"量比 X.X（< 0.8）"格式，不用"缩量/放量"
- 末端降级口径：乖离 `bias24/atr_pct > 4`（波动归一化）、连涨≥5日、换手 15–20% 任一触发即从推荐降为观察；市场广度（池内站上 MA20 比例）< 40% 时推荐上限减半
