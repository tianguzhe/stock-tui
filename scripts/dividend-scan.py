#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["requests"]
# ///
"""全市场高股息标的扫描与分层筛选（红利仓专用）。

Usage:
    uv run scripts/dividend-scan.py                    # 全流程，走缓存
    uv run scripts/dividend-scan.py --refresh          # 强制重拉全部数据
    uv run scripts/dividend-scan.py --min-yield 6      # 只看 ≥6%
    uv run scripts/dividend-scan.py --corr             # 附波动率/相关矩阵（慢，需额外拉日K）
    uv run scripts/dividend-scan.py --json out.json    # 导出终选名单

配合 docs/dividend-portfolio.md 使用：本脚本产出候选池，文档记录决策。
本仓不写入 .holdings（红利仓与技术纪律短线仓的规则方向相反），故本脚本
也不读 .holdings —— 已持有标的通过 --held 传入，仅用于报告中标注对比。

═══ 数据源与口径（关键，勿凭记忆改动）═══

1. 分红：东财数据中心 reportName=RPT_SHAREBONUS_DET
   - 字段 PRETAX_BONUS_RMB 是「每 10 股税前分红」，除以 10 才是每股
   - ASSIGN_PROGRESS 含「实施」才计入，排除预案未过会与不分配
   - 一年可能多次分红（一季/中期/三季/年报），必须按 REPORT_DATE 落在
     同一年度的记录**加总**，这才是「上年每股分红」的标准口径
   - 已验证：伊利 1.3800 / 招行 2.0160 / 平安 2.7000 与人工核对零误差

2. 行业：同站 reportName=RPT_F10_BASIC_ORGINFO，字段 EM2016（东财三级行业）

3. 现价：腾讯 qt 批量接口（qt.gtimg.cn），~字段索引 3 为现价

⚠️ **不要用 push2.eastmoney.com 的 clist + f133**：
   ① 本机对该 host 的 /api/qt/ 路径不通（根路径可达，API 路径连接被关闭）
   ② f133 口径不明——它给南山铝业 12.93%，而按年度分红/现价算是 8.85%
   自己用「年度每股分红 ÷ 现价」计算，口径完全可控。

═══ 筛选层级 ═══

L1「连续 N 年分红不下调」是最狠的一刀（实测 189 → 45），因为高股息绝大
多数是股价跌出来的或一次性的。但它有个盲区：会把「腰斩后从低点重新增长」
也算成优质（宇通客车 2020 年分红 10.0→5.0，其 no_cut 仍为 5）。故额外
输出 max_cut（9 年内最大同比降幅）用于区分「从未下调」与「曾腰斩」。

L1~L5 全部基于**已实现的历史**，年报数据最长可滞后 15 个月。中创智领
2025 年报净利 +9.14% 顺利通过 L5，而它 2026Q1 归母已 −18.06%、扣非
−35.60%（煤炭资本开支收紧）。故补两层前瞻约束：

- **L6 最近一期季报净利同比**（`RPT_LICO_FN_CPD.SJLTZ`）—— 直接拦掉
  「历史漂亮但当下已恶化」的标的
- **L7 分红 ÷ 每股经营现金流**（`MGJYXJJE`）—— 比派息率更硬：派息率对
  的是利润（可被非经常性损益粉饰），这个对的是真实现金。中创智领派息率
  51.1% 看着安全，对经营现金流却已到 **92.8%**
  ⚠️ **金融行业豁免此层**：银行/保险的经营现金流含存款吸收与贷款发放，
  量级与制造业不可比（招行 11.3%、平安 7.4%），套用会得出无意义的结论

🔴 **L6/L7 是「新标的准入」闸门，不是「已持仓清出」依据。**
   两者容错方向相反：准入宁可错杀（错过一只不亏钱），清出错了要真金白银
   承担。L6 只看同比数字，不区分「一次性外部冲击」与「结构性恶化」——
   2026Q1 海尔智家 −15.22% 与中创智领 −18.06% 在这一层长得一模一样，实际是：
   - 海尔：北美暴风雪 + 关税成本，**剔除北美后经营利润 +10% 以上**，
     中国市场与非美海外市场经营利润均增长 → 外部冲击
   - 中创智领：煤炭资本开支收紧 + 行业竞争加剧，利润占比 71% 的煤机板块
     Q1 净利 −33.72% → 结构性下滑
   已持仓的去留一律走 docs/dividend-portfolio.md 的「分红体检红线」，
   唯一硬信号是**每股分红下调**；季报同比只作为提前预警，不自动触发卖出。
"""
import argparse
import json
import math
import random
import re
import sys
import time
from collections import defaultdict
from datetime import date, datetime, timedelta
from itertools import combinations
from pathlib import Path

