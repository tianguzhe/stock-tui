#!/usr/bin/env python3
"""多因子选股筛选器 — 持仓 + 优质候选，输出可直接贴入日志"四、候补&推荐"的 Markdown 表格。

用法：
  python3 scripts/screen-stocks.py [data/stock.db] \\
      --holdings sh601991:8.504:1300,sh603256:193.752:100,sh605589:53.176:200

  持仓格式：代码:成本价:股数（逗号分隔）

候选筛选（不凑数，只出真正优质的）：
  ⭐⭐⭐  score≥70, ADX≥38, SAR/ST双多, OBV净流入, MACD hist>0,
         TD非C顶或C顶≤6, 顶背离需充分PERF验证(N≥10且win10<50%), 热度≥3
  ⭐⭐   score≥65, ADX≥35, SAR/ST双多, OBV净流入, MACD hist>0,
         TD非C顶或C顶≤6, 顶背离需充分PERF验证
  优先展示 ⭐⭐⭐，其次 ⭐⭐，合计不超过 (10 - 持仓数)，但不凑数——宁少勿滥

硬性过滤（PERF 历史验证，样本数≥10时生效）：
  - 若 PERF 趋势跟随多头 win10 < 40%：排除（追涨历史差）
  - 若当前 sig_overbought=1 且 PERF 超买反转空头 win10 > 55%：排除（超买信号历史有效，等回调）
  - 顶背离：无样本或样本不足直接排除；有充分样本时，强趋势(ADX≥38+双多)+顶背离历史无效(win10<50%) → 容忍
"""
import sqlite3, sys, argparse

parser = argparse.ArgumentParser(add_help=False)
parser.add_argument("db", nargs="?", default="data/stock.db")
parser.add_argument("--holdings", default="",
                    help="持仓，格式：代码:成本:股数,... 如 sh601991:8.504:1300")
parser.add_argument("--max", type=int, default=10, help="持仓+候选总上限（默认10）")
parser.add_argument("--dry-run", action="store_true", help="仅输出不写入decision_log")
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

# 校验 RS20 覆盖率：最新日至少50%有RS，否则中断
rs_coverage = con.execute("""
    SELECT COUNT(*) * 100.0 / NULLIF((SELECT COUNT(*) FROM snapshot WHERE trade_date = ?), 0)
    FROM snapshot WHERE trade_date = ? AND rs20 IS NOT NULL
""", (date, date)).fetchone()[0] or 0

if rs_coverage < 50:
    print(f"❌ 错误：最新日 {date} RS20 覆盖率仅 {rs_coverage:.0f}%，必须先运行 `go run ./cmd/stockdb rs-rank`", file=sys.stderr)
    sys.exit(1)

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
       s.rs20,  -- NULL表示未计算，0-100表示百分位
       s.perf_trend_follow_bull_win10,
       s.perf_overbought_bear_win10,
       s.perf_div_bear_win10,
       s.perf_trend_follow_bull_n,
       s.perf_overbought_bear_n,
       s.perf_div_bear_n
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
    """PERF 历史胜率过滤：追涨历史差或超买信号历史有效则排除（样本数≥10时生效）"""
    tf_win = r["perf_trend_follow_bull_win10"]
    ob_win = r["perf_overbought_bear_win10"]
    tf_n = r["perf_trend_follow_bull_n"]
    ob_n = r["perf_overbought_bear_n"]

    # 无 PERF 数据（新入库或历史不足）则放行
    if tf_win is None and ob_win is None:
        return True

    # 追涨历史差（win10 < 40%），但样本数≥10时才硬性排除
    if tf_win is not None and tf_n is not None and tf_n >= 10 and tf_win < 40:
        return False

    # 当前超买 + 超买反转历史有效（win10 > 55%），样本数≥10时才硬性排除
    if r["sig_overbought"] == 1 and ob_win is not None and ob_n is not None and ob_n >= 10 and ob_win > 55:
        return False

    return True


def _td_safe(r) -> bool:
    """TD 安全检查：setup见顶/8-9 或 countdown C顶7-13 为高危区，排除"""
    import re

    # 检查 setup：见顶/8-9 为临界高危区
    setup = r["td_setup"] or ""
    if setup and "见顶" in setup:
        m = re.search(r"见顶/(\d+)", setup)
        if m and int(m.group(1)) >= 8:  # 见顶/8-9 排除
            return False

    # 检查 countdown：C顶7-13 为高危区
    cdwn = r["td_countdown"] or ""
    if not cdwn:
        return True
    # 提取数字：C顶9 → 9 或 见顶/13 → 13
    m = re.search(r"[顶底]/(\d+)", cdwn)  # 匹配 "见顶/13" 或 "C顶9"
    if not m:
        m = re.search(r"C[顶底](\d+)", cdwn)  # 兼容 "C顶9" 格式
    if m:
        n = int(m.group(1))
        if "顶" in cdwn or "Sell" in cdwn or "sell" in cdwn:
            return n <= 6  # 见顶countdown仅允许1-6
    return True  # 底部序列或无法解析则放行


