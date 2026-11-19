# /// script
# requires-python = ">=3.11"
# dependencies = ["requests"]
# ///
"""Probe fuyao API against known data problems. Key read from /tmp/.fuyao_key (never stored in repo)."""
import json, sys, time, datetime as dt, requests
KEY = open("/tmp/.fuyao_key").read().strip()
B = "https://fuyao.aicubes.cn"
S = requests.Session(); S.headers["X-api-key"] = KEY
TZ = dt.timezone(dt.timedelta(hours=8))
def ms(d): return int(dt.datetime.strptime(d, "%Y-%m-%d").replace(tzinfo=TZ).timestamp()*1000)
def day(m): return dt.datetime.fromtimestamp(m/1000, TZ).strftime("%Y-%m-%d")
def get(path, **p):
    for i in range(3):
        r = S.get(B+path, params=p, timeout=30).json()
        if r["code"] == 4001: time.sleep(2**i); continue
        return r
    return r
def tx(code, n=800, fq="qfq"):
    u = f"https://proxy.finance.qq.com/ifzqgtimg/appstock/app/newfqkline/get?_var=k&param={code},day,,,{n},{fq}"
    t = requests.get(u, timeout=20).text; j = json.loads(t[t.index("=")+1:])["data"][code]
    rows = j.get("qfqday") or j.get("day")
    # row: date, open, close, high, low, vol(手), {}, turnover%, amount(万)
    return {r[0]: dict(o=float(r[1]), c=float(r[2]), h=float(r[3]), l=float(r[4]), v=float(r[5])*100, a=float(r[8])*1e4 if len(r)>8 else None) for r in rows}
def api_k(thscode, start, end, adj="forward", fund=False):
    path = "/api/fund/market/historical" if fund else "/api/a-share/prices/historical"
    r = get(path, thscode=thscode, interval="1d", start=ms(start), end=ms(end)+86400000, adjust=adj)
    if r["code"] != 0: return r
    return {day(x["date_ms"]): dict(o=x["open_price"], c=x["close_price"], h=x["high_price"], l=x["low_price"], v=x["volume"], a=x["turnover"]) for x in r["data"]["item"]}

mode = sys.argv[1]
if mode == "volfq":   # T1: does forward adjust volume/amount? 600519 送转 0.1 ex 2015-07-17
    for adj in ("none", "forward"):
        k = api_k("600519.SH", "2015-07-13", "2015-07-21", adj)
        print(adj, {d: (round(v["c"],3), v["v"], round(v["a"])) for d, v in k.items()} if isinstance(k, dict) else k)
if mode == "cmp":     # T2: API forward vs Tencent qfq over 800 bars
    for code, ths, fund in [("sh601138","601138.SH",0),("sz000063","000063.SZ",0),("sz002185","002185.SZ",0),("sh603496","603496.SH",0),("sh601991","601991.SH",0),("sh512480","512480.SH",1)]:
        t = tx(code); dates = sorted(t); a = api_k(ths, dates[0], dates[-1], fund=bool(fund))
        if not isinstance(a, dict): print(code, a.get("code"), a.get("message")); continue
        com = [d for d in dates if d in a]; miss_api = [d for d in dates if d not in a]; miss_tx = [d for d in a if d not in t]
        mx = lambda f: max(abs(a[d][f]/t[d][f]-1) for d in com if t[d][f])
        vr = [a[d]["v"]/t[d]["v"] for d in com if t[d]["v"]]
        ar = [a[d]["a"]/t[d]["a"] for d in com if t[d]["a"]]
        print(f"{code} 共同{len(com)} 仅腾讯{len(miss_api)} 仅API{len(miss_tx)} | maxΔ O={mx('o'):.4%} H={mx('h'):.4%} L={mx('l'):.4%} C={mx('c'):.4%} | vol API/腾讯 min={min(vr):.4f} max={max(vr):.4f} | amt min={min(ar):.4f} max={max(ar):.4f}")
        if miss_api[:3]: print("   仅腾讯日期样例", miss_api[:5])
if mode == "adj":     # T4: dividend events vs known facts
    for ths in ["601991.SH","601138.SH","000063.SZ","002185.SZ","603496.SH","601166.SH","600036.SH","600863.SH"]:
        r = get("/api/a-share/corporate-actions/adjustment-factors", thscode=ths, **{"from":"2026-01-01"})
        print(ths, r["code"], [(day(x["ex_date_ms"]), x["dividend_per_share"], x["per_share_bonus"]) for x in (r["data"] or {}).get("item", [])])
if mode == "cal":
    r = get("/api/a-share/calendar/trading-days"); it = [x["date"] for x in r["data"]["item"]]
    print("范围", it[0], it[-1], "条数", len(it), "含20260619?", "20260619" in it, "含20260618/22?", "20260618" in it, "20260622" in it)
    for k,v in [("start","2026-06-01"),("start_date","2026-06-01"),("from","2026-06-01")]:
        r2 = get("/api/a-share/calendar/trading-days", **{k:v}); print(k, r2["code"], r2["data"]["item"][0]["date"] if r2["code"]==0 else r2["message"])
