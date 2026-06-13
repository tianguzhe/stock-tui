#!/usr/bin/env python3
"""多因子选股筛选器 — 持仓 + 优质候选，输出可直接贴入日志"四、候补&推荐"的 Markdown 表格。

用法：
  python3 scripts/screen-stocks.py [data/stock.db] \\
      --holdings sh601991:8.504:1300,sh603256:193.752:100,sh605589:53.176:200

  持仓格式：代码:成本价:股数（逗号分隔）

候选筛选（不凑数，只出真正优质的）：
  ⭐⭐⭐  红盘/平盘 + score≥70 + ADX≥38 + SAR/ST双多 + OBV净流入 + MACD hist>0 +
         TD非C顶或C顶≤6 + 顶背离需充分PERF验证(N≥10且win10<50%) +
         无末端追高风险
  ⭐⭐   红盘/平盘 + score≥65 + ADX≥35 + 同⭐⭐⭐技术条件
  👁️观察 回踩 + score≥65 + ADX≥35 + 同⭐⭐⭐技术条件，或强势但已有末端风险的红盘票
         (强势股RS≥80且score≥65: -5%~0%且量比<1.5；一般股: -2%~0%且量比<1.0)
  优先展示 ⭐⭐⭐，其次 ⭐⭐，最后 👁️观察，合计不超过 (10 - 持仓数)，宁少勿滥

动量与末端风险（金融学口径）：
  - 动量门槛 rs20≥60 + rs60≥45（20日处学术短期反转区，需中期印证）；
    排序首键为综合动量 0.3*rs20+0.5*rs60+0.2*rs120
  - 末端追高降级：放量大涨/背离叠加之外，乖离 bias24/atr_pct>4（波动归一化）、
    连涨≥5日、换手 15–20%（与 >20 排除成梯度）任一触发即降为观察
  - 市场广度闸门：池内 <40% 站上 MA20 时推荐上限减半（动量崩溃保护）

硬性过滤（PERF 历史验证，Wilson 95% 置信界口径，自动吃掉小样本水分）：
  - 趋势跟随多头胜率 Wilson 上界 < 50%：排除（追涨显著差于抛硬币）
  - 趋势跟随多头 n≥10 且 avg10 ≤ 0%：排除（追涨平均不赚钱）
  - 当前触发复合超买 且 超买反转胜率 Wilson 下界 > 50%：排除（信号显著有效，等回调）
  - 顶背离三态：胜率下界>50% 排除；无样本降级观察；不显著+强趋势(ADX≥38+双多)容忍

score 口径：优先读 PERF 自适应调整分 score_adj（indicator-analyze 落库），旧快照无该列时回退 score_total。
"""
from __future__ import annotations

import argparse
import datetime as _dt
import math
import re
import sqlite3
import sys
from collections.abc import Sequence


MIN_RS_COVERAGE = 90


def _wilson_bounds(win_pct: float, n: int, z: float = 1.96) -> tuple[float, float]:
    """胜率的 Wilson 95% 置信区间（下界%, 上界%）。

    吃掉小样本水分：N=10、win=40% 的下界仅 ~17%、上界 ~69%——统计上
    等于没说。规则口径：排除型判断用下界>50（信号显著优于抛硬币才有
    资格否决候选）；"历史差"型判断用上界<50（显著差于抛硬币才排除）。
    边沿触发去灌水后 N 普遍缩水 3-5 倍，固定 N 阈值会失效，Wilson 随
    样本量自动平滑退化。
    """
    if not n:
        return 0.0, 100.0
    p = win_pct / 100
    denom = 1 + z * z / n
    centre = p + z * z / (2 * n)
    margin = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return (centre - margin) / denom * 100, (centre + margin) / denom * 100


