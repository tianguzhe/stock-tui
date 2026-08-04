# 东财 API 字段映射

## push2.eastmoney.com/api/qt/stock/get — 个股基本面

```go
// StockInfo 个股东财基本面信息
type StockInfo struct {
    Code        string  // 股票代码(f57)      JSON: "600519"
    Name        string  // 股票简称(f58)      JSON: "贵州茅台"
    TotalShares float64 // 总股本(股,f84)     JSON: 1250081601.0
    FloatShares float64 // 流通股本(股,f85)    JSON: 1250081601.0
    Industry    string  // 行业(f127)         JSON: "白酒Ⅱ"
    ListedDate  string  // 上市日期 YYYYMMDD  JSON: 20010827 (int! 不可用 string)
    TotalMC     float64 // 总市值(元,f116)    JSON: 1659483325327.5
    FloatMC     float64 // 流通市值(元,f117)   JSON: 1659483325327.5
}
```

| 字段 | 含义 | JSON 类型 | Go 类型 | 注意 |
|------|------|-----------|---------|------|
| `f57` | 股票代码（裸码，如 `600519`） | `str` | `string` | |
| `f58` | 股票简称 | `str` | `string` | |
| `f84` | 总股本（股） | `float` | `float64` | 可能为 `float` 或 `int`，Go 均正常 |
| `f85` | 流通股本（股） | `float` | `float64` | |
| `f116` | 总市值（元） | `float` | `float64` | |
| `f117` | 流通市值（元） | `float` | `float64` | |
| `f127` | 行业 | `str` | `string` | |
| `f189` | **上市日期（YYYYMMDD）** | **`int`** | `string` | **⚠️ 东财返 int，Go 声明为 string 会 Unmarshal 报错。已修：内层结构体用 `float64`，赋值时 `Sprintf("%.0f")` 转 string** |

### 请求格式

```
GET https://push2.eastmoney.com/api/qt/stock/get
  ?secid=<前缀.裸码>
  &fltt=2
  &invt=2
  &fields=f57,f58,f84,f85,f116,f117,f127,f189
```

### 代码位置

- 结构体：`internal/api/eastmoney.go` — `StockInfo`
- JSON 解析：`internal/api/eastmoney.go` — `FetchStockInfo`
- 前置标记 `prefix`：`sh→1`, `sz/bj→0`

---

## push2his.eastmoney.com/api/qt/stock/kline/get — 日K线

### 11 列完整 K 线

```go
// 每行逗号 CSV，解析后填入 KlineData
type KlineData struct {
    Code       string
    Name       string
    Dates      []string
    Candles    []indicator.Candle
    Turnovers  []float64  // 换手率(小数, 0.0047=0.47%)
    Amplitudes []float64  // 振幅% = (高-低)/昨收×100
    VolRatioRT float64    // 量比(实时, proxy qt[46], 东财无)
    InsideVol  float64    // 内盘(手, proxy qt[7], 东财无)
    OutsideVol float64    // 外盘(手, proxy qt[8], 东财无)
}
```

CSV 列（`fields2=f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61`）：

| 下标 | 字段 | 含义 | JSON 格式 | Go 类型 | 映射 |
|------|------|------|-----------|---------|------|
| 0 | `f51` | 日期 | `"2026-07-15"` (str) | `string` | `dates[i]` |
| 1 | `f52` | 开盘价 | `"1203.66"` (数字字符串) |→`float64` | `candles[i].Open` |
| 2 | `f53` | 收盘价 | `"1251.06"` |→`float64` | `candles[i].Close` |
| 3 | `f54` | 最高价 | `"1256.60"` |→`float64` | `candles[i].High` |
| 4 | `f55` | 最低价 | `"1198.66"` |→`float64` | `candles[i].Low` |
| 5 | `f56` | 成交量（手） | `"71944"` |→`float64`×100→股 | `candles[i].Volume` |
| 6 | `f57` | 成交额（元） | `"8922861367.00"` |→`float64` | `candles[i].Amount` |
| 7 | `f58` | 振幅% | `"4.77"` |→`float64` | `amplitudes[i]` |
| 8 | `f59` | 涨跌幅% | `"2.98"` | 代码不读 | — |
| 9 | `f60` | 涨跌额 | `"36.18"` | 代码不读 | — |
| 10 | `f61` | 换手率% | `"0.58"` |→`float64`/100→小数 | `turnovers[i]` |

⚠️ **东财的 Amount(f57) 已是元，无需 ×10000（区别于 proxy 万元口径）。**

### 轻量换手率

`fields2=f51,f61` → 2 列 CSV，仅拉换手率用于兜底。

| 下标 | 字段 | 含义 | Go 映射 |
|------|------|------|---------|
| 0 | `f51` | 日期 | 用于 `alignEMTurnovers` 按日期对齐 |
| 1 | `f61` | 换手率% | `/100`→小数，按日期对齐到 K 线序列 |

匹配率 `≥80%` 才视为有效，否则返回 `nil` 触发 TDX 兜底。

### 请求格式

```
GET https://push2his.eastmoney.com/api/qt/stock/kline/get
  ?secid=<前缀.裸码>
  &fields1=f1,f2,f3,f4,f5,f6
  &fields2=f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61
  &klt=101        &fqt=1
  &end=20500101
  &lmt=<K线根数>
```

换手率轻量：

```
GET ?secid=...&fields1=f1,f2,f3&fields2=f51,f61&klt=101&fqt=1&end=20500101&lmt=<N>
```

### 代码位置

- 结构体：`internal/api/kline_fetch.go` — `KlineData`, `emKlineResponse`
- 解析函数：`internal/api/kline_fetch.go` — `parseEMKlines`, `alignEMTurnovers`
- 入口：`internal/api/kline_fetch.go` — `FetchEMKline`, `FetchEMTurnover`
- 字段转换工具：`parseEMFloat(s string) float64`
- 请求重试：`internal/api/eastmoney.go` — `FetchEMWithRetry`

---

## 两 API 职责对比

| 对比项 | push2 stock/get | push2his kline/get |
|-------|----------------|-------------------|
| 用途 | 单只股票实时基本面 | 日K线序列 + 换手率 |
| 数据实时性 | 实时 | 历史（end=20500101 可拉到当日） |
| 列结构 | JSON 对象，每字段独立 | CSV 字符串数组 `klines[]` |
| 反限流 | 中等 | 激进（短时多次请求 IP 被限） |
| 复用工具 | `FetchEMWithRetry` 3次指数退避 | 同上 |
| 请求频率 | 建议每请求间隔 ≥2s | 建议 ≥3s，`Client.DisabledKeepAlives: true` |

## 限流说明

- `push2his` 限流阈值较低：3-5 次/分钟的连续请求即触发 IP 级空响应（HTTP 连接正常但无 body 返回）。
- 应对代码中已有机制：
  - `emRandomUA()` — 随机 UA 池（5+ 个）
  - `emInitCookie()` — 预热 NID session cookie
  - `emFetchWithRetry` / `FetchEMWithRetry` — 3 次重试 + 指数退避（300ms→900ms）+ jitter
  - 批量 worker 使用 `DisableKeepAlives: true`（防连接复用被标记）
- 如仍被限流，可在调用链路上层增加请求间隔或降级并行度。
