# /// script
# requires-python = ">=3.11"
# dependencies = ["duckdb"]
# ///
import duckdb, datetime as dt
TZ = dt.timezone(dt.timedelta(hours=8))
c = duckdb.connect()
for code in ["001256.SZ","002827.SZ","600763.SH","601991.SH","603496.SH"]:
    rows = c.sql(f"SELECT ex_date_ms, dividend_per_share, per_share_bonus FROM read_parquet('/tmp/fuyao_adj.parquet') WHERE thscode='{code}' ORDER BY ex_date_ms DESC LIMIT 2").fetchall()
    print(code, [(m, dt.datetime.fromtimestamp(m/1000, TZ).strftime('%Y-%m-%d %a %H:%M'), d, b) for m, d, b in rows])