def parse_holdings(raw: str) -> list[tuple[str, float, int]]:
    holdings = []
    for item in raw.split(","):
        item = item.strip()
        if not item:
            continue
        parts = item.split(":")
        code = parts[0].strip()
        cost = float(parts[1]) if len(parts) > 1 else 0.0
        shares = int(parts[2]) if len(parts) > 2 else 0
        holdings.append((code, cost, shares))
    return holdings


def load_snapshots(db_path: str) -> tuple[str, dict[str, sqlite3.Row], float]:
    con = sqlite3.connect(db_path)
    con.row_factory = sqlite3.Row
    try:
        date = con.execute("SELECT MAX(trade_date) FROM snapshot").fetchone()[0]
        rs_coverage = con.execute("""
            SELECT COUNT(*) * 100.0 / NULLIF((SELECT COUNT(*) FROM snapshot WHERE trade_date = ?), 0)
            FROM snapshot WHERE trade_date = ? AND rs20 IS NOT NULL
        """, (date, date)).fetchone()[0] or 0

        snap = {r["code"]: r for r in con.execute("""
        SELECT i.code, i.name, i.hot_score,
               COALESCE(s.score_adj, s.score_total) AS score_total,
               s.adx, s.change_pct, s.close,
               s.sar_long, s.supertrend_long, s.obv_up,
               s.macd_hist, s.vol_ratio,
               s.td_setup, s.td_countdown,
               s.div_bear, s.sig_overbought,
               COALESCE(s.turnover_rate, 0) AS turnover_rate,
               COALESCE(s.market_cap, 0)    AS market_cap,
               COALESCE(s.pe, 0)            AS pe,
               s.rs20, s.rs60, s.rs120,
               s.bias24, s.atr_pct, s.streak, s.ma20,
               s.perf_trend_follow_bull_win10,
               s.perf_overbought_bear_win10,
               s.perf_div_bear_win10,
               s.perf_trend_follow_bull_n,
               s.perf_overbought_bear_n,
               s.perf_div_bear_n,
               s.perf_trend_follow_bull_avg10,
               COALESCE(s.keltner_squeeze, 0)    AS keltner_squeeze,
               COALESCE(s.donch_break20_bull, 0) AS donch_break20_bull,
               COALESCE(s.donch_break55_bull, 0) AS donch_break55_bull,
               s.sar_value, s.supertrend_value
        FROM snapshot s JOIN instrument i ON s.code = i.code
        WHERE s.trade_date = ?
        """, (date,)).fetchall()}
        return date, snap, rs_coverage
    finally:
        con.close()


def market_breadth(snap) -> float:
    """市场广度：池内收盘价站上 MA20 的比例（%）。

    动量崩溃保护（Daniel & Moskowitz 2016）：广度坍塌期追高动量股的
    回撤风险集中爆发，此时压缩推荐数量比任何个股过滤都有效。
    """
    rows = [r for r in snap.values() if r["close"] and r["ma20"]]
    if not rows:
        return 100.0
    above = sum(1 for r in rows if r["close"] > r["ma20"])
    return above / len(rows) * 100


def _fund_ok(r) -> bool:
    """基本面硬性门槛：候选必须有有效市值/换手率，市值≥20亿，换手率0.3%–20%。"""
    mc = r["market_cap"] or 0
    tr = r["turnover_rate"] or 0
    if mc <= 0 or tr <= 0:
        return False
    if mc < 20:
        return False
    if not (0.3 <= tr <= 20):
        return False
    return True


