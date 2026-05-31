# stock-tui

## 行情数据
- 拉行情/K线前先查 `docs/data-apis.md`(腾讯/东财/新浪接口、OHLC 字段顺序、`sh`/`sz` 前缀、`Candle` 映射都已记录)。
- `internal/api` 仅封装实时报价 `FetchStocks` 与分时 `FetchMinute`,**无日K**——日K需按 docs 自行拉取。

## 技术指标
- `indicator.Calculate([]Candle) []Result`(KDJ/MACD/RSI/WR/DMI/CMI/BIAS/CHOP/ATR/BOLL/Donchian/MFI);`WR` 为正值口径(**值越大越超卖**,与标准威廉符号相反)。
- `MFI` 读取 `Candle.Volume`;其他核心价格指标不依赖成交量。ATR14 用 Wilder RMA;BOLL 为 20 日 ±2σ;Donchian 输出 20/55 日通道。

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

## 临时分析脚本
- `internal/` 仅本 module `stock-tui` 内可 import;一次性分析写 `cmd/<name>/main.go`,`go run` 后 `rm -rf cmd/<name>` 保持源码区干净。
