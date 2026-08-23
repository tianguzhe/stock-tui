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

L1 是最狠的一刀（实测 189 → 43），因为高股息绝大多数是股价跌出来的或
一次性的。判据经过一次修正，两版的差异值得记住：

- ❌ 旧版「连续 N 年不下调」是**二元判据**：一次下调就把计数器清零，不看
  长期增长质量。实测矛盾——上海银行 no_cut=6 通过（9 年分红只增 4%，
  2019~2022 连续四年零增长），兴业银行 no_cut=2 被拦（9 年增 **64%**，
  仅 2023 年降 12.5% 且已连续两年恢复增长）。**长期增长明显更好的被拦掉。**
  它还有第二个盲区：把「腰斩后从低点重新增长」算成优质（宇通客车 2020 年
  分红 10.0→5.0，no_cut 仍为 5）。
- ✅ 现版「**近 N 年不下调 + 历史最大降幅 ≤ X%**」把「最近是否恶化」与
  「历史是否腰斩」拆成两个独立条件。近 2 年管前者（北京银行 2025 年降
  13.1% 被拦），最大降幅管后者（川恒 −100%、东阿阿胶 −79.9%、汾酒
  −77.8%、宇通 −50% 全部被拦——这些在旧版下反而合法通过）。

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
import statistics
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

# 复合排序时对分红增速的截断区间(%)。总回报 ≈ 股息率 + 分红增速，但增速
# 必须截断：芭田股份近 3 年 CAGR +265.1%（派息率 11.0%→77.3%、EPS +591%），
# 是低基数上的一次性暴涨，不截断会让它以复合分 271.6 霸占榜首，把稳定
# 增长的标的全挤到后面。下界 -10% 同理，避免单只崩塌标的把排序拉到极端。
CAGR_CLAMP = (-10.0, 15.0)

# 缓存有效期：分红与行业变化慢，价格必须当日
TTL_SLOW = timedelta(days=7)
TTL_PRICE = timedelta(hours=12)
TTL_FORECAST = timedelta(hours=12)  # 业绩预告逐日发布，7 天缓存会重新引入滞后

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
    """拉年报现金流 + 扣非口径 + 最近一期季报同比。

    三组数据用途不同：
    - 年报（{year}-12-31）的 MGJYXJJE 每股经营现金流 → 算分红覆盖率，
      须与年度分红同口径，不能用季报的累计值
    - {year} 与 {year-1} 两年的 BASIC_EPS / DEDUCT_BASIC_EPS → 算 L8 的
      扣非背离。**两年都要拉**，接口不提供扣非同比，只能自行相除
    - {year+1} 年内最新一期季报的 SJLTZ 归母同比 → 前瞻预警

    ⚠️ **扣非与归母必须同为 EPS 口径**：美的 2024 年 H 股发行摊薄股本，
    扣非总额 +15.46% 而扣非 EPS 仅 +7.9%。若拿总额同比减 EPS 同比，
    股本变动会被误读成盈利质量背离。
    """
    out = defaultdict(dict)

    cols = ("SECURITY_CODE,SECURITY_NAME_ABBR,REPORTDATE,QDATE,SJLTZ,YSTZ,"
            "MGJYXJJE,PARENT_NETPROFIT,WEIGHTAVG_ROE,BASIC_EPS,DEDUCT_BASIC_EPS")
    for page in _em_pages("RPT_LICO_FN_CPD", cols,
                          f"(REPORTDATE='{year}-12-31')", "年报现金流"):
        for r in page:
            out[r["SECURITY_CODE"]].update(
                ocf_ps=r.get("MGJYXJJE"), roe=r.get("WEIGHTAVG_ROE"),
                basic_eps=r.get("BASIC_EPS"), deduct_eps=r.get("DEDUCT_BASIC_EPS"))

    # 上年年报：仅为算扣非/归母的同比，不覆盖本年任何字段
    for page in _em_pages("RPT_LICO_FN_CPD", cols,
                          f"(REPORTDATE='{year - 1}-12-31')", "上年扣非"):
        for r in page:
            out[r["SECURITY_CODE"]].update(
                basic_eps_prev=r.get("BASIC_EPS"),
                deduct_eps_prev=r.get("DEDUCT_BASIC_EPS"))

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


def fetch_forecasts(year: int) -> dict:
    """{year+1} 年内的**业绩预告**，按报告期取最新一期。

    🔑 **补的是 L6 的一个致命盲区：业绩预告早于正式财报数月发布。**
    2026-07-15 久立特材公告中报预减 50~55%，而 `RPT_LICO_FN_CPD` 里它
    最新一期仍是 2026Q1 的 **+1.43%**——正式中报要到 8 月底才披露。只看
    季报的筛选会让这只在长达 6 周的窗口里持续显示「季报健康」。

    取 `INCREASEL`（同比增幅**下限**）作为判据：预告给的是区间，风控
    闸门一律取悲观端。久立该字段为 **−55**，与公告「下降 50%~55%」吻合。

    ⚠️ A 股仅在净利大幅变动（±50%、亏损、扭亏等）时强制预告，故**多数
    标的没有预告，这不是缺陷**——无预告即业绩无重大变动，按放行处理，
    与 q_yoy_ok 的「缺失判否」相反（那里缺失代表查不到，这里代表没有）。
    """
    out: dict[str, dict] = {}
    latest: dict[str, str] = {}
    cols = ("SECURITY_CODE,SECURITY_NAME_ABBR,NOTICE_DATE,REPORTDATE,"
            "INCREASEL,INCREASET,FORECASTTYPE,FORECASTCONTENT")
    for page in _em_pages("RPT_PUBLIC_OP_PREDICT", cols,
                          f"(REPORTDATE>='{year + 1}-01-01')", "业绩预告"):
        for r in page:
            code, rd = r["SECURITY_CODE"], (r.get("REPORTDATE") or "")[:10]
            if not rd or rd < latest.get(code, ""):
                continue
            # 同一报告期可能多次修正，NOTICE_DATE 更晚的覆盖
            if rd == latest.get(code) and (r.get("NOTICE_DATE") or "") < out[code].get("notice", ""):
                continue
            latest[code] = rd
            out[code] = {"fc_date": rd, "notice": (r.get("NOTICE_DATE") or "")[:10],
                         "fc_lower": r.get("INCREASEL"), "fc_upper": r.get("INCREASET"),
                         "fc_type": r.get("FORECASTTYPE"),
                         "fc_text": (r.get("FORECASTCONTENT") or "")[:120]}
    return out


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
    """从 end_year 往回数，分红未下调（≥上一年）的连续年数。

    ⚠️ **仅作展示，不再作为 L1 判据**——它是二元的，一次下调即清零，
    分不出「长期高增长中的一次小幅回调」与「长期停滞」。见模块 docstring。
    """
    n = 0
    for y in range(end_year, min(map(int, series)), -1):
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