import requests

# ─────────────────────────── 配置 ───────────────────────────

CACHE_DIR = Path(__file__).resolve().parent.parent / "data" / "dividend-cache"
EM_BASE = "https://datacenter-web.eastmoney.com/api/data/v1/get"
TENCENT_QT = "https://qt.gtimg.cn/q="
TENCENT_KLINE = "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/newfqkline/get"

MAX_RETRY = 5          # 单请求最大重试次数
MAX_PAGES = 120        # 最大翻页数，防分页逻辑失控
PAGE_SIZE = 500
PRICE_BATCH = 50       # 腾讯批量行情每批只数
KLINE_WINDOW = 254     # 波动率/相关性的交易日窗口
TRADING_DAYS = 252     # 年化因子

# 缓存有效期：分红与行业变化慢，价格必须当日
TTL_SLOW = timedelta(days=7)
TTL_PRICE = timedelta(hours=12)

UA_POOL = [
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36",
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0",
]

_session = requests.Session()
_session.trust_env = False  # 绕开系统代理设置，走本机默认出口

# ─────────────────────────── HTTP ───────────────────────────


def _get(url: str, referer: str) -> str:
    """带指数退避的 GET。重试耗尽后抛出，不静默返回空值。"""
    last = None
    for attempt in range(MAX_RETRY):
        try:
            r = _session.get(
                url,
                headers={
                    "User-Agent": random.choice(UA_POOL),
                    "Referer": referer,
                    "Accept": "application/json, text/plain, */*",
                },
                timeout=25,
            )
            r.raise_for_status()
            return r.text
        except requests.RequestException as e:
            last = e
            time.sleep((2**attempt) * 0.5 + random.uniform(0, 0.4))
    raise RuntimeError(f"重试 {MAX_RETRY} 次仍失败：{url}\n最后错误：{last}")


def _em_pages(report: str, columns: str, flt: str, label: str):
    """翻页拉取东财数据中心报表，yield 每页的 data 列表。"""
    for pn in range(1, MAX_PAGES + 1):
        url = (f"{EM_BASE}?sortColumns=SECURITY_CODE&sortTypes=1&pageSize={PAGE_SIZE}"
               f"&pageNumber={pn}&reportName={report}&columns={columns}"
               f"{'&filter=' + flt if flt else ''}&source=WEB&client=WEB")
        payload = json.loads(_get(url, "https://data.eastmoney.com/yjfp/"))
        if not payload.get("success", True) and payload.get("message"):
            raise RuntimeError(f"{label} 接口报错：{payload['message']}")
        res = payload.get("result")
        if not res or not res.get("data"):
            return
        yield res["data"]
        if pn % 10 == 0:
            print(f"  {label} page {pn} (pages={res.get('pages')})", file=sys.stderr)
        if len(res["data"]) < PAGE_SIZE:
            return
        time.sleep(0.4)
    print(f"⚠️ {label} 达到最大翻页 {MAX_PAGES}，结果可能不完整", file=sys.stderr)


# ─────────────────────────── 缓存 ───────────────────────────


