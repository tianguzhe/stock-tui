# stock-tui

## 目录结构
- `cmd/indicator-analyze` — 单标的深度技术面分析 CLI（tdx.go 单独存放 TDX TCP 协议逻辑，-tdx 标志显式启用）
- `cmd/stockdb` — 数据库管理（tag/history/rs-rank/backfill/backtest）
- `internal/api` — 实时行情 API 封装
- `internal/indicator` — 技术指标计算引擎（`Calculate` 核心集 + CYQ 筹码分布 / CYC 成本均线 / TD Sequential 衍生指标）
- `internal/store` — SQLite 存储层（snapshot/decision_log/instrument）
- `scripts/` — 每日更新/选股/日志生成/测试脚本
- `docs/journal/` — 每日复盘日志
- `docs/holdings-monitor/` — 持仓监控文档（含候选观察状态）
- `docs/cyq-data-source-notes.md` — CYQ 筹码指标数据源注意事项

## 行情数据
- `internal/api` 仅封装实时报价 `FetchStocks` 与分时 `FetchMinute`,**无日K**——日K走 tdx 协议或 HTTP 自拉。
- 接口文档见 `docs/data-apis.md`(腾讯/东财/新浪 OHLC 字段顺序、`sh`/`sz`/`bj` 前缀映射、换手率字段均已更新)。

### 数据源优先级与口径（2026-07 更新）
- **默认 / `-save`**：**HTTP 前复权主力**——腾讯 **proxy `newfqkline` 前复权**为主（量单位**手**×100 转股；`row[7]`=**换手率%**→小数；`row[8]`=**成交额万元**→×10000 转元；`row[6]` 恒为 `{}` 无业务值；**振幅**不在 K 行，本地 `(H-L)/昨收×100`）。换手优先用 proxy `row[7]`，全 0 时才东财 f61 / TDX 流通股本兜底。**腾讯失败 → 东财全日 K fallback**（`push2his`，Amount 已是元 + 换手 + 振幅 f58）。东财请求需带 `Referer` + 完整 UA + 反限流重试；批量 worker 建议 `DisableKeepAlives: true`。字段布局与实测见 `docs/data-apis.md`。
- **量比 `vol_ratio`**：优先腾讯 proxy qt 实时量比（`VolRatioRT`，qt 索引 46）；`<=0` 或缺 qt（东财 fallback）时回退本地 `Volume/MA20`。两套定义接近但**非同一指标**；score/screener 用落库 `vol_ratio`。阈值口径不变（0.8 / 1.5）。
- **两套换手率（勿混）**：
  - **序列换手**（CYQ 用）：proxy `row[7]` %→小数，或东财 f61 / TDX 流通股本兜底；按日对齐 K 线。
  - **`snapshot.turnover_rate`**（落库展示）：来自 `api.FetchStocks` **实时报价**当日换手，不是 K 线序列最后一根。选股/日志读库用实时；筹码计算用序列。
- **`-tdx` 显式启用通达信 TCP 协议**(`github.com/quantbeing/tdx` v0.1.3,`FromBestHost` 自动选服务器,tdx 失败回退 HTTP)：保留精确 Amount + 本地换手率(CYQ/CYC 的 VWAP 口径),**但价格不前复权**——库本身不提供复权(唯一相关 `GetXdxrInfo` 需调用方自算,项目未用)。除权分红在不复权序列制造断崖(sh512480 2026-07-03 不复权 2.70→1.33,前复权仅 -1.4%),污染 BOLL bandwidth/ATR/SAR/CYQ 获利盘/PRY1 与回测 PERF 样本,**仅在需精确 Amount + 本地换手率时显式使用**。
- tdx 代码映射：`sh600522` → `model.MarketSH` + `"600522"`；`sz000001` → `model.MarketSZ` + `"000001"`。
- tdx 日K `GetSecurityBars` 返回**按日期升序**,`bars[0]` 最旧,`bars[len-1]` 最新。
- tdx 换手率：`GetFinanceInfo(ctx, market, code).LiutongGuben`(流通股本,单位股) → `turnover = Vol / LiutongGuben`(小数)。