def _perf_ok(r) -> bool:
    """PERF 历史胜率过滤（Wilson 95% 置信界口径）：

    - 追涨"历史差"：趋势跟随多头胜率 Wilson 上界 < 50%（显著差于抛硬币）→ 排除
    - 追涨平均不赚钱：n≥10 且 avg10 ≤ 0 → 排除（均值无置信区间，保留 N 阈值）
    - 超买信号"历史有效"：当前触发复合超买且超买反转胜率 Wilson 下界 > 50% → 排除（等回调）
    """
    tf_win = r["perf_trend_follow_bull_win10"]
    ob_win = r["perf_overbought_bear_win10"]
    tf_n = r["perf_trend_follow_bull_n"]
    ob_n = r["perf_overbought_bear_n"]
    tf_avg = r["perf_trend_follow_bull_avg10"]

    if tf_win is not None and tf_n:
        _, hi = _wilson_bounds(tf_win, tf_n)
        if hi < 50:
            return False
    if tf_avg is not None and tf_n is not None and tf_n >= 10 and tf_avg <= 0:
        return False
    if r["sig_overbought"] == 1 and ob_win is not None and ob_n:
        lo, _ = _wilson_bounds(ob_win, ob_n)
        if lo > 50:
            return False
    return True


def _cdwn_top_n(r) -> int:
    """countdown 顶部序列计数：td_countdown 为 "见顶/N"（tdSignalText 落库格式）时返回 N，否则 0。"""
    m = re.search(r"顶/(\d+)", r["td_countdown"] or "")
    return int(m.group(1)) if m else 0


def _td_safe(r) -> bool:
    """TD 安全检查：setup见顶/8-9 或 countdown 见顶/7-13 为高危区，排除。"""
    setup = r["td_setup"] or ""
    if setup and "见顶" in setup:
        m = re.search(r"见顶/(\d+)", setup)
        if m and int(m.group(1)) >= 8:
            return False

    n = _cdwn_top_n(r)
    if n:
        return n <= 6
    return True


def _td_top_count(r) -> int:
    m = re.search(r"顶/(\d+)", r["td_setup"] or "")
    if m:
        return int(m.group(1))
    return _cdwn_top_n(r)


def _div_bear_state(r) -> str:
    """顶背离三态：'ok'（可推荐）/ 'watch'（降级观察）/ 'exclude'（回避）。

    - 顶背离在本股历史显著有效（胜率 Wilson 下界 > 50%）→ exclude
    - 无样本 = 不确定 → watch（旧版直接排除，与 _perf_ok "无数据放行"
      不对称；边沿去灌水后 div_n 普降，按旧规则会开始误杀）
    - 不显著 + 强趋势（ADX≥38 + SAR/ST双多）→ ok；非强趋势 → watch
    """
    if r["div_bear"] != 1:
        return "ok"

    div_win = r["perf_div_bear_win10"]
    div_n = r["perf_div_bear_n"]
    if div_win is None or not div_n:
        return "watch"

    lo, _ = _wilson_bounds(div_win, div_n)
    if lo > 50:
        return "exclude"

    strong_trend = (
        r["adx"] >= 38
        and r["sar_long"] == 1
        and r["supertrend_long"] == 1
    )
    return "ok" if strong_trend else "watch"


def _late_stage_risk(r) -> bool:
    """强势票的末端风险：不直接排除，但从推荐降到观察。

    乖离用波动率归一化（bias24/atr_pct > 4 = 偏离 MA24 超 4 个日 ATR）：
    热榜池平均 ATR% 6.4，固定 bias 阈值对高低波票含义完全不同；
    SAR/ST/MACD/RS 硬门槛存活者 2/3 的 bias24>15，固定 15 会清空推荐池。
    换手 15–20% 降级与 _fund_ok 的 >20 排除形成连续梯度（A股高换手是
    强负向因子）；连涨≥5 日对应 A股短期反转效应（捕捉路径而非位置）。
    """
    chg = r["change_pct"] or 0
    vr = r["vol_ratio"] or 0
    td_top = _td_top_count(r)
    div_bear = r["div_bear"] == 1
    bias = r["bias24"] or 0
    atr = r["atr_pct"] or 0
    stretched = bias / atr > 4 if atr > 0 else bias > 25
    streak = r["streak"] or 0
    tr = r["turnover_rate"] or 0
    return (
        (chg >= 5 and vr >= 1.5)
        or (chg >= 3 and div_bear)
        or (td_top >= 5 and div_bear)
        or stretched
        or streak >= 5
        or tr > 15
    )


