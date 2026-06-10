#!/usr/bin/env python3
"""多因子选股筛选器 — 持仓 + 优质候选，输出可直接贴入日志"四、候补&推荐"的 Markdown 表格。

用法：
  python3 scripts/screen-stocks.py [data/stock.db] \\
      --holdings sh601991:8.504:1300,sh603256:193.752:100,sh605589:53.176:200

  持仓格式：代码:成本价:股数（逗号分隔）

候选筛选（不凑数，只出真正优质的）：
  ⭐⭐⭐  score≥70, ADX≥40, SAR/ST双多, OBV净流入, MACD hist>0,
         TD非C顶或C顶≤6, 今日无顶背离, 热度≥3
  ⭐⭐   score≥65, ADX≥35, SAR/ST双多, OBV净流入, MACD hist>0,
         TD非C顶或C顶≤6, 今日无顶背离（与⭐⭐⭐共享安全阀）
  优先展示 ⭐⭐⭐，其次 ⭐⭐，合计不超过 (10 - 持仓数)，但不凑数——宁少勿滥

硬性过滤（PERF 暂不可用，future: 扩展 snapshot 或建 perf 表后启用）：
  - 若 PERF 趋势跟随多头 win10 < 40%：排除（追涨历史差）
  - 若当前 sig_overbought=1 且 PERF 超买反转空头 win10 > 55%：排除（超买信号历史有效，等回调）
"""
import sqlite3, sys, argparse

parser = argparse.ArgumentParser(add_help=False)
parser.add_argument("db", nargs="?", default="data/stock.db")
parser.add_argument("--holdings", default="",
                    help="持仓，格式：代码:成本:股数,... 如 sh601991:8.504:1300")
parser.add_argument("--max", type=int, default=10, help="持仓+候选总上限（默认10）")
args = parser.parse_args()

# 解析持仓
holdings = []        # [(code, cost, shares)]
holding_codes = set()
for item in args.holdings.split(","):
    item = item.strip()
    if not item:
        continue
    parts = item.split(":")
    code  = parts[0].strip()
    cost  = float(parts[1]) if len(parts) > 1 else 0.0
    shares = int(parts[2])  if len(parts) > 2 else 0
    holdings.append((code, cost, shares))
    holding_codes.add(code)

con = sqlite3.connect(args.db)
con.row_factory = sqlite3.Row

date = con.execute("SELECT MAX(trade_date) FROM snapshot").fetchone()[0]

# 全量快照
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
       COALESCE(s.rs20, 0)          AS rs20,
       s.perf_trend_follow_bull_win10,
       s.perf_overbought_bear_win10
