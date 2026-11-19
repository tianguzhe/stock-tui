# /// script
# requires-python = ">=3.11"
# dependencies = ["duckdb"]
# ///
import duckdb
c = duckdb.connect()
for f in ["/tmp/fuyao_dk10d.parquet", "/tmp/fuyao_adj.parquet"]:
    print("==", f); print(c.sql(f"DESCRIBE SELECT * FROM read_parquet('{f}')").fetchall())
    print(c.sql(f"SELECT COUNT(*), COUNT(DISTINCT thscode) FROM read_parquet('{f}')").fetchall())
print(c.sql("SELECT MIN(date_ms), MAX(date_ms), COUNT(DISTINCT date_ms) FROM read_parquet('/tmp/fuyao_dk10d.parquet')").fetchall())
print(c.sql("SELECT * FROM read_parquet('/tmp/fuyao_dk10d.parquet') WHERE thscode IN ('603496.SH','512480.SH','601138.SH') ORDER BY date_ms DESC LIMIT 4").fetchall())
print(c.sql("SELECT * FROM read_parquet('/tmp/fuyao_adj.parquet') WHERE thscode='601991.SH' ORDER BY 2 DESC LIMIT 3").fetchall())
print(c.sql("SELECT MIN(ex_date_ms), MAX(ex_date_ms) FROM read_parquet('/tmp/fuyao_adj.parquet')").fetchall())