def tier(r) -> str | None:
    """返回 '⭐⭐⭐' / '⭐⭐' / '👁️观察' / None（不够格）。"""
    if not _fund_ok(r):
        return None
    if not _perf_ok(r):
        return None

    rs20 = r["rs20"]
    if rs20 is None or rs20 < 60:
        return None
    # 中期动量保险：20日强但60日弱 = 一波急拉的纯反转候选（1个月内是
    # 学术反转区，Jegadeesh 1990；A股反转效应更强），要求中期也不弱。
    rs60 = r["rs60"]
    if rs60 is not None and rs60 < 45:
        return None

    hist = r["macd_hist"] or 0
    chg = r["change_pct"] or 0
    vr = r["vol_ratio"] or 0

    # ±9.5 闸门隐含主板 10% 涨跌停假设；若池子混入创业板/科创板（±20%）
    # 该语义会静默漂移，届时需按板块区分阈值。
    if chg <= -9.5:
        return None
    if chg <= -5.0 and vr > 1.5:
        return None
    if chg >= 9.5:
        return None

    div_state = _div_bear_state(r)
    if div_state == "exclude":
        return None
    core_tech = (
        r["sar_long"] == 1
        and r["supertrend_long"] == 1
        and r["obv_up"] == 1
        and hist > 0
        and _td_safe(r)
    )
    if not core_tech:
        return None

    if chg >= 0:
        if r["score_total"] >= 70 and r["adx"] >= 38:
            if _late_stage_risk(r) or div_state == "watch":
                return "👁️观察"
            return "⭐⭐⭐"
        if r["score_total"] >= 65 and r["adx"] >= 35:
            return "👁️观察" if div_state == "watch" else "⭐⭐"
    else:
        is_strong = rs20 >= 80 and r["score_total"] >= 65
        min_chg = -5.0 if is_strong else -2.0
        max_vr = 1.5 if is_strong else 1.0
        if min_chg <= chg and vr < max_vr and r["score_total"] >= 65 and r["adx"] >= 35:
            return "👁️观察"

    return None


def _stop_text(r) -> str:
    """止损列：SAR 值（日志口径的止损价）+ 相对现价距离%。"""
    sar = r["sar_value"]
    close = r["close"]
    if not sar or not close:
        return "—"
    dist = (sar / close - 1) * 100
    return f"{sar:.2f}({dist:+.1f}%)"


def _position_hint(r, capital: float) -> str:
    """ATR 仓位法（简化）：单笔风险 1% 总资金 / 止损距离 → 建议股数上限。

    高 ATR/宽止损的票建议仓位自然缩小——传达"仓位可行性"而非给股票
    本身贴优劣标签。
    """
    sar = r["sar_value"]
    close = r["close"]
    if not capital or not sar or not close or close <= sar:
        return ""
    risk_per_share = close - sar
    shares = int(capital * 0.01 / risk_per_share / 100) * 100
    if shares <= 0:
        return "止损距离过宽，建议观望"
    return f"建议≤{shares}股"