def recent_no_cut(series: dict, end_year: int, years: int) -> bool:
    """最近 `years` 个年度是否均未下调。L1 的前半条，管「当下是否在恶化」。

    与 streak_no_cut 的区别：只看固定窗口，不因窗口外的一次下调而清零。
    """
    for y in range(end_year, end_year - years, -1):
        cur, prev = series.get(str(y), 0), series.get(str(y - 1), 0)
        if prev > 0 and cur < prev - 1e-9:
            return False
    return True


def max_cut(series: dict) -> float:
    """全序列内最大同比降幅(%)。0 = 从未下调。

    L1 的后半条，管「历史是否腰斩」——把「小幅回调后恢复增长」与
    「腰斩后从低点重新计数」区分开。宇通客车 2020 年 10.0→5.0 即由此拦下。
    """
    years = sorted(map(int, series))
    worst = 0.0
    for y in years[1:]:
        cur, prev = series.get(str(y), 0), series.get(str(y - 1), 0)
        if prev > 0:
            worst = min(worst, (cur - prev) / prev * 100)
    return worst


def div_cagr(series: dict, end_year: int, years: int) -> float | None:
    """近 `years` 年每股分红复合增速(%)。数据不足或首尾非正返回 None。

    🔑 **这是 L1 两个判据都盖不住的维度。** L1 的 recent_no_cut 是二元的
    （降没降）、max_cut 是极值的（有没有腰斩），两者可以同时成立而分红
    仍在缓慢阴跌——兴业银行 1.188→1.04→1.06→1.066 即是：近 2 年确实
    没降、最大降幅 12.5% 也在 15% 以内，七层全部放行，而近 3 年 CAGR
    是 **−3.5%**。

    ⚠️ **窗口取 3 年而非全序列**：兴业 9 年 CAGR 仍有 +6.4%（正！），
    远期基数会把近期恶化稀释掉。长周期健康、近周期恶化正是要抓的形态。
    """
    if years < 1:
        return None
    beg = series.get(str(end_year - years), 0)
    end = series.get(str(end_year), 0)
    if beg <= 0 or end <= 0:
        return None
    return ((end / beg) ** (1 / years) - 1) * 100


def cagr_median(series: dict, end_year: int, max_years: int = 8) -> float | None:
    """1~max_years 各窗口 CAGR 的**中位数**。数据不足返回 None。

    🔑 **单窗口对基期极度敏感，方向可以完全相反**：新华文轩 2023 年把
    派息率从 30% 一次性抬到 45%，分红 0.34→0.58（+70.6%）。以 2022 为
    基期的 3 年 CAGR 是 **+21.5%**，而跳升之后的真实速度只有 **+2.6%**。
    反向的例子是兴业：3 年 CAGR −3.5% 是把 2022 峰值当基期的产物，
    8 个窗口里 7 个为正。

    中位数对两端的一次性事件都不敏感，故用于「分红增长是否由 EPS 支撑」
    这类**趋势性**判断；`div_cagr` 的单窗口口径仍保留给 L1 与排序使用
    （那里要的正是「近周期是否恶化」的敏感性）。
    """
    vals = [c for n in range(1, max_years + 1)
            if (c := div_cagr(series, end_year, n)) is not None]
    return statistics.median(vals) if vals else None


def eps_cagr_median(annual: dict, end_year: int, max_years: int = 8) -> float | None:
    """EPS 的多窗口 CAGR 中位数，口径与 `cagr_median` 一致，用于与分红增速对比。"""
    def eps_of(y: int) -> float:
        return (annual.get(str(y)) or {}).get("eps") or 0.0

    vals = []
    for n in range(1, max_years + 1):
        beg, end = eps_of(end_year - n), eps_of(end_year)
        if beg > 0 and end > 0:
            vals.append(((end / beg) ** (1 / n) - 1) * 100)
    return statistics.median(vals) if vals else None


def _yoy_pct(cur: float | None, prev: float | None) -> float | None:
    """同比(%)。prev<=0 时返回 None——负基数的同比没有可解释的方向。"""
    if cur is None or prev is None or prev <= 0:
        return None
    return (cur / prev - 1) * 100


def parent_eps_yoy(r: dict) -> float | None:
    """归母每股收益同比(%)。与 `deduct_eps_yoy` 同口径，二者方可相减。"""
    return _yoy_pct(r.get("basic_eps"), r.get("basic_eps_prev"))


def deduct_eps_yoy(r: dict) -> float | None:
    """扣非每股收益同比(%)。"""
    return _yoy_pct(r.get("deduct_eps"), r.get("deduct_eps_prev"))


