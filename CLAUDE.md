# stock-tui

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
- **资金**:`MFI`(0–100 有界、超买超卖)与 OBV(累计、趋势)互补;量比看量能强度。
- **择时**:`TDSequential` 与斐波那契是独立口径,可与趋势/动量交叉印证。

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

## 技术面分析 CLI
- 深度技术面分析优先用固定命令 `go run ./cmd/indicator-analyze <代码>`；不要再写一次性 `cmd/<name>/main.go`。
- `indicator-analyze` 会拉腾讯日K、处理 `qfqday/day` 回退、复用 `indicator.Calculate` / `TDSequential` / `FibRetracementOf`，并输出 SCORE、DIVERGENCE、TD、FIB、PERF 与近15日演变。