def signals(r, cost=0.0, shares=0, capital=0.0) -> str:
    parts = []
    cdwn = r["td_countdown"] or ""
    td = cdwn if (cdwn and cdwn != "-/0") else r["td_setup"] or ""
    if "底" in td:
        parts.append(f"底部序列({td})")
    elif td:
        parts.append(td)
    # 趋势 stance：候选经 core_tech 必为双多；持仓行可能翻空——退出纪律
    # 比入场筛选更决定波段收益，翻空必须显式警示而非只显示浮盈。
    sar, st = r["sar_long"] == 1, r["supertrend_long"] == 1
    if sar and st:
        parts.append("SAR/ST双多")
    elif sar:
        parts.append("SAR多/⚠️ST翻空")
    elif st:
        parts.append("ST多/⚠️SAR翻空")
    else:
        parts.append("⚠️SAR/ST双空")
    if r["obv_up"] == 1:
        parts.append("OBV净流入")
    hist = r["macd_hist"] or 0
    if hist > 0:
        parts.append(f"MACD H={hist:.2f}")
    # Donchian 突破：55 覆盖 20，不叠加；Squeeze 方向中性，仅作信息标记
    if r["donch_break55_bull"] == 1:
        parts.append("破D55")
    elif r["donch_break20_bull"] == 1:
        parts.append("破D20")
    if r["keltner_squeeze"] == 1:
        parts.append("Squeeze压缩")
    if r["div_bear"] == 1:
        parts.append("⚠️顶背离")
    if cdwn_top := _cdwn_top_n(r):
        parts.append(f"⚠️C顶{cdwn_top}")
    if _late_stage_risk(r):
        parts.append("⚠️末端追高")
    tf_avg = r["perf_trend_follow_bull_avg10"]
    tf_n = r["perf_trend_follow_bull_n"]
    if tf_avg is not None and tf_n is not None and tf_n >= 10:
        parts.append(f"趋势A10={tf_avg:+.1f}%")
    if cost > 0 and shares > 0:
        profit = (r["close"] - cost) * shares
        profit_pct = (r["close"] / cost - 1) * 100
        parts.append(f"浮盈{profit:+.0f}（{profit_pct:+.1f}%）")
    elif capital > 0:
        # 仅候选行给建议仓位；持仓行已有真实股数
        if hint := _position_hint(r, capital):
            parts.append(hint)
    return "，".join(parts) if parts else "—"


def row_text(label, r, cost=0.0, shares=0, capital=0.0) -> str:
    sa = f"{r['score_total']} / {r['adx']:.1f}"
    chg = f"{r['change_pct']:+.2f}%"
    vr = f"{r['vol_ratio']:.2f}" if r["vol_ratio"] else "—"
    rs20 = f"{r['rs20']:.0f}" if r["rs20"] is not None else "—"
    mc = f"{r['market_cap']:.0f}亿" if r["market_cap"] else "—"
    tr = f"{r['turnover_rate']:.2f}%" if r["turnover_rate"] else "—"
    hot = f"{r['hot_score']}" if r["hot_score"] else "—"

    div_n = r["perf_div_bear_n"]
    div_win = r["perf_div_bear_win10"]
    if div_n and div_win is not None:
        perf = f"N={div_n},W={div_win:.0f}%"
    elif div_n:
        perf = f"N={div_n}"
    else:
        perf = "—"

    return (
        f"| {label} | {r['code']} | {r['name']} | {sa} | {chg} | {vr} | {rs20} | "
        f"{hot} | {perf} | {mc} | {tr} | {_stop_text(r)} | {signals(r, cost, shares, capital)} |"
    )


def sort_key(r):
    # 综合动量首键：短期动量(20日)处于学术反转区，权重压到 0.3，
    # 主排序交给与持有周期（数天-数周）匹配的中期动量 rs60。
    rs20 = r["rs20"] if r["rs20"] is not None else 0
    rs60 = r["rs60"] if r["rs60"] is not None else 0
    rs120 = r["rs120"] if r["rs120"] is not None else 0
    momentum = 0.3 * rs20 + 0.5 * rs60 + 0.2 * rs120
    chg = r["change_pct"] or 0
    td_penalty = 0
    setup = r["td_setup"] or ""
    if "见顶/8" in setup or "见顶/9" in setup:
        td_penalty = 100
    elif _cdwn_top_n(r) >= 7:
        td_penalty = 50
    div_penalty = 10 if r["div_bear"] == 1 else 0
    late_penalty = 20 if _late_stage_risk(r) else 0
    # Donchian 多头突破排序加分（55 覆盖 20）；Squeeze 方向中性不参与排序
    breakout = 2 if r["donch_break55_bull"] == 1 else (1 if r["donch_break20_bull"] == 1 else 0)
    return (-momentum, td_penalty, late_penalty, div_penalty, -breakout, -r["score_total"], -chg, -r["adx"])


