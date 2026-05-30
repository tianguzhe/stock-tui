# stock-tui

## 行情数据
- 拉行情/K线前先查 `docs/data-apis.md`(腾讯/东财/新浪接口、OHLC 字段顺序、`sh`/`sz` 前缀、`Candle` 映射都已记录)。
- `internal/api` 仅封装实时报价 `FetchStocks` 与分时 `FetchMinute`,**无日K**——日K需按 docs 自行拉取。

## 技术指标
- `indicator.Calculate([]Candle) []Result`(KDJ/MACD/RSI/WR/DMI/CMI/BIAS/CHOP);**不读取 `Candle.Volume`**;`WR` 为正值口径(**值越大越超卖**,与标准威廉符号相反)。

## 临时分析脚本
- `internal/` 仅本 module `stock-tui` 内可 import;一次性分析写 `cmd/<name>/main.go`,`go run` 后 `rm -rf cmd/<name>` 保持源码区干净。
