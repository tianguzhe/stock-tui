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

硬性过滤（PERF 历史验证，样本数≥10时生效）：
  - 若 PERF 趋势跟随多头 win10 < 40%：排除（追涨历史差）
  - 若当前 sig_overbought=1 且 PERF 超买反转空头 win10 > 55%：排除（超买信号历史有效，等回调）
  - 顶背离：无样本或样本不足直接排除；有充分样本时，强趋势(ADX≥38+双多)+顶背离历史无效(win10<50%) → 容忍
"""
from __future__ import annotations

import argparse
import datetime as _dt
import re
import sqlite3
import sys
from collections.abc import Sequence


MIN_RS_COVERAGE = 90


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
               s.score_total, s.adx, s.change_pct, s.close,
               s.sar_long, s.supertrend_long, s.obv_up,
               s.macd_hist, s.vol_ratio,
               s.td_setup, s.td_countdown,
               s.div_bear, s.sig_overbought,
               COALESCE(s.turnover_rate, 0) AS turnover_rate,
               COALESCE(s.market_cap, 0)    AS market_cap,
               COALESCE(s.pe, 0)            AS pe,
               s.rs20,
               s.perf_trend_follow_bull_win10,
               s.perf_overbought_bear_win10,
               s.perf_div_bear_win10,
               s.perf_trend_follow_bull_n,
               s.perf_overbought_bear_n,
               s.perf_div_bear_n
        FROM snapshot s JOIN instrument i ON s.code = i.code
        WHERE s.trade_date = ?
        """, (date,)).fetchall()}
        return date, snap, rs_coverage
    finally:
        con.close()


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
    """PERF 历史胜率过滤：追涨历史差或超买信号历史有效则排除（样本数≥10时生效）。"""
    tf_win = r["perf_trend_follow_bull_win10"]
    ob_win = r["perf_overbought_bear_win10"]
    tf_n = r["perf_trend_follow_bull_n"]
    ob_n = r["perf_overbought_bear_n"]

    if tf_win is None and ob_win is None:
        return True
    if tf_win is not None and tf_n is not None and tf_n >= 10 and tf_win < 40:
        return False
    if r["sig_overbought"] == 1 and ob_win is not None and ob_n is not None and ob_n >= 10 and ob_win > 55:
        return False
    return True


def _td_safe(r) -> bool:
    """TD 安全检查：setup见顶/8-9 或 countdown C顶7-13 为高危区，排除。"""
    setup = r["td_setup"] or ""
    if setup and "见顶" in setup:
        m = re.search(r"见顶/(\d+)", setup)
        if m and int(m.group(1)) >= 8:
            return False

    cdwn = r["td_countdown"] or ""
    if not cdwn:
        return True
    m = re.search(r"[顶底]/(\d+)", cdwn)
    if not m:
        m = re.search(r"C[顶底](\d+)", cdwn)
    if m:
        n = int(m.group(1))
        if "顶" in cdwn or "Sell" in cdwn or "sell" in cdwn:
            return n <= 6
    return True


def _td_top_count(r) -> int:
    setup = r["td_setup"] or ""
    cdwn = r["td_countdown"] or ""
    for text in (setup, cdwn):
        if "顶" not in text and "Sell" not in text and "sell" not in text:
            continue
        m = re.search(r"[顶]/(\d+)", text)
        if not m:
            m = re.search(r"C顶(\d+)", text)
        if m:
            return int(m.group(1))
    return 0


def _div_bear_safe(r) -> bool:
    """顶背离安全检查：强趋势中顶背离可容忍，但需充分样本验证。"""
    if r["div_bear"] != 1:
        return True

    div_win = r["perf_div_bear_win10"]
    div_n = r["perf_div_bear_n"]
    if div_win is None or div_n is None or div_n < 10:
        return False

    strong_trend = (
        r["adx"] >= 38
        and r["sar_long"] == 1
        and r["supertrend_long"] == 1
    )
    return strong_trend and div_win < 50