def deduct_ok(r: dict) -> bool:
    """L8：**归母正增长而扣非负增长即拦下**。数据缺失判否。

    🔑 **L1~L7 全部建立在归母口径上，主业塌陷可以完全不留痕迹地穿过。**
    新华文轩 2025 年归母 +1.53%、扣非 **−12.2%**（EPS 1.34→1.18），
    三大主业营收分别 −10.2%/−2.6%/−11.6%，靠非经常性损益把归母做成
    正增长，七层逐层放行，还以 DQ 8/9 拿到建仓清单第一大权重。

    判据刻意做成**无阈值的方向判断**（正 vs 负），不设「背离超过 N
    个百分点」——阈值需要样本拟合，而本仓候选池只有几十只。方向相反
    本身已是确定事实，不需要再校准强度。

    ⚠️ **对银行无效，且这是结构性的**：招商银行 2025 营收 +0.01%、
    拨备前利润 **−1.6%**、归母 +1.21%，靠拨备释放（覆盖率 411.98%
    →391.79%）撑住增长；而它的扣非/归母 = **100.0%**，本判据必然放行。
    银行的利润平滑发生在**经常性损益之内**，扣非扣不掉。金融股的盈利
    质量只能人工核查拨备与净息差，见 docs/dividend-portfolio.md。

    缺失判否与 L6 `q_yoy_ok` 一致——它是准入闸门，宁可错杀。
    """
    p, d = parent_eps_yoy(r), deduct_eps_yoy(r)
    if p is None or d is None:
        return False
    return not (p > 0 and d < 0)


def implied_pb(price: float, eps: float | None, roe: float | None) -> float | None:
    """由 ROE 与 EPS 反推市净率，避开额外拉一次行情接口。

    推导：ROE = EPS / BPS ⟹ BPS = EPS / ROE ⟹ PB = price × ROE / EPS。
    实测与腾讯 qt 的 PB 吻合（兴业 0.479 vs 0.47、招行 0.92 vs 0.89）。

    有了 PB，股息率就能做**三因子分解**（见 yield_driver）：
        股息率 ≡ 派息率 × ROE ÷ PB
    这个恒等式在 14 只终选上还原误差为 0.00pct。
    """
    if not eps or eps <= 0 or not roe or roe <= 0 or price <= 0:
        return None
    return price * (roe / 100) / eps


def eps_series(annual: dict, years: int) -> list[float]:
    """近 `years` 个年度的 EPS 序列（按年份升序，跳过缺失）。"""
    out = [v["eps"] for _, v in sorted(annual.items())
           if isinstance(v, dict) and v.get("eps")]
    return out[-years:]


def earnings_vol(annual: dict, years: int) -> float | None:
    """净利同比增速的标准差(%)，即**盈利波动率**。

    MSCI Quality 指数的三大因子之一（另两个是 ROE 与负债率）：盈利越
    平稳，未来分红越可预测。实测区分度极佳——银行 4~12%，而芭田股份
    **75%**、三七互娱 41%。

    🔑 **它抓的是「周期股伪装成红利股」**：周期顶的高股息率看起来与
    优质红利股毫无区别，差别只在盈利的稳定性上。
    """
    yoys = [v["yoy"] for _, v in sorted(annual.items())
            if isinstance(v, dict) and v.get("yoy") is not None][-years:]
    if len(yoys) < 3:
        return None
    m = sum(yoys) / len(yoys)
    return math.sqrt(sum((x - m) ** 2 for x in yoys) / (len(yoys) - 1))


def earnings_position(annual: dict, eps_now: float | None, years: int) -> float | None:
    """当前 EPS ÷ 近 `years` 年均值 EPS。**Shiller CAPE 的正常化思想。**

    周期股的当期盈利不能直接用来算股息率——分子（分红）会随周期回落，
    而买入时看到的高股息率是周期顶的产物。用多年均值平滑后才是可持续的
    盈利中枢。

    实测把唯一的伪装者一刀切出：芭田股份 **3.24x**（正常化股息率仅
    **2.01%**，远低于 5% 门槛），其余 13 只终选全部落在 0.98~1.33x。

    ⚠️ **对高成长公司会系统性偏高**（招行 1.16x 只是因为 EPS 九年翻倍），
    故阈值取 1.5x/2.0x 这类宽口径，不作精细判别。
    """
    hist = eps_series(annual, years)
    if not eps_now or len(hist) < 5:
        return None
    avg = sum(hist) / len(hist)
    return eps_now / avg if avg > 0 else None


def normalized_yield(r: dict, years: int) -> float | None:
    """用**均值 EPS**（而非当期）还原的股息率(%)，即周期平滑后的可持续股息率。

    ＝ 近 N 年均值EPS × 当前派息率 ÷ 现价。回答的是「如果盈利回到中枢，
    这只还剩多少股息率」——芭田由 6.50% 塌到 **2.01%**。
    """
    hist = eps_series(r["annual"], years)
    if len(hist) < 5 or not r["payout"] or r["price"] <= 0:
        return None
    return sum(hist) / len(hist) * (r["payout"] / 100) / r["price"] * 100


def payout_of(series: dict, annual: dict, year: int) -> float | None:
    """指定年度的派息率(%)。口径与 build() 中的 payout 一致（每股分红÷EPS）。

    抽成函数是为了给 div_quality 比较首尾两年的派息率变化——判断分红
    增长究竟来自利润还是来自派息率抬升。
    """
    div = series.get(str(year), 0)
    eps = (annual.get(str(year)) or {}).get("eps")
    if div <= 0 or not eps or eps <= 0:
        return None
    return div / eps * 100


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

def latest_earnings_signal(r: dict) -> tuple[float | None, str]:
    """最新的盈利方向信号，返回 (同比%, 来源)。

    **业绩预告优先于正式季报**——前者早数月发布。仅当预告报告期比季报
    报告期更新时才接管；否则沿用季报（预告是老报告期的、季报已出正式值）。
    """
    q, qd = r.get("q_np_yoy"), (r.get("q_date") or "")
    lo, fd = r.get("fc_lower"), (r.get("fc_date") or "")
    if lo is None or not fd:
        return q, "季报"
    # q_date 形如 2026Q1，转成可比的日期串
    qmap = {"Q1": "-03-31", "Q2": "-06-30", "Q3": "-09-30", "Q4": "-12-31"}
    qdate = (qd[:4] + qmap.get(qd[4:], "-01-01")) if len(qd) >= 6 else ""
    if qdate and fd <= qdate:
        return q, "季报"
    return lo, f"预告{fd[:7]}"