def build_candidates(snap: dict[str, sqlite3.Row], holding_codes: set[str]):
    candidates = {"⭐⭐⭐": [], "⭐⭐": [], "👁️观察": []}
    for r in snap.values():
        if r["code"] in holding_codes:
            continue
        t = tier(r)
        if t:
            candidates[t].append(r)
    for t in candidates:
        candidates[t].sort(key=sort_key)
    return candidates


def select_candidates(candidates, limit: int):
    selected = []
    if limit <= 0:
        return selected
    for t in ["⭐⭐⭐", "⭐⭐", "👁️观察"]:
        for r in candidates[t]:
            if len(selected) >= limit:
                break
            selected.append((t, r))
        if len(selected) >= limit:
            break
    return selected


def render(date, snap, holdings, candidates, selected, limit, breadth=100.0, gated=False, capital=0.0) -> str:
    lines = [
        f"**候补 & 推荐（{date}，持仓 {len(holdings)} 只 + 候选 {len(selected)} 只）**",
        "",
    ]
    if gated:
        lines.append(
            f"> 🚨 市场广度闸门：池内仅 {breadth:.0f}% 站上 MA20（<40%），动量崩溃风险期，"
            f"推荐上限减半至 {limit} 只——今日少推荐是市场状态，不是程序故障"
        )
        lines.append("")
    lines.extend([
        "| 级别 | 代码 | 名称 | score / ADX | 今日% | 量比 | RS20 | 热度 | 顶背离PERF | 市值 | 换手 | 止损(距%) | 信号摘要 |",
        "|------|------|------|-------------|-------|------|------|------|-----------|------|------|----------|---------|",
    ])
    for code, cost, shares in holdings:
        r = snap.get(code)
        if r:
            lines.append(row_text("📌持仓", r, cost, shares))
        else:
            lines.append(f"| 📌持仓 | {code} | — | — | — | — | — | — | — | — | — | — | 无快照数据 |")
    for t, r in selected:
        lines.append(row_text(t, r, capital=capital))
    if len(selected) < limit:
        lines.extend([
            "",
            f"> ⚠️ 优质候选不足 {limit} 只，当前仅筛出 {len(selected)} 只（⭐⭐⭐ {len(candidates['⭐⭐⭐'])} / ⭐⭐ {len(candidates['⭐⭐'])} / 👁️观察 {len(candidates['👁️观察'])}）",
            ">    推荐池（⭐⭐⭐/⭐⭐）：红盘/平盘 + 技术完美；强势但末端风险降为观察",
            ">    观察池（👁️）：强势股(RS≥80):-5%~0%且量比<1.5；一般股:-2%~0%且量比<1.0",
        ])
    return "\n".join(lines)


def _ensure_decision_log(cur):
    cur.execute("""CREATE TABLE IF NOT EXISTS decision_log (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  code         TEXT NOT NULL REFERENCES instrument(code) ON DELETE CASCADE,
  log_date     TEXT NOT NULL,
  action       TEXT NOT NULL,
  tier         TEXT NOT NULL,
  score_total  INTEGER,
  adx          REAL,
  sar_long     INTEGER,
  st_long      INTEGER,
  obv_up       INTEGER,
  macd_hist    REAL,
  td_countdown TEXT,
  signals      TEXT,
  created_at   TEXT NOT NULL,
  outcome_pct  REAL,
  outcome_date TEXT,
  correct      INTEGER,
  UNIQUE(code, log_date, action)
)""")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_decision_log_date ON decision_log(log_date)")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_decision_log_pending ON decision_log(outcome_pct) WHERE outcome_pct IS NULL")