FROM snapshot s JOIN instrument i ON s.code = i.code
WHERE s.trade_date = ?
""", (date,)).fetchall()}
con.close()


def _fund_ok(r) -> bool:
    """基本面硬性门槛：市值≥20亿（有数据时），换手率在 0.3%–15% 区间（有数据时）"""
    mc = r["market_cap"] or 0
    tr = r["turnover_rate"] or 0
    if mc > 0 and mc < 20:
        return False
    if tr > 0 and not (0.3 <= tr <= 15):
        return False
    return True


def _perf_ok(r) -> bool:
    """PERF 历史胜率过滤：追涨历史差或超买信号历史有效则排除"""
    tf_win = r["perf_trend_follow_bull_win10"]
    ob_win = r["perf_overbought_bear_win10"]

    # 无 PERF 数据（新入库或历史不足）则放行
    if tf_win is None and ob_win is None:
        return True

    # 追涨历史差（win10 < 40%），排除
    if tf_win is not None and tf_win < 40:
        return False

    # 当前超买 + 超买反转历史有效（win10 > 55%），等回调
    if r["sig_overbought"] == 1 and ob_win is not None and ob_win > 55:
        return False

    return True


def _td_safe(cdwn: str) -> bool:
    """TD Countdown 安全检查：C顶7-13 为高危区，排除"""
    if not cdwn:
        return True
    # 提取数字：C顶9 → 9 或 见顶/13 → 13
    import re
    m = re.search(r"[顶底]/(\d+)", cdwn)  # 匹配 "见顶/13" 或 "C顶9"
    if not m:
        m = re.search(r"C[顶底](\d+)", cdwn)  # 兼容 "C顶9" 格式
    if m:
        n = int(m.group(1))
        if "顶" in cdwn or "Sell" in cdwn or "sell" in cdwn:
            return n <= 6  # 见顶countdown仅允许1-6
    return True  # 底部序列或无法解析则放行


def _div_bear_safe(r) -> bool:
    """顶背离安全检查：强趋势中顶背离可容忍（技术钝化是常态）"""
    if r["div_bear"] != 1:
        return True  # 无顶背离

    # 有顶背离，但满足以下条件可容忍（趋势强劲 + 历史验证顶背离无效）：
    strong_trend = (r["adx"] >= 38                # 同步主筛选阈值
                    and r["sar_long"] == 1
                    and r["supertrend_long"] == 1)

    # PERF 历史：该股顶背离胜率低（<50%）说明顶背离信号不靠谱
    perf_div_weak = (r["perf_overbought_bear_win10"] is not None
                     and r["perf_overbought_bear_win10"] < 50)

    # 强趋势 + 顶背离历史无效 → 容忍（技术钝化但趋势未破）
    if strong_trend and perf_div_weak:
        return True

    return False  # 弱趋势或顶背离历史有效，排除


def tier(r) -> str | None:
    """返回 '⭐⭐⭐' / '⭐⭐' / None（不够格）"""
    if not _fund_ok(r):
        return None
    if not _perf_ok(r):
        return None
    # RS20 < 60 排除（弱势股）
    rs20 = r["rs20"] or 0
    if rs20 > 0 and rs20 < 60:
        return None
    cdwn = r["td_countdown"] or ""
    hist = r["macd_hist"] or 0
    hot  = r["hot_score"] or 0

    # ⭐⭐⭐：严格门槛（ADX 40→38，平衡保守与覆盖）
    if (r["score_total"] >= 70
            and r["adx"] >= 38                # 优化：放宽至38，ADX=38-40仍属强趋势
            and r["sar_long"] == 1
            and r["supertrend_long"] == 1
            and r["obv_up"] == 1
            and hist > 0
            and _div_bear_safe(r)        # 改为：多维度评估顶背离
            and _td_safe(cdwn)
            and hot >= 3):
        return "⭐⭐⭐"

    # ⭐⭐：放宽但保留核心安全阀
    if (r["score_total"] >= 65
            and r["adx"] >= 35
            and r["sar_long"] == 1
            and r["supertrend_long"] == 1
            and r["obv_up"] == 1
            and hist > 0
            and _div_bear_safe(r)        # 改为：多维度评估顶背离
            and _td_safe(cdwn)):
        return "⭐⭐"

    return None


def signals(r, cost=0.0, shares=0) -> str:
    parts = []
    cdwn  = r["td_countdown"] or ""
    td    = cdwn if (cdwn and cdwn != "-/0") else r["td_setup"] or ""
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
    # 持仓浮盈
    if cost > 0 and shares > 0:
        profit     = (r["close"] - cost) * shares
        profit_pct = (r["close"] / cost - 1) * 100
        parts.append(f"浮盈{profit:+.0f}（{profit_pct:+.1f}%）")
    return "，".join(parts) if parts else "—"


def print_row(label, r, cost=0.0, shares=0):
    sa   = f"{r['score_total']} / {r['adx']:.1f}"
    chg  = f"{r['change_pct']:+.2f}%"
    vr   = f"{r['vol_ratio']:.2f}" if r["vol_ratio"] else "—"
    rs20 = f"{r['rs20']:.0f}" if r["rs20"] else "—"
    mc   = f"{r['market_cap']:.0f}亿" if r["market_cap"] else "—"
    tr   = f"{r['turnover_rate']:.2f}%" if r["turnover_rate"] else "—"
    sig  = signals(r, cost, shares)
    print(f"| {label} | {r['code']} | {r['name']} | {sa} | {chg} | {vr} | {rs20} | {mc} | {tr} | {sig} |")


# ── 筛选候选（排除持仓）────────────────────────────────────────────────────────
candidates = {"⭐⭐⭐": [], "⭐⭐": []}
for r in snap.values():
    if r["code"] in holding_codes:
        continue
    t = tier(r)
    if t:
        candidates[t].append(r)

# 按 score 降序排列
for t in candidates:
    candidates[t].sort(key=lambda r: (-r["score_total"], -r["adx"]))

# 取候选，优先 ⭐⭐⭐，不足再取 ⭐⭐，总数不超过 max-持仓
limit = args.max - len(holdings)
selected = []
for t in ["⭐⭐⭐", "⭐⭐"]:
    for r in candidates[t]:
        if len(selected) >= limit:
            break
        selected.append((t, r))
    if len(selected) >= limit:
        break

# ── 输出 ──────────────────────────────────────────────────────────────────────
n_hold = len(holdings)
n_cand = len(selected)
print(f"**候补 & 推荐（{date}，持仓 {n_hold} 只 + 候选 {n_cand} 只）**\n")
print("| 级别 | 代码 | 名称 | score / ADX | 今日% | 量比 | RS20 | 市值 | 换手 | 信号摘要 |")
print("|------|------|------|-------------|-------|------|------|------|------|---------|")

for code, cost, shares in holdings:
    r = snap.get(code)
    if r:
        print_row("📌持仓", r, cost, shares)
    else:
        print(f"| 📌持仓 | {code} | — | — | — | — | 无快照数据 |")

for t, r in selected:
    print_row(t, r)

if n_cand < limit:
    print(f"\n> ⚠️ 优质候选不足 {limit} 只，当前仅筛出 {n_cand} 只（⭐⭐⭐ {len(candidates['⭐⭐⭐'])} / ⭐⭐ {len(candidates['⭐⭐'])}）")

# ── 写入 decision_log ────────────────────────────────────────────────────────
import datetime as _dt

def _ensure_decision_log(cur):
    """Create decision_log table if it doesn't exist yet."""
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