## 技术指标
- `indicator.Calculate([]Candle) []Result`(KDJ/MACD/RSI/StochRSI/WR/DMI/CMI/BIAS/CHOP/ATR/BOLL/Donchian/MFI/SAR/Keltner/SuperTrend);`WR` 为正值口径(**值越大越超卖**,与标准威廉符号相反)。`StochRSI` 挂在 `Result.RSI` 旁(`StochRSI.K/D`),在 `fillRSI` 之后填充,用于 RSI6 钝化时重新展开极端区间(CLI `stochStagnation`、score、PERF 均引用)。
- `indicator.CalcCYC([]Candle) []CYCResult` 成本均线(N 日 VWAP = sum(Amount,N)/sum(Volume,N),非收盘价等权 MA):`CYC5`/`CYC13`/`CYC34`/`CYCInf`(全量历史加权)。Volume=0 该日跳出不参与加权,回退收盘价;前置依赖 `Amount`(元)/`Volume`(股数)。**纯展示指标**:仅 `cmd/indicator-analyze main.go:407` 一处调用打印 `CYC CYC5/13/34/∞` 一行,不进 score/screener/snapshot/PERF。
- `Candle` 结构体字段: `Open`、`High`、`Low`、`Close`、`Volume`(股数)、`Amount`(元)。CYQ 的 avgPrice 优先 VWAP(`Amount/Volume`),Volume=0 时回退 `(H+L)/2`——VWAP 在暴跌日比 `(H+L)/2` 低最多 1.44%,修正系统性偏差。
- `indicator.CalcCYQ([]Candle, []float64) []CYQResult` 计算筹码衍生指标(持仓成本衰减模型):
  - `WinnerClose`/`WinnerOpen`/`WinnerHigh`/`WinnerLow` = 该价格的获利盘比例(0~1)
  - `ASR` = 活动筹码(收盘±10%区间筹码量,0~100)
  - `CYQK_Open/High/Low/Close` = 博弈K线(获利盘OHLC,0~100),`CYQK_Length` = 收-开
  - `VolumeLessBigKline`(无量长阳:长度>18%且换手<3%)、`Ratio90v3`(90比3)、`IsLowPosition`(PRY1<40%)
  - 前置依赖:至少 60 根日K(推荐 250+),换手率用小数(0.0646=6.46%),价格须前复权
- `MFI` 读取 `Candle.Volume`;其他核心价格指标不依赖成交量。ATR14 用 Wilder RMA;BOLL 为 20 日 ±2σ;Donchian 输出 20/55 日通道。
- `SAR` 为 Wilder 抛物线转向(AF 0.02→0.20,触破翻转),输出 `Value`(止损/翻转价)、`Long`(多空 stance)、`Reversed`(本根是否刚翻转)。`Keltner` 为 EMA20±1.5×ATR20 通道,`Squeeze` = BOLL(20,2σ) 完全收进 Keltner 内(波动压缩、突破临近);`Keltner` 读取已算好的 `BOLL`,故 `Calculate` 内在 `fillBOLL` 之后填充。
- `SuperTrend` 为 ATR 通道趋势跟踪(ATR10×3),输出 `Value`(趋势线:多头=下轨支撑/空头=上轨压力)、`Long`(趋势 stance)、`Reversed`(本根是否刚翻转);比 `SAR` 更平滑、噪音更低,适合作"当前趋势态"总览。与 SAR/Keltner 同属 ATR 系趋势工具,解读时注意三者不要互相当独立证据(见下「指标分工」)。

