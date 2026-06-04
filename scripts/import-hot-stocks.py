#!/usr/bin/env python3
"""从同花顺热榜拉取大盘A股代码，INSERT OR IGNORE 入库"""
import json, sqlite3, sys, datetime
from urllib.request import Request, urlopen

DB  = sys.argv[1] if len(sys.argv) > 1 else "data/stock.db"
URL = "https://dq.10jqka.com.cn/fuyao/hot_list_data/out/hot_list/v1/stock?stock_type=a&type=hour&list_type=skyrocket"

req = Request(URL, headers={"User-Agent": "Mozilla/5.0"})
data = json.loads(urlopen(req, timeout=10).read())
stocks = data["data"]["stock_list"]

# 大盘主板过滤：沪市(17)/深市(33)，排除创业板(3xx)和科创板(688xx)
def is_mainboard(s):
    if s["market"] not in (17, 33):
        return False
    code = s["code"]
    return not (code.startswith("3") or code.startswith("688"))

market_map = {17: "sh", 33: "sz"}
now = datetime.datetime.now().strftime("%Y-%m-%dT%H:%M:%S")

rows = [
    (market_map[s["market"]] + s["code"], s["name"], market_map[s["market"]], now)
    for s in stocks if is_mainboard(s)
]

con = sqlite3.connect(DB)
before = con.execute("SELECT COUNT(*) FROM instrument").fetchone()[0]
con.executemany(
    "INSERT OR IGNORE INTO instrument(code, name, market, note, created_at) VALUES(?,?,?,'',?)",
    rows,
)
con.commit()
inserted = con.execute("SELECT COUNT(*) FROM instrument").fetchone()[0] - before
con.close()

print(f"热榜共 {len(stocks)} 只，大盘主板 {len(rows)} 只，新增入库 {inserted} 只")
for code, name, *_ in rows:
    print(f"  {code}  {name}")