def q_yoy_ok(r: dict, floor: float) -> bool:
    """最近一期盈利同比（**业绩预告优先**）。

    季报缺失一律判否——这是风控闸门，信息最少时必须向保护侧失败，不能
    因为「查不到」就放行。但**预告缺失属正常**（A 股仅大幅变动才强制
    预告），此时回落到季报判断。
    """
    val, _ = latest_earnings_signal(r)
    return val is not None and val > floor


def div_ocf_ok(r: dict, cap: float) -> bool:
    """分红对经营现金流的覆盖率。金融行业豁免——银行/保险的经营现金流
    含存款吸收与贷款发放，量级与制造业不可比（招行 11.3%、平安 7.4%）。
    非金融且数据缺失或现金流为负，判否。"""
    if r["ind1"] == "金融":
        return True
    return r["div_ocf"] is not None and r["div_ocf"] <= cap


# 年度分红「已公告未实施」的识别阈值：较上年降幅超过此值即疑为口径残缺。
# 30% 取自「中期分红通常占全年三成到一半」——只剩中期的序列必然跌破它。
STALE_DIV_DROP = 30.0


def stale_dividend_suspects(rows: list[dict], year: int, min_yield: float) -> list[dict]:
    """年度分红「已公告未实施」导致 `year` 序列残缺、因而可能被误拦的标的。

    A 股多数公司的年度分红在 6~8 月才除息，而数据源按**已实施**口径统计。
    在此窗口内跑扫描，这批公司的序列只含中期分红，表现为「大幅降分红」，
    于是同时栽在 L0（股息率被低估）与 L1（近年下降 + 降幅超限）上。

    实证：格力电器 2025 年度实为 30 元/10股（中期 10 元已于 2026-01-23 除息
    ＋ 年度 20 元于 2026-04-28 公告预案待股东会审议），序列却只记 1.0 ——
    股息率由 7.28% 塌到 2.42%，最大降幅由 −42.9% 夸大为 −66.7%。

    判据取「较上年降幅 ≥STALE_DIV_DROP 且按上年分红补全后可过 min_yield」。
    ⚠️ **只标注不筛除**：真降分红与口径残缺在数据上无法区分（中远海控净利
    −37%，它的降分红很可能是真的），定性需逐只核实公告。
    """
    out = []
    for r in rows:
        prev = r["series"].get(str(year - 1), 0)
        now = r["series"].get(str(year), 0)
        price = r.get("price") or 0
        if prev <= 0 or now <= 0 or price <= 0:
            continue
        if now / prev > 1 - STALE_DIV_DROP / 100:
            continue
        # 已过门槛的无需提示；补全后仍不够门槛的也不影响筛选结果
        if r["yield"] < min_yield <= prev / price * 100:
            out.append(r)
    return sorted(out, key=lambda r: -(r["series"][str(year - 1)] / r["price"]))


# 派息率抬升多少算「靠缓冲垫买增长」。1pct 以内属正常年度波动。
PAYOUT_SHIFT_MIN = 1.0

# ── 红利质量分 DQ 的阈值（实测于 2026-08 终选 14 只，见文档「专业维度」）──
EPS_NORM_YEARS = 7      # 盈利正常化窗口，覆盖一轮完整周期
DQ_PAYOUT_MAX = 70.0    # 派息率上限：>70% 缓冲垫已薄
DQ_ROE_MIN = 10.0       # ROE 下限：银行股低于此普遍伴随 PB 深度折价
DQ_EVOL_MAX = 30.0      # 盈利波动率上限（MSCI Quality 口径）
DQ_OCF_MAX = 60.0       # 分红/经营现金流上限（非金融）
DQ_FIN_PAYOUT_MAX = 40.0  # 金融行业改用派息率，其 OCF 含存贷不可比
DQ_EPOS_WARN = 1.5      # 盈利位置：>1.5x 偏高
DQ_EPOS_FATAL = 2.0     # >2.0x 判为周期高位，当期股息率不可持续
DQ_TRAP_PB = 0.8        # 价值陷阱形态：PB 低于此
DQ_TRAP_ROE = 10.0      # 且 ROE 低于此
DQ_DIVERGE_MAX = 20.0   # 净利同比 − 营收同比 的背离上限(pct)
DQ_DIVERGE_REV = -10.0  # 且营收同比低于此才算红旗（营收在涨则背离无害）
PAYOUT_JUMP_MIN = 10.0  # 派息率单年跳升多少 pct 算「一次性重定」而非趋势
PAYOUT_DRIVEN_GAP = 5.0  # 分红多窗口增速超出 EPS 多窗口增速多少 pct 算抬派息率驱动


def payout_jump(series: dict, annual: dict, end_year: int,
                lookback: int = 8) -> tuple[int, float, float] | None:
    """近 `lookback` 年内**最大的一次派息率单年跳升**，返回 (年份, 前值, 后值)。

    🔑 **一次性重定与趋势性增长在分红序列上长得一模一样。** 新华文轩
    2023 年派息率 30.1%→45.3%，每股分红 0.34→0.58（+70.6%），此后
    +3.4%、+1.7%。若不识别这一跳，`div_cagr` 的 3 年窗口会读出
    **+21.5%/年** 的「高增长」——而它是一次会计政策变化，不会重演。

    只标注不筛除：抬派息率本身是股东友好的，问题只在于**别把它当成
    可外推的增长**。
    """
    best = None
    for y in range(end_year - lookback + 1, end_year + 1):
        cur, prev = payout_of(series, annual, y), payout_of(series, annual, y - 1)
        if cur is None or prev is None:
            continue
        if cur - prev > PAYOUT_JUMP_MIN and (best is None or cur - prev > best[2] - best[1]):
            best = (y, prev, cur)
    return best