多数指标按维度高度相关。解读与评分时**每个维度只计一次票**,不要把同源指标当独立证据制造"虚假共振":
- **趋势方向/强度**:主用 `DMI`(ADX 强度 + PDI/MDI 方向)+ MA 排列;`CMI`/`CHOP` 仅作趋势效率/震荡度印证(三者相关:ADX 高≈CHOP 低≈CMI 高)。**`SAR`/`SuperTrend`/`Keltner` 同属 ATR 系趋势跟踪,方向几乎总是一致——三者一致才算趋势确认,仅作 stance 印证与移动止损参考,不叠加计分。**
- **SAR vs SuperTrend 止损口径(不同翻空节奏,日志/选股统一用此标准)**:`SAR`(抛物线、AF 0.02→0.20 紧贴价格)**翻空最早、最灵敏**,宜作移动止损;`SuperTrend`(ATR×3 通道、翻转慢)**滞后于 SAR**,宜作大趋势 stance 总览。**只盯 ST 止损会扛过整段下跌**(SAR 先报警、ST 慢承认)。正确用法:
  - **移动止损用 SAR**(贴身,破位即跑,回撤小)
  - **趋势总览用 ST**(确认大 stance,不易被洗出)
  - **SAR+ST 双空 + score 弱 → 硬清仓**(2026-07-02 工业富联/中天科技:SAR 早于 6/11 已空、ST 拖到 7/2 才翻,双空 + score≤44)
  - **SAR 翻空但 ST 仍多 → 高危观察**(圣泉 6/29 SAR 翻空、ST 仍多):跌破 ST 下轨值才彻底清仓;反弹则 SAR/ST 谁先翻转谁说了算
  - **SAR 翻多但 ST 仍空 → 弱势修复**:短线反弹信号,但大趋势仍空头。站上 ST 上轨值才确认趋势反转
  - **不一致时一律以 ST 判断大 stance**,SAR 作短线移动止损参考
  - CLI 近15日行 `SAR=空*`/`SAR=多*` 带 `*`=**当日刚翻转**;日志"止损价"列应区分 SAR 止损线(贴身)与 ST 趋势线(粗),不可混称
- **动量/超买超卖**:`WR` 与 `KDJ` 同源(都基于 close 在 N 日 high-low 区间的位置),**勿当两个独立证据**;`RSI`(涨跌幅)、`BIAS`(乖离)口径不同可印证;`MACD` 相对独立(趋势性动量)。`StochRSI`(K/D)= RSI6 在 14 日窗口内的位置,RSI6 钝死极端值时判别力丧失、StochRSI 重新展开该区间——见下「极端行情指标口径 → 极端超卖/超买」段。
- **波动/通道**:`ATR`/BOLL 带宽量波动幅度;`BOLL`(σ 带)、`Keltner`(ATR 带)、`Donchian`(极值带)是三类通道,BOLL vs Keltner 的对比正是 Squeeze 的意义。
- **资金**:`MFI`(0–100 有界、超买超卖,位于 `internal/indicator`)与 OBV(累计、趋势)互补;量比看量能强度。**MFI 定性口径**：> 80 超买 / 70–80 偏高 / 20–30 偏低 / < 20 超卖。**OBV 不在指标包**:实现为 `cmd/indicator-analyze main.go` 的 `obvSeries`(经典累加:收涨加量/收跌减量/平盘持平)+ `obvTrend`(近6日趋势文字"上升(净流入)"/"下降(净流出)"/"持平"),进 score 信号位 `OBVUp`、`evalBullBear`、PERF 与 CLI `OBV=` 输出;screener 的 star 分层另要求 OBV 3 日持续净流入(单日沦为 watch)。**未落 snapshot**,故选股表/回测/journal 看不到 OBV 数值,仅 CLI 运行时算。
- **择时**:`TDSequential` 是独立口径,可与趋势/动量交叉印证。

### 极端行情指标口径

**涨跌停日（单日涨跌幅 ≥9.5% 或 ≤-9.5%）**：
- KDJ/WR 在 `high==low` 时回退中性 50，RSI 正常计算→极端值（接近 100/0）
- MFI 在封板日 Volume 极小时资金流信号失真，仅作参考
- BOLL `%B` 可能突破 [0,100] 区间（>100=突破上轨，<0=跌破下轨），是趋势延续信号而非回归信号
- 量比 <0.3 在涨跌停日为"封板惜售"（极强/极弱信号），不等同于"清淡"