if mode == "pools":
    for d in ["2026-11-19","2026-07-24","2026-06-18"]:
        r = get("/api/a-share/special-data/limit-down-pool", date_ms=ms(d), size=200)
        it = (r.get("data") or {}).get("item", []) if r["code"]==0 else []
        print("跌停池", d, r["code"], r.get("message"), "n=", len(it), "含恒为" if any(x["ticker"]=="603496" for x in it) else "", "含大唐" if any(x["ticker"]=="601991" for x in it) else "")
    for d in ["2026-11-19","2026-07-20"]:
        r = get("/api/a-share/special-data/hot-stock-list-history", date=d)
        print("历史热榜", d, r["code"], r.get("message"), "n=", len((r.get("data") or {}).get("item", [])))
if mode == "tickers":
    r = get("/api/meta/tickers/list", asset_type="a-share", limit=10000); it = r["data"]["item"]
    st = [x for x in it if "ST" in (x["name"] or "")]
    print("A股", len(it), "ST", len(st), "样例", it[0])
    r = get("/api/meta/tickers/list", asset_type="fund-etf", limit=10000); it = r["data"]["item"]
    print("ETF", len(it), [x for x in it if x["ticker"] in ("512480","588000","159915")])
if mode == "anom":
    r = get("/api/a-share/special-data/anomaly-analysis-list"); it = r["data"]["item"] if r["code"]==0 else []
    print("当日异动记录", r["code"], len(it), {t: sum(1 for x in it if x["tag_name"]==t) for t in set(x["tag_name"] for x in it)})
    r = get("/api/news/anomaly/analysis", thscodes="603496.SH", start_date="2026-10-20", end_date="2026-11-19")
    print("区间异动解读", r["code"], r.get("message"), json.dumps(r.get("data"), ensure_ascii=False)[:600])
if mode == "method":  # which adjustment method does each source use? compare first bar vs bfq
    for code, ths in [("sh601991","601991.SH"),("sh600036","600036.SH")]:
        q, b = tx(code), tx(code, fq="bfq"); d0 = sorted(q)[0]
        a = api_k(ths, d0, d0)
        ev = get("/api/a-share/corporate-actions/adjustment-factors", thscode=ths, **{"from": d0})["data"]["item"]
        D = sum(x["dividend_per_share"] for x in ev if day(x["ex_date_ms"]) > d0)
        dates = sorted(b); ratio = 1.0
        for x in ev:
            e = day(x["ex_date_ms"]);
            if e <= d0: continue
            prev = max(d for d in dates if d < e); ratio *= (1 - x["dividend_per_share"]/b[prev]["c"]) / (1 + x["per_share_bonus"])
        print(code, d0, "不复权", b[d0]["c"], "| 腾讯qfq", q[d0]["c"], "| API forward", round(a[d0]["c"],4), "| 差值法", round(b[d0]["c"]-D,4), "| 等比法", round(b[d0]["c"]*ratio,4), f"(期间分红合计 {D:.3f}, 事件 {len([x for x in ev if day(x['ex_date_ms'])>d0])})")
if mode == "anom2":
    r = get("/api/news/anomaly/analysis", thscodes="603496.SH", begin_date="2026-10-20", end_date="2026-11-19")
    print(r["code"], r.get("message"), json.dumps(r.get("data"), ensure_ascii=False)[:900])
if mode == "dump":
    import pathlib
    r = get("/api/dump/market-dumps/daily-k-10d/download-url"); print("10d", r["code"], r.get("message"))
    if r["code"] == 0:
        p = pathlib.Path("/tmp/fuyao_dk10d.parquet"); p.write_bytes(requests.get(r["data"]["presigned_url"], timeout=120).content); print("size", p.stat().st_size)
    r = get("/api/dump/market-dumps/adjustment-factors/download-url"); print("adj", r["code"], r.get("message"))
    if r["code"] == 0:
        p = pathlib.Path("/tmp/fuyao_adj.parquet"); p.write_bytes(requests.get(r["data"]["presigned_url"], timeout=120).content); print("size", p.stat().st_size)
if mode == "split":
    t = tx("sh512480"); tb = tx("sh512480", fq="bfq")
    for adj in ("none","forward"):
        a = api_k("512480.SH", "2026-06-30", "2026-07-07", adj, fund=True)
        print(adj, {d: (round(v["c"],3), int(v["v"]), round(v["a"])) for d,v in a.items()} if isinstance(a,dict) else a)
    print("腾讯qfq", {d: (t[d]["c"], int(t[d]["v"])) for d in sorted(t) if "2026-06-30"<=d<="2026-07-07"})
    print("腾讯bfq", {d: (tb[d]["c"], int(tb[d]["v"])) for d in sorted(tb) if "2026-06-30"<=d<="2026-07-07"})