def payout_driven(r: dict) -> tuple[float, float] | None:
    """分红增速显著快于 EPS 增速时返回 (分红多窗口增速, EPS多窗口增速)，否则 None。

    🔑 **补 `div_quality` 🟡 档的盲区**：那一档要求「EPS 在降」，只能抓
    三七互娱式的衰退型。而**美的（派息率 41%→74%）与海尔（27%→55%）
    EPS 都在涨**（多窗口 +8.5% / +8.8%），分红却涨 +19.8% / +20.5%
    —— 增长的一半以上来自抬派息率，EPS 在涨故 🟡 完全不触发。

    派息率有上限（伊利 75%、神华 75.6% 已近顶），靠它买来的增速必然
    收敛到 EPS 增速。两只家电龙头是同一模式的两个阶段：美的已抬到 74%
    几乎无空间，海尔 55% 尚有承诺中的 5pct。
    """
    d = cagr_median(r["series"], max(map(int, r["series"])))
    e = eps_cagr_median(r["annual"], max(map(int, r["series"])))
    if d is None or e is None or d - e <= PAYOUT_DRIVEN_GAP:
        return None
    return (d, e)


def rev_np_diverge(r: dict) -> float | None:
    """净利同比 − 营收同比(pct)。**盈利质量的前瞻信号。**

    🔑 **营收塌了而净利没塌，利润来源必然存疑**——可能是一次性收益、
    高毛利订单结构、或会计口径。它不可持续，下一期往往就是业绩拐点。

    实测久立特材 2026Q1：净利 **+1.43%** 而营收 **−29.79%**，背离
    31.2pct。三个月后（2026-07-15）中报预告净利腰斩 **−50%~−55%**。
    **信号早就在数据里**——`q_rev_yoy` 一直被拉取，只是 L6 从没用过它。

    ⚠️ 只在**营收下滑**时才是红旗：营收增长时的正背离多为经营杠杆
    （华夏银行营收 +35% / 净利 −1.5% 是反向，属另一类问题，不由此判据管）。
    """
    np_, rev = r.get("q_np_yoy"), r.get("q_rev_yoy")
    if np_ is None or rev is None:
        return None
    return np_ - rev


def yield_driver(r: dict) -> str:
    """高股息率的**主导来源**，基于恒等式 股息率 ≡ 派息率 × ROE ÷ PB。

    同样是 5%+ 的股息率，三个来源的风险性质完全不同：

    - **低 PB 驱动**：市场给净资产打折。若 ROE 也低，折价往往是**对盈利
      能力的合理定价而非错杀**——华夏 PB 0.34/ROE 8.3%、兴业 0.48/9.2%
    - **高 ROE 驱动**：真实盈利能力支撑，最健康
    - **高派息率驱动**：靠分掉更大比例的利润换来，派息率有上限，走不远
    """
    pb, roe, payout = r.get("pb"), r.get("roe"), r.get("payout")
    src = []
    if pb is not None and pb < DQ_TRAP_PB:
        src.append(f"低PB{pb:.2f}")
    if roe is not None and roe > 15:
        src.append(f"高ROE{roe:.0f}%")
    if payout is not None and payout > DQ_PAYOUT_MAX:
        src.append(f"高派息{payout:.0f}%")
    return "＋".join(src) if src else "均衡"


def dq_score(r: dict) -> tuple[int, int, list[str]]:
    """红利质量分（仿 Piotroski F-Score 的**等权加总**），返回 (得分, 满分, 未通过项)。

    等权是刻意的：加权需要拟合历史收益，而本仓样本只有几十只、持有期
    以年计，任何权重都是过拟合。等权加总可解释、可手算复核。

    八项各 1 分，覆盖红利投资的三支柱——**能力**（ROE、现金流覆盖）、
    **意愿**（分红增速、派息率）、**可持续性**（盈利波动、盈利位置、
    价值陷阱形态、当期盈利方向）。
    """
    fin = r["ind1"] == "金融"
    checks = {
        "分红增速": r["div_cagr"] is not None and r["div_cagr"] >= 0,
        "派息率": r["payout"] is not None and r["payout"] <= DQ_PAYOUT_MAX,
        "ROE": r["roe"] is not None and r["roe"] >= DQ_ROE_MIN,
        "盈利波动": r["evol"] is not None and r["evol"] <= DQ_EVOL_MAX,
        "季报": r["q_np_yoy"] is not None and r["q_np_yoy"] >= 0,
        "非陷阱形态": not (r["pb"] is not None and r["pb"] < DQ_TRAP_PB
                       and (r["roe"] or 0) < DQ_TRAP_ROE),
        "营收匹配": not ((d := rev_np_diverge(r)) is not None
                     and d > DQ_DIVERGE_MAX
                     and (r.get("q_rev_yoy") or 0) < DQ_DIVERGE_REV),
        "分红覆盖": ((r["payout"] is not None and r["payout"] <= DQ_FIN_PAYOUT_MAX)
                 if fin else
                 (r["div_ocf"] is not None and r["div_ocf"] <= DQ_OCF_MAX)),
        "盈利位置": r["epos"] is not None and r["epos"] <= DQ_EPOS_WARN,
    }
    return sum(checks.values()), len(checks), [k for k, v in checks.items() if not v]