**连续涨跌停（近 5 日内 ≥3 天涨跌停）**：
- KDJ-J/RSI6 会钉死极端值（100+ 或 <0），此时 StochRSI 的参考价值更高（重新展开钝化区间）
- SAR 因 AF 快速加速到 0.20，涨停结束后**必然立刻翻空**——这不是趋势反转信号，而是涨停打开的机械结果。此时应以 SuperTrend（更平滑）判断大趋势 stance
- ADX 可能飙到 50+，仅代表趋势极强，不代表即将反转
- TD Sequential setup 9 在连续涨跌停中完成概率极高，不代表反转——仅作力竭预警参考
- BIAS 极端值在连续涨跌停中是正常的，`lateStagePenalty` 已在 `score_adj` 中处理

**连续下跌/上涨（近 5 日内 ≥3 天同方向，比涨跌停更常见）**：
- RSI6 可能跌至 <25（极端超卖）或 >75（极端超买），StochRSI K 可能触及 0 或 100
- StochRSI K=0 = RSI6 在 14 日窗口内处于最低位（极端超卖确认），K=100 = 最高位（极端超买确认）
- SAR 在连续下跌中 AF 持续加速，止损线紧贴价格上方；一旦反弹，SAR 会**快速翻多**（AF 大→翻转灵敏）
- BOLL bandwidth 可能扩大到 >40%（波动极端），`%B` 跌至 <10 或 >90
- 连续下跌后 DONCHIAN_BREAK bear20/bear55 = true 是**有意义的空头信号**（价格创近期新低），不是"失去区分度"

**跳空缺口（开盘价与前收盘差 >3%）**：
- ATR 因 trueRange（含与前收盘差）急剧上升 → SAR/ST 止损线突然远离价格
- 跳空后止损距离可能从 3% 突变到 15%+，之前设置的止损价失效
- BOLL 带宽急剧扩大，`%B` 突破极端值——与连续涨跌停类似，是趋势延续信号

**TD 方向冲突（setup 与 countdown 方向不同）**：
- setup 见底/N + countdown 见顶/M → "多空拉锯"：setup 显示下跌力竭，但 countdown 仍在计数顶部。以 countdown 方向为主（更长周期），setup 作为反弹预警
- setup 见顶/N + countdown 见底/M → "多空拉锯"：setup 显示上涨力竭，但 countdown 仍在计数底部。以 countdown 方向为主，setup 作为回调预警
- 冲突时降权处理，不作为强信号

**极端超卖/超买（RSI6 <25 或 >75）**：
- RSI6 <25 = 极端超卖，但**不代表必反弹**——趋势空头中可能持续低位运行
- StochRSI K=0 = RSI6 在 14 日窗口内处于最低位，是极端超卖确认；K=100 = 极端超买确认
- 极端超卖时 WR 也会接近 100（超卖），KDJ-J 可能 < -20——三者同源，只算一票
- 需结合趋势 stance 判断：SAR/ST 双空 + 极端超卖 → "下跌趋势中的超卖修复预警，需确认"；SAR/ST 翻多 + 极端超卖 → "超卖反弹信号较强"

**BOLL bandwidth 极宽（>40%）**：
- 波动极端，价格远离均线。`%B` 可能在 <10 或 >90 的极端区域
- 极宽 bandwidth 通常出现在快速下跌或跳空之后，是**波动率扩张**信号
- 极宽后可能收窄（波动率回归），但收窄不等于趋势反转——需等 SAR/ST 确认

**CHOP <30（趋势效率极高）**：
- 价格单向移动，趋势效率高。CMI 通常也会 >60
- CHOP <30 不代表趋势健康——可能因连续下跌导致（趋势效率高但方向向下）
- 需结合 PDI/MDI 方向判断：PDI>MDI + CHOP<30 = 强上升趋势；MDI>PDI + CHOP<30 = 强下降趋势