def _cached(name: str, ttl: timedelta, producer):
    """文件缓存。缓存损坏时重新生成，不静默返回坏数据。"""
    CACHE_DIR.mkdir(parents=True, exist_ok=True)
    path = CACHE_DIR / f"{name}.json"
    if path.exists():
        age = datetime.now() - datetime.fromtimestamp(path.stat().st_mtime)
        if age < ttl:
            try:
                with path.open() as f:
                    print(f"  [缓存] {name}（{age.days}天{age.seconds//3600}小时前）", file=sys.stderr)
                    return json.load(f)
            except json.JSONDecodeError as e:
                print(f"  ⚠️ 缓存 {name} 损坏（{e}），重新拉取", file=sys.stderr)
    data = producer()
    with path.open("w") as f:
        json.dump(data, f, ensure_ascii=False)
    return data


# ───────────────────────── 数据获取 ─────────────────────────


def fetch_dividends(year_from: int, year_to: int) -> dict:
    """按年度汇总每股分红（元）。返回 {code: {name, series:{year: 每股}, annual:{year:{eps,yoy,shares}}}}"""
    by_year = defaultdict(lambda: defaultdict(float))
    annual = defaultdict(dict)
    names, secucodes = {}, {}
    flt = f"(REPORT_DATE>='{year_from}-01-01')(REPORT_DATE<='{year_to}-12-31')"
    for page in _em_pages("RPT_SHAREBONUS_DET", "ALL", flt, "分红"):
        for r in page:
            code, rd = r["SECURITY_CODE"], (r.get("REPORT_DATE") or "")[:10]
            if not rd:
                continue
            names[code] = r.get("SECURITY_NAME_ABBR") or names.get(code, "")
            secucodes[code] = r.get("SECUCODE") or secucodes.get(code, "")
            bonus, prog = r.get("PRETAX_BONUS_RMB"), (r.get("ASSIGN_PROGRESS") or "")
            if bonus and "实施" in prog:
                by_year[int(rd[:4])][code] += bonus / 10.0   # 每10股 → 每股
            if rd.endswith("12-31"):                          # 年报口径的基本面
                annual[code][rd[:4]] = {"eps": r.get("BASIC_EPS"),
                                        "yoy": r.get("PNP_YOY_RATIO"),
                                        "shares": r.get("TOTAL_SHARES")}
    return {c: {"name": names[c], "secucode": secucodes.get(c, ""),
                "series": {str(y): round(by_year[y].get(c, 0.0), 4)
                           for y in range(year_from, year_to + 1)},
                "annual": annual.get(c, {})} for c in names}


def fetch_industry() -> dict:
    out = {}
    cols = "SECURITY_CODE,SECURITY_NAME_ABBR,EM2016,INDUSTRYCSRC1,PROVINCE"
    for page in _em_pages("RPT_F10_BASIC_ORGINFO", cols, "", "行业"):
        for r in page:
            out[r["SECURITY_CODE"]] = {"em2016": r.get("EM2016"),
                                       "csrc": r.get("INDUSTRYCSRC1"),
                                       "province": r.get("PROVINCE")}
    return out


