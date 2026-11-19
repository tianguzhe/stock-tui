# /// script
# requires-python = ">=3.11"
# dependencies = ["duckdb"]
# ///
import duckdb
c = duckdb.connect()
c.sql("""CREATE VIEW k AS SELECT lower(split_part(thscode,'.',2))||split_part(thscode,'.',1) AS code, date_ms, close_price c, volume v
         FROM read_parquet('/tmp/fuyao_dk10d.parquet')""")
c.sql("""CREATE VIEW r AS SELECT code, date_ms, c, v,
           LAG(c) OVER w pc,
           AVG(v) OVER (PARTITION BY code ORDER BY date_ms ROWS BETWEEN 5 PRECEDING AND 1 PRECEDING) v5,
           COUNT(*) OVER (PARTITION BY code ORDER BY date_ms ROWS BETWEEN 5 PRECEDING AND 1 PRECEDING) n5
         FROM k WINDOW w AS (PARTITION BY code ORDER BY date_ms)""")
c.sql("CREATE VIEW s AS SELECT * FROM read_csv_auto('/tmp/snap1119.csv')")
c.sql("CREATE VIEW j AS SELECT s.code, s.close sc, r.c dc, s.change_pct spct, 100*(r.c/r.pc-1) dpct, s.vol_ratio svr, r.v/r.v5 dvr FROM s JOIN r ON r.code=s.code AND r.date_ms=(SELECT MAX(date_ms) FROM k) AND r.n5=5")
print("A股匹配", c.sql("SELECT COUNT(*) FROM j").fetchall(), "全池", c.sql("SELECT COUNT(*) FROM s").fetchall(), "未匹配(ETF等)", c.sql("SELECT COUNT(*) FROM s WHERE code NOT IN (SELECT code FROM j)").fetchall())
print("收盘不一致(>0.001)", c.sql("SELECT COUNT(*) FROM j WHERE abs(sc-dc)>0.001").fetchall())
print("涨跌幅差>0.01pt", c.sql("SELECT COUNT(*), MAX(abs(spct-dpct)) FROM j WHERE abs(spct-dpct)>0.01").fetchall())
print("量比 |差|分布", c.sql("SELECT COUNT(*), ROUND(MEDIAN(abs(svr-dvr)),4), ROUND(quantile_cont(abs(svr-dvr),0.95),4), ROUND(MAX(abs(svr-dvr)),3), SUM(CASE WHEN abs(svr-dvr)>0.02 THEN 1 ELSE 0 END) FROM j WHERE svr>0").fetchall())
print("量比差>0.02 样例", c.sql("SELECT code, ROUND(svr,3), ROUND(dvr,3) FROM j WHERE svr>0 AND abs(svr-dvr)>0.02 ORDER BY abs(svr-dvr) DESC LIMIT 6").fetchall())
print("量比阈值判定不一致(0.8/1.5)", c.sql("SELECT COUNT(*) FROM j WHERE svr>0 AND ((svr<0.8)<>(dvr<0.8) OR (svr>1.5)<>(dvr>1.5))").fetchall())
a = "read_parquet('/tmp/fuyao_adj.parquet')"
print("未来除权事件(>=11-20)", c.sql(f"SELECT COUNT(*), strftime(to_timestamp(MIN(ex_date_ms)/1000)+INTERVAL 8 HOUR,'%Y-%m-%d'), strftime(to_timestamp(MAX(ex_date_ms)/1000)+INTERVAL 8 HOUR,'%Y-%m-%d') FROM {a} WHERE ex_date_ms>=1795104000000").fetchall())
print("池内未来10日除权", c.sql(f"SELECT lower(split_part(thscode,'.',2))||ticker, strftime(to_timestamp(ex_date_ms/1000)+INTERVAL 8 HOUR,'%m-%d'), dividend_per_share, per_share_bonus FROM {a} WHERE ex_date_ms>=1795104000000 AND lower(split_part(thscode,'.',2))||ticker IN (SELECT code FROM s) ORDER BY ex_date_ms").fetchall())