def _div_bear_safe(r) -> bool:
    """顶背离安全检查：强趋势中顶背离可容忍（技术钝化是常态），但需充分样本验证且历史明显无效"""
    if r["div_bear"] != 1:
        return True  # 无顶背离

    # 有顶背离时：必须有充分PERF样本（N≥10）才能评估，否则排除
    div_win = r["perf_div_bear_win10"]
    div_n = r["perf_div_bear_n"]

    # 无样本或样本不足：排除（无历史验证，不冒风险）
    if div_win is None or div_n is None or div_n < 10:
        return False

    # 有充分样本时：强趋势 + 顶背离历史明显无效（win10<40%）→ 容忍
    strong_trend = (r["adx"] >= 38
                    and r["sar_long"] == 1
                    and r["supertrend_long"] == 1)

    perf_div_weak = (div_win < 40)  # 收紧：从50%改为40%，历史胜率需明显低才容忍
    if strong_trend and perf_div_weak:
        return True

    return False  # 弱趋势或顶背离历史有效，排除


def tier(r) -> str | None:
    """返回 '⭐⭐⭐' / '⭐⭐' / None（不够格）"""
    if not _fund_ok(r):
        return None
    if not _perf_ok(r):
        return None
    # RS20 过滤：必须有RS且≥60（弱势股排除）
    rs20 = r["rs20"]
    if rs20 is None or rs20 < 60:
        return None
    hist = r["macd_hist"] or 0
    hot  = r["hot_score"] or 0
    chg  = r["change_pct"] or 0
    vr   = r["vol_ratio"] or 0

    # 当日破位/大跌保护：跌停/近跌停、放量大阴排除
    if chg <= -9.5:  # 跌停或近跌停
        return None
    if chg <= -5.0 and vr > 1.5:  # 放量大跌（跌超5%且量比>1.5）
        return None

    # 追高保护：涨停/近涨停排除（不适合次日买入）
    if chg >= 9.5:  # 涨停或近涨停
        return None

    # ⭐⭐⭐：严格门槛（ADX 40→38，平衡保守与覆盖）
    if (r["score_total"] >= 70
            and r["adx"] >= 38
            and r["sar_long"] == 1
            and r["supertrend_long"] == 1
            and r["obv_up"] == 1
            and hist > 0
            and _div_bear_safe(r)
            and _td_safe(r)):
        return "⭐⭐⭐"

    # ⭐⭐：放宽但保留核心安全阀
    if (r["score_total"] >= 65
            and r["adx"] >= 35
            and r["sar_long"] == 1
            and r["supertrend_long"] == 1
            and r["obv_up"] == 1
            and hist > 0
            and _div_bear_safe(r)
            and _td_safe(r)):
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
    hot  = f"{r['hot_score']}" if r["hot_score"] else "—"

    # PERF顶背离：样本数 + 胜率
    div_n = r["perf_div_bear_n"]
    div_win = r["perf_div_bear_win10"]
    if div_n and div_win is not None:
        perf = f"N={div_n},W={div_win:.0f}%"
    elif div_n:
        perf = f"N={div_n}"
    else:
        perf = "—"

    sig  = signals(r, cost, shares)
    print(f"| {label} | {r['code']} | {r['name']} | {sa} | {chg} | {vr} | {rs20} | {hot} | {perf} | {mc} | {tr} | {sig} |")


# ── 筛选候选（排除持仓）────────────────────────────────────────────────────────
candidates = {"⭐⭐⭐": [], "⭐⭐": []}
for r in snap.values():
    if r["code"] in holding_codes:
        continue
    t = tier(r)
    if t:
        candidates[t].append(r)

# 按质量综合排序：RS20 > score > 当日表现 > ADX（优先相对强度高、涨势好的）
def sort_key(r):
    rs20 = r["rs20"] if r["rs20"] is not None else 0  # NULL视为0
    chg = r["change_pct"] or 0
    # TD高危区降权
    setup = r["td_setup"] or ""
    cdwn = r["td_countdown"] or ""
    td_penalty = 0
    if "见顶/8" in setup or "见顶/9" in setup:
        td_penalty = 100  # setup高危区大幅降权
    elif "C顶" in cdwn:
        import re
        m = re.search(r"C顶(\d+)", cdwn)
        if m and int(m.group(1)) >= 7:
            td_penalty = 50  # countdown高危区中等降权
    # 顶背离降权
    div_penalty = 10 if r["div_bear"] == 1 else 0

    return (-rs20, td_penalty, div_penalty, -r["score_total"], -chg, -r["adx"])

for t in candidates:
    candidates[t].sort(key=sort_key)

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
print("| 级别 | 代码 | 名称 | score / ADX | 今日% | 量比 | RS20 | 热度 | 顶背离PERF | 市值 | 换手 | 信号摘要 |")
print("|------|------|------|-------------|-------|------|------|------|-----------|------|------|---------|")

for code, cost, shares in holdings:
    r = snap.get(code)
    if r:
        print_row("📌持仓", r, cost, shares)
    else:
        print(f"| 📌持仓 | {code} | — | — | — | — | — | — | — | — | — | 无快照数据 |")

for t, r in selected:
    print_row(t, r)

if n_cand < limit:
    print(f"\n> ⚠️ 优质候选不足 {limit} 只，当前仅筛出 {n_cand} 只（⭐⭐⭐ {len(candidates['⭐⭐⭐'])} / ⭐⭐ {len(candidates['⭐⭐'])}）")
    print(f">    策略定位：极致严格的强趋势上涨日精选，宁缺毋滥")
    print(f">    当前市场：{'全部下跌日，无符合买入标准' if n_cand == 0 else '少量符合标准'}")

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
    if args.dry_run:
        print("\n> 🔍 dry-run 模式，未写入 decision_log")
    else:
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