def fetch_fundamentals(year: int) -> dict:
    """拉年报现金流 + 最近一期季报同比。

    两组数据用途不同：
    - 年报（{year}-12-31）的 MGJYXJJE 每股经营现金流 → 算分红覆盖率，
      须与年度分红同口径，不能用季报的累计值
    - {year+1} 年内最新一期季报的 SJLTZ 归母同比 → 前瞻预警
    """
    out = defaultdict(dict)

    cols = ("SECURITY_CODE,SECURITY_NAME_ABBR,REPORTDATE,QDATE,SJLTZ,YSTZ,"
            "MGJYXJJE,PARENT_NETPROFIT,WEIGHTAVG_ROE")
    for page in _em_pages("RPT_LICO_FN_CPD", cols,
                          f"(REPORTDATE='{year}-12-31')", "年报现金流"):
        for r in page:
            out[r["SECURITY_CODE"]]["ocf_ps"] = r.get("MGJYXJJE")
            out[r["SECURITY_CODE"]]["roe"] = r.get("WEIGHTAVG_ROE")

    # 次年至今的季报，按 REPORTDATE 取最新一期（中报披露期内 Q1/H1 会并存）
    latest: dict[str, str] = {}
    for page in _em_pages("RPT_LICO_FN_CPD", cols,
                          f"(REPORTDATE>='{year + 1}-01-01')", "最新季报"):
        for r in page:
            code, rd = r["SECURITY_CODE"], (r.get("REPORTDATE") or "")[:10]
            if not rd or rd <= latest.get(code, ""):
                continue
            latest[code] = rd
            out[code].update(q_date=r.get("QDATE"), q_np_yoy=r.get("SJLTZ"),
                             q_rev_yoy=r.get("YSTZ"))
    return dict(out)


def to_tencent(secucode: str) -> str | None:
    """000001.SZ → sz000001"""
    if not secucode or "." not in secucode:
        return None
    num, mkt = secucode.split(".")
    prefix = {"SH": "sh", "SZ": "sz", "BJ": "bj"}.get(mkt)
    return f"{prefix}{num}" if prefix else None


def fetch_prices(tcodes: list[str]) -> dict:
    out = {}
    for i in range(0, len(tcodes), PRICE_BATCH):
        batch = tcodes[i:i + PRICE_BATCH]
        try:
            txt = _get(TENCENT_QT + ",".join(batch), "https://finance.qq.com")
        except RuntimeError as e:
            print(f"  ⚠️ 行情批次 {i // PRICE_BATCH} 失败，跳过：{e}", file=sys.stderr)
            continue
        for line in txt.split(";"):
            m = re.match(r'\s*v_([a-z]{2}\d{6})="(.*)"', line)
            if not m:
                continue
            parts = m.group(2).split("~")
            if len(parts) > 3:
                try:
                    if (p := float(parts[3])) > 0:
                        out[m.group(1)] = p
                except ValueError:
                    continue
        time.sleep(0.15)
    return out


def fetch_closes(tcode: str) -> list[float]:
    """前复权日K收盘序列，用于波动率/相关性。"""
    url = f"{TENCENT_KLINE}?param={tcode},day,,,{KLINE_WINDOW + 10},qfq"
    d = json.loads(_get(url, "https://finance.qq.com"))["data"][tcode]
    rows = d.get("qfqday") or d.get("day") or []
    return [float(x[2]) for x in rows]


# ───────────────────────── 分红序列指标 ─────────────────────────


def streak_no_cut(series: dict, end_year: int) -> int:
    """从 end_year 往回数，分红未下调（≥上一年）的连续年数。"""
    n = 0
    for y in range(end_year, min(map(int, series)) , -1):
        cur, prev = series.get(str(y), 0), series.get(str(y - 1), 0)
        if cur <= 0:
            break
        if prev > 0 and cur < prev - 1e-9:
            break
        n += 1
        if prev <= 0:
            break
    return n


def streak_growth(series: dict, end_year: int) -> int:
    n = 0
    for y in range(end_year, min(map(int, series)), -1):
        cur, prev = series.get(str(y), 0), series.get(str(y - 1), 0)
        if cur <= 0 or prev <= 0 or cur <= prev + 1e-9:
            break
        n += 1
    return n


def max_cut(series: dict) -> float:
    """9 年内最大同比降幅(%)。0 = 从未下调。用于识别「腰斩后重新增长」。"""
    years = sorted(map(int, series))
    worst = 0.0
    for y in years[1:]:
        cur, prev = series.get(str(y), 0), series.get(str(y - 1), 0)
        if prev > 0:
            worst = min(worst, (cur - prev) / prev * 100)
    return worst


# ─────────────────────────── 组合指标 ───────────────────────────