def _late_stage_risk(r) -> bool:
    """强势票的末端风险：不直接排除，但从推荐降到观察。"""
    chg = r["change_pct"] or 0
    vr = r["vol_ratio"] or 0
    td_top = _td_top_count(r)
    div_bear = r["div_bear"] == 1
    return (
        (chg >= 5 and vr >= 1.5)
        or (chg >= 3 and div_bear)
        or (td_top >= 5 and div_bear)
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

    hist = r["macd_hist"] or 0
    chg = r["change_pct"] or 0
    vr = r["vol_ratio"] or 0

    if chg <= -9.5:
        return None
    if chg <= -5.0 and vr > 1.5:
        return None
    if chg >= 9.5:
        return None

    core_tech = (
        r["sar_long"] == 1
        and r["supertrend_long"] == 1
        and r["obv_up"] == 1
        and hist > 0
        and _div_bear_safe(r)
        and _td_safe(r)
    )
    if not core_tech:
        return None

    if chg >= 0:
        if r["score_total"] >= 70 and r["adx"] >= 38:
            if _late_stage_risk(r):
                return "👁️观察"
            return "⭐⭐⭐"
        if r["score_total"] >= 65 and r["adx"] >= 35:
            return "⭐⭐"
    else:
        is_strong = rs20 >= 80 and r["score_total"] >= 65
        min_chg = -5.0 if is_strong else -2.0
        max_vr = 1.5 if is_strong else 1.0
        if min_chg <= chg and vr < max_vr and r["score_total"] >= 65 and r["adx"] >= 35:
            return "👁️观察"

    return None


def signals(r, cost=0.0, shares=0) -> str:
    parts = []
    cdwn = r["td_countdown"] or ""
    td = cdwn if (cdwn and cdwn != "-/0") else r["td_setup"] or ""
    if "底" in td:
        parts.append(f"底部序列({td})")
    elif td:
        parts.append(td)
    if r["supertrend_long"] == 1:
        parts.append("SAR/ST双多")
    elif r["sar_long"] == 1:
        parts.append("SAR多")
    if r["obv_up"] == 1:
        parts.append("OBV净流入")
    hist = r["macd_hist"] or 0
    if hist > 0:
        parts.append(f"MACD H={hist:.2f}")
    if r["div_bear"] == 1:
        parts.append("⚠️顶背离")
    if cdwn.startswith("C顶"):
        parts.append(f"⚠️{cdwn}")
    if _late_stage_risk(r):
        parts.append("⚠️末端追高")
    if cost > 0 and shares > 0:
        profit = (r["close"] - cost) * shares
        profit_pct = (r["close"] / cost - 1) * 100
        parts.append(f"浮盈{profit:+.0f}（{profit_pct:+.1f}%）")
    return "，".join(parts) if parts else "—"


def row_text(label, r, cost=0.0, shares=0) -> str:
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
        f"{hot} | {perf} | {mc} | {tr} | {signals(r, cost, shares)} |"
    )


def sort_key(r):
    rs20 = r["rs20"] if r["rs20"] is not None else 0
    chg = r["change_pct"] or 0
    td_penalty = 0
    setup = r["td_setup"] or ""
    cdwn = r["td_countdown"] or ""
    if "见顶/8" in setup or "见顶/9" in setup:
        td_penalty = 100
    elif "C顶" in cdwn:
        m = re.search(r"C顶(\d+)", cdwn)
        if m and int(m.group(1)) >= 7:
            td_penalty = 50
    div_penalty = 10 if r["div_bear"] == 1 else 0
    late_penalty = 20 if _late_stage_risk(r) else 0
    return (-rs20, td_penalty, late_penalty, div_penalty, -r["score_total"], -chg, -r["adx"])


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


def render(date, snap, holdings, candidates, selected, limit) -> str:
    lines = [
        f"**候补 & 推荐（{date}，持仓 {len(holdings)} 只 + 候选 {len(selected)} 只）**",
        "",
        "| 级别 | 代码 | 名称 | score / ADX | 今日% | 量比 | RS20 | 热度 | 顶背离PERF | 市值 | 换手 | 信号摘要 |",
        "|------|------|------|-------------|-------|------|------|------|-----------|------|------|---------|",
    ]
    for code, cost, shares in holdings:
        r = snap.get(code)
        if r:
            lines.append(row_text("📌持仓", r, cost, shares))
        else:
            lines.append(f"| 📌持仓 | {code} | — | — | — | — | — | — | — | — | — | 无快照数据 |")
    for t, r in selected:
        lines.append(row_text(t, r))
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
    selected = select_candidates(candidates, limit)
    print(render(date, snap, holdings, candidates, selected, limit))

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