def dq_alerts(r: dict) -> list[str]:
    """结构性警示。**命中不影响入选**（与 L6/L7 的准入分工一致），但意味着
    「这只的高股息率能否延续」存在可名状的机制性疑问，需在文档中单独交代。

    三条都不是「某个指标偏低」，而是**分红与其盈利基础脱钩的三种形态**。
    """
    out = []
    if r["epos"] is not None and r["epos"] > DQ_EPOS_FATAL:
        ny = r.get("norm_yield")
        tail = f"，正常化股息率仅 {ny:.2f}%" if ny is not None else ""
        out.append(f"周期高位 {r['epos']:.2f}x{tail}")
    if r["pb"] is not None and r["pb"] < DQ_TRAP_PB and (r["roe"] or 0) < DQ_TRAP_ROE:
        out.append(f"价值陷阱形态 PB{r['pb']:.2f}/ROE{r['roe']:.1f}%")
    if r["div_cagr"] is not None and r["div_cagr"] < 0:
        out.append(f"分红负增 {r['div_cagr']:+.1f}%/年")
    d = rev_np_diverge(r)
    if d is not None and d > DQ_DIVERGE_MAX and (r.get("q_rev_yoy") or 0) < DQ_DIVERGE_REV:
        out.append(f"营收背离 净利{r['q_np_yoy']:+.1f}%/营收{r['q_rev_yoy']:+.1f}%")
    if r.get("fc_lower") is not None and r["fc_lower"] < 0:
        out.append(f"业绩预告 {r['fc_date'][:7]} 同比{r['fc_lower']:+.0f}%~{r['fc_upper']:+.0f}%")
    # 下面两条需要完整的分红/EPS 序列；dq_alerts 也被只带指标字段的精简 row
    # 调用（见测试），故缺序列时静默跳过而非报错
    if r.get("series") and r.get("annual"):
        end = max(map(int, r["series"]))
        if (j := payout_jump(r["series"], r["annual"], end)):
            out.append(f"派息率跳升 {j[0]} {j[1]:.0f}%→{j[2]:.0f}%")
        if (pd_ := payout_driven(r)):
            out.append(f"分红增速靠抬派息率（分红{pd_[0]:+.1f}%/EPS{pd_[1]:+.1f}%）")
    return out


def div_quality(r: dict, end_year: int, years: int) -> tuple[str, str]:
    """分红增长的**来源**分解，返回 (标记, 原因)。仅标注，不参与筛选。

    依据近似分解「分红增速 ≈ EPS 增速 + 派息率增速」，分三档：

    - 🔴 分红实际在降（CAGR < 0）——L1 放行的缓慢阴跌，如兴业 −3.5%/年
    - 🟡 分红在涨但 **EPS 在降且派息率在抬**——增长来自缓冲垫而非利润。
      比 🔴 更隐蔽：三七互娱分红 3 年 +8.8%、9 年从未下调、稳居 A 组，
      而 EPS 同期 −1.5%、派息率 59.7%→78.0%。派息率是有上限的，靠它
      买来的增长不可持续。
    - 🟢 利润驱动，或派息率抬升有 EPS 增长支撑

    🔴/🟡 **不构成卖出依据**——红利仓的唯一硬信号是每股分红下调
    （见 docs/dividend-portfolio.md「分红体检红线」）。这里只回答
    「同样 5.9% 的股息率，哪一只更可能在三年后还是 5.9%」。
    """
    cagr = r.get("div_cagr")
    if cagr is None:
        return "—", "分红序列不足"
    if cagr < 0:
        return "🔴", f"分红{years}年负增 {cagr:+.1f}%/年"
    eps_beg = (r["annual"].get(str(end_year - years)) or {}).get("eps")
    eps_end = (r["annual"].get(str(end_year)) or {}).get("eps")
    pay_beg = payout_of(r["series"], r["annual"], end_year - years)
    pay_end = payout_of(r["series"], r["annual"], end_year)
    if (eps_beg and eps_end and eps_end < eps_beg
            and pay_beg is not None and pay_end is not None
            and pay_end > pay_beg + PAYOUT_SHIFT_MIN):
        return "🟡", f"派息率驱动 {pay_beg:.0f}%→{pay_end:.0f}%，EPS 同期降"
    return "🟢", "利润驱动"


def sort_score(r: dict) -> float:
    """复合排序分 ＝ 当期股息率 + 截断后的分红增速。

    只按当期股息率排会系统性地把「分红正在下滑的公司」顶到最前——它的
    分子是已实现的历史分红、分母是当前价格，价格跌得越狠排得越靠前。
    兴业以 5.87% 排全池第 3 即由此而来，而它分红三年 −3.5%/年。

    CAGR 缺失按 0 计（中性），**不套用 q_yoy_ok 的「缺失判否」原则**——
    那是风控闸门，此处只是排序，判否会把数据不全的正常标的误沉到底。
    """
    lo, hi = CAGR_CLAMP
    cagr = r.get("div_cagr")
    return r["yield"] + (0.0 if cagr is None else max(lo, min(hi, cagr)))


def make_layers(args):
    return [
        (f"L1 近{args.recent_years:g}年不降+降幅≤{abs(args.max_cut):g}%",
         lambda r: r["recent_ok"] and r["max_cut"] >= -abs(args.max_cut)
         and r["per_share"] > 0),
        ("L2 近9年≥7年分红", lambda r: r["pay_years"] >= 7),
        ("L3 派息率≤100%", lambda r: r["payout"] is not None and r["payout"] <= 100),
        ("L4 市值≥100亿", lambda r: r["mcap"] is not None and r["mcap"] >= 100),
        ("L5 年报净利>-20%", lambda r: r["yoy"] is not None and r["yoy"] > -20),
        (f"L6 最新季报净利>{args.min_q_yoy:g}%", lambda r: q_yoy_ok(r, args.min_q_yoy)),
        (f"L7 分红/经营现金流≤{args.max_div_ocf:g}%", lambda r: div_ocf_ok(r, args.max_div_ocf)),
        ("L8 扣非不得负增长", deduct_ok),
    ]