def ann_vol(closes: list[float]) -> float:
    rets = [math.log(closes[i] / closes[i - 1])
            for i in range(1, len(closes)) if closes[i - 1] > 0]
    if len(rets) < 30:
        return float("nan")
    m = sum(rets) / len(rets)
    var = sum((x - m) ** 2 for x in rets) / (len(rets) - 1)
    return math.sqrt(var * TRADING_DAYS) * 100


def correlation(a: list[float], b: list[float]) -> float:
    n = min(len(a), len(b))
    ra = [math.log(a[i] / a[i - 1]) for i in range(len(a) - n + 1, len(a))]
    rb = [math.log(b[i] / b[i - 1]) for i in range(len(b) - n + 1, len(b))]
    k = min(len(ra), len(rb))
    ra, rb = ra[-k:], rb[-k:]
    ma, mb = sum(ra) / k, sum(rb) / k
    cov = sum((ra[i] - ma) * (rb[i] - mb) for i in range(k))
    va = math.sqrt(sum((x - ma) ** 2 for x in ra))
    vb = math.sqrt(sum((x - mb) ** 2 for x in rb))
    return cov / (va * vb) if va > 0 and vb > 0 else float("nan")


# ─────────────────────────── 主流程 ───────────────────────────

def q_yoy_ok(r: dict, floor: float) -> bool:
    """最近一期季报归母同比。数据缺失一律判否——这是风控闸门，
    信息最少时必须向保护侧失败，不能因为「查不到」就放行。"""
    return r["q_np_yoy"] is not None and r["q_np_yoy"] > floor


def div_ocf_ok(r: dict, cap: float) -> bool:
    """分红对经营现金流的覆盖率。金融行业豁免——银行/保险的经营现金流
    含存款吸收与贷款发放，量级与制造业不可比（招行 11.3%、平安 7.4%）。
    非金融且数据缺失或现金流为负，判否。"""
    if r["ind1"] == "金融":
        return True
    return r["div_ocf"] is not None and r["div_ocf"] <= cap


def make_layers(args):
    return [
        ("L1 连续≥5年不下调", lambda r: r["no_cut"] >= 5),
        ("L2 近9年≥7年分红", lambda r: r["pay_years"] >= 7),
        ("L3 派息率≤100%", lambda r: r["payout"] is not None and r["payout"] <= 100),
        ("L4 市值≥100亿", lambda r: r["mcap"] is not None and r["mcap"] >= 100),
        ("L5 年报净利>-20%", lambda r: r["yoy"] is not None and r["yoy"] > -20),
        (f"L6 最新季报净利>{args.min_q_yoy:g}%", lambda r: q_yoy_ok(r, args.min_q_yoy)),
        (f"L7 分红/经营现金流≤{args.max_div_ocf:g}%", lambda r: div_ocf_ok(r, args.max_div_ocf)),
    ]