**PERF 历史好但当前不适用的情况**：
- 某信号类型历史 win10 >60%，但当前趋势方向与信号方向相反 → PERF 不适用
- 例：趋势跟随多头 win10=70%，但当前 SAR/ST 双空 → 多头信号不适用
- 此时以当前趋势为准，PERF 仅作"如果趋势反转后的预期收益"参考

**多指标交叉检验极端行情优先级**：
1. 趋势 stance：优先 SAR/ST 翻转信号（但需理解连续涨停后 SAR 必然翻空的特殊性）；SAR/ST 不一致时以 ST 判断大 stance
2. 量价关系：封板量比 vs 打开量比（封板惜售=强，打开放量=弱）；连续下跌中量比<0.8=缩量下跌（弱势延续）
3. Donchian 通道突破：突破后是否站稳（连续涨停站稳=强趋势）；跌破 Donchian 20/55 日新低是有意义的空头信号
4. TD Sequential：countdown 完成作参考但降权；setup 与 countdown 方向冲突时以 countdown 为主
5. 超买超卖指标（KDJ/RSI/WR/BIAS）：在连续涨跌停中**失去区分度**，不作为主要判断依据；在连续下跌中极端超卖（RSI6<25）是修复预警，需结合趋势 stance 确认

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
- 持仓以**手(1手=100股)**为最小单位。`.holdings` 文件格式：`代码:成本:手数`（如 `sh601138:65.490:2`）。
- 浮盈 = (今收 - 成本) × 手数 × 100。

## PERF 历史驱动的信号权重（核心方法论）
- 推荐/评估标的前，**先查该股自身 PERF 历史**，不用同一把尺子量所有股：
  - `PERF 趋势跟随多头` avg10 > 5%：追涨有历史依据，趋势信号优先
  - `PERF 超买反转空头` win10 < 35%：超买警报在本股历史近乎无效，可降权
  - `PERF 顶背离空头` win10 < 40%：顶背离历史无效，不以此降评级
  - 反之（超买/背离信号 win10 > 55%）：信号有效，应等回调再入场
- **PERF N 为信号边沿计数**（信号 0→1 翻转才计 1 次，连续触发日不重复计数，避免重叠前向窗口灌水）；Go screen(`internal/screener.WilsonBounds`)的排除/容忍判断用 **Wilson 95% 置信界**（排除型须下界>50%，"历史差"型须上界<50%），小样本自动失去否决力。
- **RS 排名是热榜池内百分位**（非全市场）：池子本身按热度准入，RS 高位叠加了双重动量选择，短期反转暴露是结构性的——排序已按 0.3*RS20+0.5*RS60+0.2*RS120 综合动量降权短期。
- **score_adj 口径**：`-save` 落库两个分——`score_total` 为原始固定尺评分（历史可比），`score_adj` 为 PERF 自适应分（超买/顶背离惩罚按本股历史胜率调权：win10 低于阈值减半、高于 55% ×1.5，gate 在复合超买信号上）。Go screen 用 `COALESCE(score_adj, score_total)` 筛选；CLI `SCORE` 行的 `adj=`/`perfadj=` 即调整分与调整量。

## 技术面分析 CLI
- 深度技术面分析优先用固定命令 `go run ./cmd/indicator-analyze <代码>`；不要再写一次性 `cmd/<name>/main.go`。
- `indicator-analyze` 数据流：
  - **纯分析模式（无 `-save` 无 `-tdx`）**：HTTP 前复权主力（腾讯 `qfq` OHLCV + 东财换手率兜底），与落库口径一致
  - **`-save` 模式**：同上，纯 HTTP 前复权（避免批量保存时每个标的 100-170ms 的 TDX 握手开销）
  - **`-tdx` 模式**（含 `-save -tdx`）：显式启用 TDX 协议（获取更精确的 Amount + 本地换手率），**价格不前复权**，除权日断崖会污染 BOLL/ATR/SAR/CYQ/PERF，仅在需要 Amount 精度时使用