def build(args) -> list[dict]:
    y_to = args.year
    y_from = y_to - 8
    divs = _cached(f"dividends-{y_from}-{y_to}", TTL_SLOW,
                   lambda: fetch_dividends(y_from, y_to))
    inds = _cached("industry", TTL_SLOW, fetch_industry)
    # v2：2026-08 增加 BASIC_EPS/DEDUCT_BASIC_EPS（L8 扣非背离）。版本后缀让
    # 旧缓存自动作废——靠 TTL 自然过期会让 L8 在最长 7 天内静默按缺失判否。
    fund = _cached(f"fundamentals-{y_to}-v2", TTL_SLOW, lambda: fetch_fundamentals(y_to))
    # 业绩预告逐日发布，缓存不能用 7 天——那正是本判据要消除的滞后
    fcst = _cached(f"forecasts-{y_to + 1}", TTL_FORECAST, lambda: fetch_forecasts(y_to))

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
        fc = fcst.get(code, {})
        ocf_ps = f.get("ocf_ps")
        # 分红对经营现金流的覆盖率。ocf_ps<=0（现金流为负）记为 None 而非
        # 无穷大——负现金流下这个比率没有意义，交给 L7 按缺失处理。
        div_ocf = (per_share / ocf_ps * 100) if ocf_ps and ocf_ps > 0 else None
        rows.append({
            "code": code, "tencent": t, "name": e["name"], "price": price,
            "per_share": per_share, "yield": round(per_share / price * 100, 3),
            "series": e["series"], "annual": e["annual"],
            "no_cut": streak_no_cut(e["series"], y_to),
            "recent_ok": recent_no_cut(e["series"], y_to, args.recent_years),
            "growth": streak_growth(e["series"], y_to),
            "max_cut": max_cut(e["series"]),
            "div_cagr": div_cagr(e["series"], y_to, args.cagr_years),
            "pay_years": sum(1 for v in e["series"].values() if v > 0),
            "eps": eps, "yoy": a.get("yoy"),
            "payout": (per_share / eps * 100) if eps and eps > 0 else None,
            "mcap": (shares * price / 1e8) if shares else None,
            "ind_full": em, "ind1": em.split("-")[0],
            "pb": implied_pb(price, eps, f.get("roe")),
            "evol": earnings_vol(e["annual"], EPS_NORM_YEARS),
            "epos": earnings_position(e["annual"], eps, EPS_NORM_YEARS),
            "ocf_ps": ocf_ps, "div_ocf": div_ocf, "roe": f.get("roe"),
            "q_date": f.get("q_date"), "q_np_yoy": f.get("q_np_yoy"),
            "q_rev_yoy": f.get("q_rev_yoy"),
            # L8 扣非背离用；与上面的 "eps"（来自分红接口的 annual）同为基本
            # 每股收益，但两年配对必须取自同一接口，故单独存
            **{k: f.get(k) for k in
               ("basic_eps", "deduct_eps", "basic_eps_prev", "deduct_eps_prev")},
            **{k: fc.get(k) for k in ("fc_date", "fc_lower", "fc_upper", "fc_type", "fc_text")},
        })
        # norm_yield 依赖同一行的 payout，故在字典构造完成后补算
        rows[-1]["norm_yield"] = normalized_yield(rows[-1], EPS_NORM_YEARS)
    return sorted(rows, key=lambda r: -r["yield"])