def build(args) -> list[dict]:
    y_to = args.year
    y_from = y_to - 8
    divs = _cached(f"dividends-{y_from}-{y_to}", TTL_SLOW,
                   lambda: fetch_dividends(y_from, y_to))
    inds = _cached("industry", TTL_SLOW, fetch_industry)
    fund = _cached(f"fundamentals-{y_to}", TTL_SLOW, lambda: fetch_fundamentals(y_to))

    paying = {c: e for c, e in divs.items() if e["series"].get(str(y_to), 0) > 0}
    tmap = {t: c for c, e in paying.items() if (t := to_tencent(e["secucode"]))}
    prices = _cached(f"prices-{date.today():%Y%m%d}", TTL_PRICE,
                     lambda: fetch_prices(sorted(tmap)))

    rows = []
    for t, price in prices.items():
        code = tmap.get(t)
        if not code:
            continue
        e = paying[code]
        per_share = e["series"][str(y_to)]
        a = e["annual"].get(str(y_to)) or {}
        eps, shares = a.get("eps"), a.get("shares")
        em = (inds.get(code, {}) or {}).get("em2016") or "—"
        f = fund.get(code, {})
        ocf_ps = f.get("ocf_ps")
        # 分红对经营现金流的覆盖率。ocf_ps<=0（现金流为负）记为 None 而非
        # 无穷大——负现金流下这个比率没有意义，交给 L7 按缺失处理。
        div_ocf = (per_share / ocf_ps * 100) if ocf_ps and ocf_ps > 0 else None
        rows.append({
            "code": code, "tencent": t, "name": e["name"], "price": price,
            "per_share": per_share, "yield": round(per_share / price * 100, 3),
            "series": e["series"], "annual": e["annual"],
            "no_cut": streak_no_cut(e["series"], y_to),
            "growth": streak_growth(e["series"], y_to),
            "max_cut": max_cut(e["series"]),
            "pay_years": sum(1 for v in e["series"].values() if v > 0),
            "eps": eps, "yoy": a.get("yoy"),
            "payout": (per_share / eps * 100) if eps and eps > 0 else None,
            "mcap": (shares * price / 1e8) if shares else None,
            "ind_full": em, "ind1": em.split("-")[0],
            "ocf_ps": ocf_ps, "div_ocf": div_ocf, "roe": f.get("roe"),
            "q_date": f.get("q_date"), "q_np_yoy": f.get("q_np_yoy"),
            "q_rev_yoy": f.get("q_rev_yoy"),
        })
    return sorted(rows, key=lambda r: -r["yield"])


def report(rows: list[dict], args) -> list[dict]:
    held = set(args.held.split(",")) if args.held else set()
    hi = [r for r in rows if r["yield"] >= args.min_yield]
    print(f"\n覆盖 {len(rows)} 只已分红标的｜股息率 ≥{args.min_yield}%：{len(hi)} 只")

    print("\n=== 分层筛选 ===")
    cur = hi
    print(f"  {'L0 股息率门槛':<26} → {len(cur):>4} 只")
    for name, fn in make_layers(args):
        before = cur
        cur = [r for r in cur if fn(r)]
        note = ""
        if name.startswith("L6"):
            miss = sum(1 for r in before if r["q_np_yoy"] is None)
            note = f"（其中 {miss} 只因无季报数据被拦）" if miss else ""
        elif name.startswith("L7"):
            fin = sum(1 for r in cur if r["ind1"] == "金融")
            note = f"（{fin} 只金融豁免）" if fin else ""
        print(f"  {name:<26} → {len(cur):>4} 只 {note}")

    ys = sorted(map(int, rows[0]["series"])) if rows else []
    clean = sorted([r for r in cur if r["max_cut"] >= -0.01], key=lambda x: -x["yield"])
    cut = sorted([r for r in cur if r["max_cut"] < -0.01], key=lambda x: x["max_cut"])

    def line(r, extra=""):
        tag = " ⬅持仓" if r["code"] in held else ""
        q = f"{r['q_np_yoy']:>7.1f}%" if r["q_np_yoy"] is not None else f"{'—':>8}"
        ocf = "  金融豁免" if r["ind1"] == "金融" else (
            f"{r['div_ocf']:>7.1f}%" if r["div_ocf"] is not None else f"{'—':>8}")
        return (f"{r['code']:<8}{r['name']:<9}{r['yield']:>6.2f}%{r['payout']:>6.1f}%"
                f"{r['mcap']:>7.0f}亿{r['growth']:>4}{r['yoy']:>7.1f}%{q}{ocf}{extra}"
                f"  {r['ind_full']}{tag}")

    hdr = (f"{'代码':<8}{'名称':<9}{'股息率':>7}{'派息率':>7}{'市值':>9}{'连增':>4}"
           f"{'年报净利':>9}{'季报净利':>9}{'分红/现金流':>10}")
    print(f"\n🟢 A 组 · {len(ys)} 年从未下调（{len(clean)} 只）")
    print(hdr + "  行业")
    for r in clean:
        print(line(r))
    print(f"\n🟡 B 组 · 曾下调（{len(cut)} 只）—— 「不下调 N 年」是从低点重新计起")
    print(hdr + f"{'最大降幅':>9}  行业")
    for r in cut:
        print(line(r, f"{r['max_cut']:>8.1f}%"))

    print("\n=== 行业分布 ===")
    by_ind = defaultdict(list)
    for r in cur:
        by_ind[r["ind1"]].append(r["name"])
    for k, v in sorted(by_ind.items(), key=lambda x: -len(x[1])):
        print(f"  {k:<10}{len(v):>3} 只: {'、'.join(v)}")

    if args.corr:
        print("\n=== 波动率与相关性（拉日K，较慢）===")
        targets = {r["tencent"]: r["name"] for r in cur[:args.corr_top]}
        for c in held:
            m = next((r for r in rows if r["code"] == c), None)
            if m:
                targets[m["tencent"]] = m["name"]
        closes = {}
        for t, n in targets.items():
            try:
                closes[t] = fetch_closes(t)
            except (RuntimeError, KeyError, ValueError) as e:
                print(f"  ⚠️ {n} 日K失败：{e}", file=sys.stderr)
            time.sleep(0.3)
        vols = {t: ann_vol(v) for t, v in closes.items()}
        print("  年化波动率：")
        for t, v in sorted(vols.items(), key=lambda x: x[1]):
            print(f"    {targets[t]:<9}{v:>6.1f}%")
        pairs = [(targets[a], targets[b], correlation(closes[a], closes[b]))
                 for a, b in combinations(closes, 2)]
        print("  相关性最低的 8 对（分散化价值最高）：")
        for a, b, v in sorted(pairs, key=lambda x: x[2])[:8]:
            print(f"    {a:<9}× {b:<9}{v:>7.3f}")
        if held:
            hp = [p for p in pairs if p[0] in {rr["name"] for rr in rows if rr["code"] in held}
                  and p[1] in {rr["name"] for rr in rows if rr["code"] in held}]
            if hp:
                print(f"  现有持仓两两相关均值：{sum(p[2] for p in hp) / len(hp):.3f}")

    if args.json:
        Path(args.json).write_text(json.dumps(
            [{k: v for k, v in r.items() if k != "annual"} for r in cur], ensure_ascii=False))
        print(f"\n终选 {len(cur)} 只 → {args.json}")
    return cur


