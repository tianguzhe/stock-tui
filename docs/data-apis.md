# 行情数据接口手册

本项目用到的第三方行情接口汇总。涵盖 **腾讯(gtimg)**、**天天基金/东方财富(eastmoney)**、**新浪(sina)** 三家。

> 全部接口于 **2026-05-29** 实测可用。均为公开 HTTP GET，无需鉴权。
> 以 `600580`(沪市)、`000001`(深市)为例。

## 市场代码前缀对照

不同数据源对"沪/深市"的标记方式不同，这是跨源切换时最容易踩的坑：

| 市场 | 腾讯 / 新浪 | 东方财富 `secid` |
|------|------------|-----------------|
| 上交所 | `sh600580` | `1.600580` |
| 深交所 | `sz000001` | `0.000001` |
| 北交所 | `bj920819` | `0.920819`(北交所归 market 0,未单独实测) |

**裸码自动加前缀规则**(运行时 `main.go` 的 `normalizeCodes` 口径,跨文档以此为单一事实来源):

- 已带 `sh` / `sz` / `bj` / `hk` 前缀 → 原样放行。
- 6 位裸码**先看前两位**(只看首位会把可转债/基金/北交所混淆):
  - `11` → 沪市可转债 `sh`
  - `12` / `15` / `16` / `18` → 深市可转债 / LOF / ETF / 封基 `sz`
  - `43` / `82` / `83` / `87` / `88` / `92` → 北交所 `bj`(新三板平移 43/8x、920 新号段前两位 `92`、`82` 优先股)
- 其余回退**首位**:`6` / `5` → `sh`(沪股 / ETF);`0` / `3` → `sz`(深股 / 创业板);其它 → 默认 `sh`。

> 北交所(`bj`)代码段(`82/87/88/92/43/83` 等)必须按前两位识别,否则会漏判为沪深。对应单测见 `main_test.go` 的 `TestNormalizeCodesMarketPrefix`。

---

## 一、腾讯 (gtimg)

项目主数据源。`internal/api/tencent.go`(实时)、`internal/api/minute.go`(分时)在用。

### 1.1 实时行情

```
GET https://qt.gtimg.cn/q=sh600580,sz000001
```

- **编码 GBK**，需用 `simplifiedchinese.GBK` 解码(见 `tencent.go`)。
- 多个代码用 `,` 拼接。
- 返回形如 `v_sh600580="1~卧龙电驱~600580~39.46~..."`，字段用 `~` 分隔。

字段索引(项目 `parseStock` 已用)：

| 索引 | 含义 | 索引 | 含义 |
|------|------|------|------|
| 1 | 名称 | 31 | 涨跌额 |
| 3 | 最新价 | 32 | 涨跌幅 % |
| 4 | 昨收 | 33 | 最高 |
| 5 | 今开 | 34 | 最低 |
| 6 | 成交量(手) | 37 | 成交额(万元) |

### 1.2 日K / 周K / 月K(前复权)

```
GET https://ifzq.gtimg.cn/appstock/app/fqkline/get?param=sh600580,day,,,320,qfq
```

- `param` = `代码,周期,起始日,结束日,数量,复权`。
- 周期：`day` / `week` / `month`；起止日留空取最近 N 根。
- 复权：`qfq` 前复权 / `hfq` 后复权 / 空 不复权。
- 返回 JSON：`data.sh600580.qfqday`(周K 为 `qfqweek`)。
- **每根 K 字段顺序：`[日期, 开, 收, 高, 低, 量]`**(O,C,H,L)。

> ⚠️ 注意是 **开、收、高、低**，收在高/低之前，与新浪不同。

### 1.3 分时(分钟K)

```
GET https://ifzq.gtimg.cn/appstock/app/kline/mkline?param=sh600580,m1,,240
```

- `m1` 为 1 分钟，`240` 为根数。项目 `minute.go` 在用。

> ⚠️ 腾讯 K 线在 `ifzq.gtimg.cn/appstock/app/` 路径下。
> `web.ifzq.gtimg.cn/appstuff/...` 路径会返回 `{"code":11,"msg":"No dispatch info found"}`，勿用。