def _save_decisions(con, date, holdings, selected):
    """Insert recommend/hold entries into decision_log (idempotent on code+date+action)."""
    cur = con.cursor()
    _ensure_decision_log(cur)
    inserted_holdings = 0
    inserted_selected = 0
    skipped_holdings = 0
    for code, cost, shares in holdings:
        r = snap.get(code)
        if not r:
            skipped_holdings += 1
            continue
        cur.execute(
            "INSERT OR IGNORE INTO decision_log "
            "(code, log_date, action, tier, score_total, adx, sar_long, st_long, "
            "obv_up, macd_hist, td_countdown, signals, created_at) "
            "VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
            (code, date, "hold", "📌持仓",
             r["score_total"], r["adx"],
             r["sar_long"], r["supertrend_long"], r["obv_up"],
             r["macd_hist"] or 0, r["td_countdown"] or "",
             signals(r, cost, shares), _dt.datetime.now().isoformat()))
        inserted_holdings += cur.rowcount
    for tier_label, r in selected:
        cur.execute(
            "INSERT OR IGNORE INTO decision_log "
            "(code, log_date, action, tier, score_total, adx, sar_long, st_long, "
            "obv_up, macd_hist, td_countdown, signals, created_at) "
            "VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
            (r["code"], date, "recommend", tier_label,
             r["score_total"], r["adx"],
             r["sar_long"], r["supertrend_long"], r["obv_up"],
             r["macd_hist"] or 0, r["td_countdown"] or "",
             signals(r), _dt.datetime.now().isoformat()))
        inserted_selected += cur.rowcount
    con.commit()
    return inserted_holdings, inserted_selected, skipped_holdings

con2 = sqlite3.connect(args.db)
try:
    inserted_holdings, inserted_selected, skipped_holdings = _save_decisions(con2, date, holdings, selected)
    duplicate_holdings = len(holdings) - skipped_holdings - inserted_holdings
    duplicate_selected = len(selected) - inserted_selected
    print(
        f"\n> 📝 已写入 decision_log（新增 {inserted_holdings} 持仓 + {inserted_selected} 候选；"
        f"重复 {duplicate_holdings} 持仓 + {duplicate_selected} 候选；无快照持仓 {skipped_holdings}）"
    )
except Exception as e:
    print(f"\n> ⚠️ decision_log 写入失败: {e}")
finally:
    con2.close()