def main() -> None:
    p = argparse.ArgumentParser(description="全市场高股息扫描与分层筛选")
    p.add_argument("--year", type=int, default=date.today().year - 1,
                   help="分红年度（默认去年，即最近一个已公布年报的年份）")
    p.add_argument("--min-yield", type=float, default=5.0, help="股息率门槛%%，默认 5.0")
    p.add_argument("--min-q-yoy", type=float, default=-10.0,
                   help="L6 最近一期季报归母同比下限%%，默认 −10（数据缺失判否）")
    p.add_argument("--max-div-ocf", type=float, default=80.0,
                   help="L7 分红/每股经营现金流上限%%，默认 80（金融行业豁免）")
    p.add_argument("--refresh", action="store_true", help="忽略缓存强制重拉")
    p.add_argument("--corr", action="store_true", help="附波动率与相关矩阵（慢）")
    p.add_argument("--corr-top", type=int, default=12, help="参与相关性计算的终选只数上限")
    p.add_argument("--held", default="", help="已持有代码，逗号分隔，仅用于报告标注")
    p.add_argument("--json", default="", help="终选名单导出路径")
    args = p.parse_args()

    if args.refresh and CACHE_DIR.exists():
        for f in CACHE_DIR.glob("*.json"):
            f.unlink()
        print("缓存已清空", file=sys.stderr)

    rows = build(args)
    if not rows:
        raise SystemExit("未取到任何标的，检查网络或接口是否变更")
    report(rows, args)


if __name__ == "__main__":
    main()