---

## 二、天天基金 / 东方财富 (eastmoney)

字段以 `f` 编号，**价格普遍放大 100 倍**(需 `/100` 还原)。

### 2.1 实时行情

```
GET https://push2.eastmoney.com/api/qt/stock/get?secid=1.600580&fields=f43,f44,f45,f46,f57,f58
```

- 需带 `User-Agent` 头，否则可能被拒。
- 常用字段：`f43` 最新价、`f44` 最高、`f45` 最低、`f46` 今开、`f57` 代码、`f58` 名称。
- **价格为整数 ×100**，如 `f43:3946` → 39.46。

### 2.2 日K / 周K / 月K

```
GET https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=1.600580&fields1=f1,f2,f3&fields2=f51,f52,f53,f54,f55,f56,f57&klt=101&fqt=1&end=20500101&lmt=320
```

- `klt`：`101` 日 / `102` 周 / `103` 月。
- `fqt`：`0` 不复权 / `1` 前复权 / `2` 后复权。
- `lmt` 取最近 N 根；`end=20500101` 取到最新。
- 返回 `data.klines`，每根为逗号分隔字符串，**字段顺序由 `fields2` 决定**：
  本例 `f51,f52,f53,f54,f55,f56,f57` = **`日期, 开, 收, 高, 低, 量, 额`**。

> ⚠️ 该接口偶发限流(返回空/连接 EOF)。批量或重试场景需加退避，必要时回退到新浪源。

---

## 三、新浪 (sina)

### 3.1 实时行情

```
GET https://hq.sinajs.cn/list=sh600580
```

- **必须带 `Referer: https://finance.sina.com.cn`**，否则 403。
- **编码 GBK**。返回 `var hq_str_sh600580="卧龙电驱,开,昨收,现价,高,低,..."`。

### 3.2 日K / 周K

```
GET https://money.finance.sina.com.cn/quotes_service/api/json_v2.php/CN_MarketData.getKLineData?symbol=sh515180&scale=240&ma=no&datalen=320
```

- **建议带 `Referer: https://finance.sina.com.cn`**。
- `scale`：`240` 日(分钟数)/ `1200` 周 / `7200` 月；亦支持 `5`/`15`/`30`/`60` 分钟。
- `datalen` 取最近 N 根；`ma=no` 不附带均线。
- 返回 JSON 数组，每根为对象：
  **`{day, open, high, low, close, volume}`**(O,H,L,C)。

> ⚠️ 新浪日K字段顺序是 **开、高、低、收**(收在最后)，与腾讯/东财的"开、收、高、低"**不一致**，跨源解析务必逐字段对应，不要按位置照搬。
> 对 ETF(如 `sh515180`、`sh518680`)同样适用，本项目分析 ETF 时即用此源。

---

## 字段顺序速查(避坑)

把同一根 K 的 OHLC 顺序并排，跨源时照此对应：

| 数据源 | K 线字段顺序 |
|--------|-------------|
| 腾讯 `fqkline` | 日期, **开, 收, 高, 低**, 量 |
| 东财 `kline`(本项目 fields2) | 日期, **开, 收, 高, 低**, 量, 额 |
| 新浪 `getKLineData` | 日期, **开, 高, 低, 收**, 量 |

> 三源的"开"都在最前，但**腾讯/东财第 3 位是"收"，新浪第 3 位是"高"**。这是最常见的解析错位来源。

---

## 接入 `indicator.Calculate` 示例

三源拉到的日K都可映射为 `indicator.Candle{High, Low, Close, Volume}` 后传入计算：

```go
// 新浪源(注意字段顺序 O,H,L,C)
candles = append(candles, indicator.Candle{
    High:   toF(b.High),
    Low:    toF(b.Low),
    Close:  toF(b.Close),
    Volume: toF(b.Volume),
})
res := indicator.Calculate(candles) // → KDJ/MACD/RSI/WR/DMI/CMI/BIAS/CHOP
```