- 快速提取关键字段：`go run ./cmd/indicator-analyze <code> 2>/dev/null | grep -E "SCORE|TD_NOW|SAR_KELT|DIVERGENCE|PERF|CYQ"`
- 批量落库（纯 HTTP，全池约 90 秒，串行 `-P 1` 避免 SQLITE_BUSY）：完整命令见下「每日复盘日志 → 每日工作流」步骤 1。
- 多因子选股筛选：**已统一为 Go 实现**（类型安全、性能更优）
  - **推荐**：`go run ./cmd/stockdb screen --holdings 代码:成本:手数,...` 或快捷脚本 `./scripts/screen-stocks.sh --holdings ...`
  - 旧版 Python `screen-stocks.py`/`test_screen_stocks.py` 已删除，选股与测试一律以 Go screen(`cmd/stockdb screen` + `internal/screener`)为准
  - 示例：`go run ./cmd/stockdb screen --holdings sh601138:65.490:2,sh600522:25.008:1 --max 10 --capital 68000`
  - `--max` 默认值为**持仓数+7**（动态计算），可手动指定固定上限
  - `--dry-run` 临时查询不写 decision_log（正式落库每日一次即可）；`--capital 68000` 输出候选建议仓位（单笔风险1%/止损距离）
  - 持仓须先 `-save` 落库，否则显示"无快照数据"

## snapshot 加列同步清单（缺一即漏）
1. `internal/store/store.go`：Snapshot struct → CREATE TABLE → ALTER 容错列表 → SaveSnapshot 四子处（列名/占位符/**ON CONFLICT DO UPDATE**/参数顺序；漏 DO UPDATE 同日重跑会**静默保留旧值**）
2. `internal/store/store_test.go`：迁移测试 SELECT + round-trip 直查 SQL（`History()` 不含新列，不能靠它）+ 同日二次 save 覆盖用例
3. Go screen：`internal/screener/screener.go` 的 snapshot SELECT(line 436+)与 `rows.Scan`(line 479)缺列会报错，+ `internal/screener/screener_test.go` 补对应字段断言
4. 全量重跑 `-save` 后新列才有值；重跑前备份：`cp data/stock.db data/stock.db.bak-$(date +%Y%m%d-%H%M)`

## decision_log 表结构关键点
- 字段名：`log_date`（非 `date`）、`outcome_pct`（结算涨跌幅）、`outcome_date`（结算日）、`correct`（是否正确）
- 查询示例：`SELECT log_date, code, tier, outcome_pct FROM decision_log WHERE log_date='2026-06-18';`
- 回填条件：信号日后满 10 个交易日 snapshot 数据

## snapshot 表结构关键点
- 字段名：`trade_date`（非 `date`）、`sar_long`/`supertrend_long`（非 `sar_stance`）、下划线命名（snake_case）
- 已有 `low`/`high` 字段(struct/CREATE TABLE/ALTER 容错/SaveSnapshot 四子处同步),回测盘中止损/止盈读 `COALESCE(low, close)` 与 `COALESCE(high, close)`(`internal/backtest/engine.go`、`portfolio.go` 的 `getPriceRange`),旧行缺失时回退 close;数据覆盖率约 79%(2026-06 起)
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
- 从项目外 `import "stock-tui/internal/*"` 会在 Go 1.26+ 被 `internal` 限制拒绝。解法：`/tmp/probe-xxx` 下创建独立 go.mod, 用 `replace stock-tui => /absolute/project/path` 引用; 或直接复制算法代码。

## 测试
- Go：`go test ./...`；提交前对改动的 Go 文件跑 `gofmt -w`

## 每日复盘日志
日志目录：`docs/journal/YYYY-MM-DD/journal.md`，四段结构：

