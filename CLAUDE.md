# stock-tui

## 行情数据
- 拉行情/K线前先查 `docs/data-apis.md`(腾讯/东财/新浪接口、OHLC 字段顺序、`sh`/`sz` 前缀、`Candle` 映射都已记录)。
- `internal/api` 仅封装实时报价 `FetchStocks` 与分时 `FetchMinute`,**无日K**——日K需按 docs 自行拉取。

## 技术指标
- `indicator.Calculate([]Candle) []Result`(KDJ/MACD/RSI/WR/DMI/CMI/BIAS/CHOP);**不读取 `Candle.Volume`**;`WR` 为正值口径(**值越大越超卖**,与标准威廉符号相反)。

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