def _upsert_decision(cur, values) -> int:
    cur.execute(
        "INSERT INTO decision_log "
        "(code, log_date, action, tier, score_total, adx, sar_long, st_long, "
        "obv_up, macd_hist, td_countdown, signals, created_at) "
        "VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) "
        "ON CONFLICT(code, log_date, action) DO UPDATE SET "
        "tier=excluded.tier, score_total=excluded.score_total, adx=excluded.adx, "
        "sar_long=excluded.sar_long, st_long=excluded.st_long, obv_up=excluded.obv_up, "
        "macd_hist=excluded.macd_hist, td_countdown=excluded.td_countdown, "
        "signals=excluded.signals, created_at=excluded.created_at",
        values,
    )
    return cur.rowcount


def save_decisions(con, snap, date, holdings, selected):
    cur = con.cursor()
    _ensure_decision_log(cur)
    upserted_holdings = 0
    upserted_selected = 0
    skipped_holdings = 0
    now = _dt.datetime.now().isoformat()
    for code, cost, shares in holdings:
        r = snap.get(code)
        if not r:
            skipped_holdings += 1
            continue
        upserted_holdings += _upsert_decision(cur, (
            code, date, "hold", "📌持仓",
            r["score_total"], r["adx"],
            r["sar_long"], r["supertrend_long"], r["obv_up"],
            r["macd_hist"] or 0, r["td_countdown"] or "",
            signals(r, cost, shares), now,
        ))
    for tier_label, r in selected:
        action = "watch" if tier_label == "👁️观察" else "recommend"
        upserted_selected += _upsert_decision(cur, (
            r["code"], date, action, tier_label,
            r["score_total"], r["adx"],
            r["sar_long"], r["supertrend_long"], r["obv_up"],
            r["macd_hist"] or 0, r["td_countdown"] or "",
            signals(r), now,
        ))
    con.commit()
    return upserted_holdings, upserted_selected, skipped_holdings


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("db", nargs="?", default="data/stock.db")
    parser.add_argument("--holdings", default="",
                        help="持仓，格式：代码:成本:股数,... 如 sh601991:8.504:1300")
    parser.add_argument("--max", type=int, default=10, help="持仓+候选总上限（默认10）")
    parser.add_argument("--capital", type=float, default=0,
                        help="总资金（元）；提供时按单笔风险1%%/止损距离输出候选建议仓位")
    parser.add_argument("--dry-run", action="store_true", help="仅输出不写入decision_log")
    args = parser.parse_args(argv)

    holdings = parse_holdings(args.holdings)
    holding_codes = {code for code, _, _ in holdings}
    date, snap, rs_coverage = load_snapshots(args.db)
    if rs_coverage < MIN_RS_COVERAGE:
        print(
            f"❌ 错误：最新日 {date} RS20 覆盖率仅 {rs_coverage:.0f}%，"
            "`go run ./cmd/stockdb rs-rank` 后需达到 90% 以上",
            file=sys.stderr,
        )
        return 1

    candidates = build_candidates(snap, holding_codes)
    limit = args.max - len(holdings)
    # 市场广度闸门：广度坍塌期（<40% 站上 MA20）动量崩溃风险集中，推荐上限减半
    breadth = market_breadth(snap)
    gated = breadth < 40
    if gated and limit > 0:
        limit = max(1, limit // 2)
    selected = select_candidates(candidates, limit)
    print(render(date, snap, holdings, candidates, selected, limit, breadth, gated, args.capital))

    if args.dry_run:
        print("\n> 🔍 dry-run 模式，未写入 decision_log")
        return 0

    con = sqlite3.connect(args.db)
    try:
        upserted_holdings, upserted_selected, skipped_holdings = save_decisions(
            con, snap, date, holdings, selected,
        )
        print(
            f"\n> 📝 已写入 decision_log（更新/新增 {upserted_holdings} 持仓 + "
            f"{upserted_selected} 候选；无快照持仓 {skipped_holdings}）"
        )
    except Exception as e:
        print(f"\n> ⚠️ decision_log 写入失败: {e}")
    finally:
        con.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