| 章节 | 内容 | 填写时机 |
|------|------|---------|
| 一、昨日复盘 | 预判对比表（自动回填）、止损触发、小结 | 开盘前 |
| 二、持仓 | 持仓快照表（成本/手数/浮盈/score/TD/ADX/SAR/OBV）+ 每只2行关键信号 | 收盘后 |
| 三、明日预判 & 计划 | 预判方向 + 操作触发条件 + 止损，合一张表 | 收盘后 |
| 四、候补 & 推荐 | 候补入场条件 + 持仓置顶的选股表（`screen-stocks.sh` 生成）| 收盘后 |

**生成脚本**：`./scripts/gen-journal.sh [YYYY-MM-DD]`
- 自动从昨日 journal.md 的"三、明日预判"章节提取预判，回填至"一、昨日复盘"预判对比表
- 若文件已存在则跳过，幂等安全

**每日工作流**：
```bash
# 0. 【必须第一步】更新同花顺热榜（确保 instrument 表在批量 -save 前已更新）
./scripts/import-hot-stocks.sh

# 1. 收盘后批量更新快照（含换手率/市值/PE + CYQ 筹码衍生指标；并行 Go 进程，全量约 90 秒）
go run ./cmd/stockdb batch-save -P 4
# ⚠️ 4 线程并行拉取 + 串行 SQLite 写入（互斥锁避免 SQLITE_BUSY），
#    前复权口径与 snapshot 一致；-P N 可调并发度（默认 4，CPU 高时可用 8）。

# 2. 计算 RS 相对强度百分位排名（横截面 ret20 排名，全量落库当日即有效）
go run ./cmd/stockdb rs-rank

# 3. 回填决策结果（信号后满 10 个交易日的 decision_log 自动结算，输出分层胜率）
go run ./cmd/stockdb backfill

# 4. 生成选股表（持仓置顶 + 优质候选，合计≤持仓数+7；--max 可手动指定上限）
./scripts/screen-stocks.sh \
  --holdings <代码:成本:手数,...>

# 5. 生成次日日志模板（含昨日预判自动回填）
./scripts/gen-journal.sh

# 6. 填写日志：二、持仓 → 三、明日预判 → 四、候补&推荐（贴步骤4输出）
```

**⚠️ 流程顺序不可调整**：热榜必须第一步执行，否则后续批量保存的股票池不完整。

**日志字段速查**：
- `TD`：优先显示 countdown，无则显示 setup；snapshot 落库格式均为 `见顶/N`/`见底/N`（CLI 近15日行才用 `C顶N` 短格式）；setup `见顶/8` 次日警惕进入 countdown
- `CYQ`：CLI 输出两行——`WINNER`(获利盘%)/`ASR`(浮筹%)/`PRY1`(年度相对位置%) 与博弈K线 OHLC + 控盘信号(无量长阳/90比3/低位) + 状态标签(深度套牢/全民获利/筹码密集/近年底位)
- `SAR/ST`：`多/多` = SAR 多头 + SuperTrend 多头，双确认；持仓翻空时选股表显示 `⚠️SAR/ST双空` 等警示，必须执行退出纪律
- `止损价`：选股表"止损(距%)"列口径——**SAR 在现价下方(多头)时用 `sar_value`(贴身移动止损);SAR 翻到现价上方(空头/双空)时其线是反手位非卖出止损,回退近 20 日最低价(`low20`,跌破前低就跑)**;两者皆无则显「—」。`--capital 总资金` 按此止损线输出候选建议仓位(单笔风险 1% / 止损距离),止损线失效(无 Low20)时输出「止损距离过宽,建议观望」。
- 量比口径：量比 < 0.8 / > 1.5 为阈值，描述时一律写"量比 X.X（< 0.8）"格式，不用"缩量/放量"
- 末端降级口径：乖离 `bias24/atr_pct > 4`（波动归一化）、连涨≥5日、换手率≥15%（`tr >= 15`,15–20% 闭区间含端点）任一触发即从推荐降为观察；市场广度（池内站上 MA20 比例）< 40% 时推荐上限减半