def report(rows: list[dict], args) -> list[dict]:
    held = set(args.held.split(",")) if args.held else set()
    hi = [r for r in rows if r["yield"] >= args.min_yield]
    print(f"\n覆盖 {len(rows)} 只已分红标的｜股息率 ≥{args.min_yield}%：{len(hi)} 只")

    print("\n=== 分层筛选 ===")
    cur = hi
    stale = stale_dividend_suspects(rows, args.year, args.min_yield)
    note0 = f"⚠️ 另有 {len(stale)} 只疑因年度分红未实施而低估" if stale else ""
    print(f"  {'L0 股息率门槛':<26} → {len(cur):>4} 只 {note0}")
    for name, fn in make_layers(args):
        before = cur
        cur = [r for r in cur if fn(r)]
        note = ""
        if name.startswith("L6"):
            miss = sum(1 for r in before if r["q_np_yoy"] is None)
            byfc = [r for r in before if r not in cur
                    and latest_earnings_signal(r)[1].startswith("预告")]
            note = f"（其中 {miss} 只因无季报数据被拦）" if miss else ""
            if byfc:
                note += f" 🔴 {len(byfc)} 只由业绩预告拦下：" + "、".join(
                    f"{r['name']}({r['fc_lower']:+.0f}%)" for r in byfc[:5])
        elif name.startswith("L7"):
            fin = sum(1 for r in cur if r["ind1"] == "金融")
            note = f"（{fin} 只金融豁免）" if fin else ""
        elif name.startswith("L8"):
            # 缺数据被拦与真背离被拦必须分开报：前者是数据问题（大量出现说明
            # 拉取有误），后者才是判据在起作用。混在一起无法判断闸门是否健康。
            blocked = [r for r in before if r not in cur]
            miss = [r for r in blocked
                    if parent_eps_yoy(r) is None or deduct_eps_yoy(r) is None]
            real = [r for r in blocked if r not in miss]
            note = f"（其中 {len(miss)} 只因缺扣非数据被拦）" if miss else ""
            if real:
                note += " 🔴 " + "、".join(
                    f"{r['name']}(归母{parent_eps_yoy(r):+.1f}%/扣非{deduct_eps_yoy(r):+.1f}%)"
                    for r in real[:5])
                if len(real) > 5:
                    note += f" 等 {len(real)} 只"
        print(f"  {name:<26} → {len(cur):>4} 只 {note}")

    ys = sorted(map(int, rows[0]["series"])) if rows else []
    quality = {r["code"]: div_quality(r, args.year, args.cagr_years) for r in cur}
    n_red = sum(1 for m, _ in quality.values() if m == "🔴")
    n_yellow = sum(1 for m, _ in quality.values() if m == "🟡")
    print(f"  {'分红质量标注（不筛除）':<24} → 🔴 {n_red} 只分红负增、"
          f"🟡 {n_yellow} 只派息率驱动")

    if stale:
        print(f"\n=== ⚠️ 疑似分红口径残缺（{len(stale)} 只，**不影响筛选**）===")
        print(f"   {args.year} 年度分红「已公告未实施」时数据源只统计到中期，序列表现为"
              "大幅降分红，\n   会同时栽在 L0（股息率低估）与 L1（近年下降）上。"
              "6~8 月跑扫描时该窗口最宽。")
        print(f"   ⚠️ 真降分红与口径残缺在数据上无法区分，**定性需逐只核实公告**。")
        for r in stale[:10]:
            prev = r["series"][str(args.year - 1)]
            now = r["series"][str(args.year)]
            print(f"  {r['name']:<8} {r['yield']:>5.2f}% → 按{args.year - 1}年"
                  f"({prev:g})补全≈{prev / r['price'] * 100:>5.2f}%"
                  f"   {prev:g}→{now:g} ({now / prev - 1:+.0%})")
        if len(stale) > 10:
            print(f"  …… 另 {len(stale) - 10} 只")

    # A/B 组仍按「是否曾下调」分，但两组排序键统一为复合分：只按当期股息率
    # 排会把分红正在下滑的顶到最前（见 sort_score）。max_cut 仍作 B 组列保留。
    clean = sorted([r for r in cur if r["max_cut"] >= -0.01], key=lambda x: -sort_score(x))
    cut = sorted([r for r in cur if r["max_cut"] < -0.01], key=lambda x: -sort_score(x))

    def line(r, extra=""):
        tag = " ⬅持仓" if r["code"] in held else ""
        q = f"{r['q_np_yoy']:>7.1f}%" if r["q_np_yoy"] is not None else f"{'—':>8}"
        ocf = "  金融豁免" if r["ind1"] == "金融" else (
            f"{r['div_ocf']:>7.1f}%" if r["div_ocf"] is not None else f"{'—':>8}")
        cg = f"{r['div_cagr']:>+7.1f}%" if r["div_cagr"] is not None else f"{'—':>8}"
        mark, why = quality[r["code"]]
        s, tot, miss = dq_score(r)
        pb = f"{r['pb']:>5.2f}" if r["pb"] is not None else f"{'—':>5}"
        ev = f"{r['evol']:>5.0f}%" if r["evol"] is not None else f"{'—':>6}"
        ep = f"{r['epos']:>5.2f}x" if r["epos"] is not None else f"{'—':>6}"
        return (f"{r['code']:<8}{r['name']:<9}{r['yield']:>6.2f}%{cg}{r['payout']:>6.1f}%"
                f"{r['mcap']:>7.0f}亿{r['growth']:>4}{q}{ocf}{extra}"
                f"{pb}{ev}{ep}{s:>3}/{tot}  {mark}{r['ind_full']}{tag}")

    hdr = (f"{'代码':<8}{'名称':<9}{'股息率':>7}{f'{args.cagr_years}年增速':>8}"
           f"{'派息率':>7}{'市值':>9}{'连增':>4}"
           f"{'季报净利':>9}{'分红/现金流':>10}{'PB':>6}{'盈利σ':>7}{'盈利位置':>8}{'DQ':>5}")
    print(f"\n🟢 A 组 · {len(ys)} 年从未下调（{len(clean)} 只）")
    print(f"   按「股息率 + {args.cagr_years}年分红增速」排序，"
          f"增速截断于 [{CAGR_CLAMP[0]:g}%, {CAGR_CLAMP[1]:g}%]")
    print(hdr + "  质量  行业")
    for r in clean:
        print(line(r))
    print(f"\n🟡 B 组 · 曾小幅下调但已恢复（{len(cut)} 只）")
    print(f"   降幅在 {abs(args.max_cut):g}% 以内且近 {args.recent_years} 年未再降；"
          f"腰斩型已由 L1 拦在池外")
    print(hdr + f"{'最大降幅':>9}  质量  行业")
    for r in cut:
        print(line(r, f"{r['max_cut']:>8.1f}%"))

    print("\n=== 高股息的来源分解（股息率 ≡ 派息率 × ROE ÷ PB）===")
    print("   同样 5%+ 的股息率，三个来源风险性质完全不同：低PB＝市场给净资产打折")
    print("   （若 ROE 也低则多为合理定价而非错杀）｜高ROE＝盈利能力支撑｜高派息＝透支缓冲")
    for r in sorted(cur, key=lambda x: (x["pb"] if x["pb"] is not None else 99)):
        ny = f"{r['norm_yield']:>5.2f}%" if r.get("norm_yield") is not None else f"{'—':>6}"
        print(f"  {r['name']:<9}{r['yield']:>6.2f}% → 正常化 {ny}"
              f"   {yield_driver(r)}")

    alerts = [(r, a) for r in cur if (a := dq_alerts(r))]
    print(f"\n=== ⚠️ 结构性警示（{len(alerts)} 只，**不影响入选**，需在文档中单独交代）===")
    if not alerts:
        print("  无")
    for r, a in sorted(alerts, key=lambda x: -len(x[1])):
        print(f"  🔴 {r['name']:<9}{'；'.join(a)}")

    weak = sorted(((dq_score(r), r) for r in cur), key=lambda x: x[0][0])[:3]
    print("\n=== DQ 最低的 3 只与其失分项 ===")
    for (s, tot, miss), r in weak:
        print(f"  {r['name']:<9}{s}/{tot}  失分：{'、'.join(miss)}")

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
    p.add_argument("--recent-years", type=int, default=2,
                   help="L1 前半条：最近 N 个年度不得下调，默认 2")
    p.add_argument("--cagr-years", type=int, default=3,
                   help="分红增速与质量分解的回看年数，默认 3。"
                        "取 3 而非全序列：兴业 9 年 CAGR +6.4%% 是正的，"
                        "3 年才是 -3.5%%，远期基数会稀释近期恶化")
    p.add_argument("--max-cut", type=float, default=15.0,
                   help="L1 后半条：历史最大同比降幅上限%%，默认 15（超过视为曾腰斩）")
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
