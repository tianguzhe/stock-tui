# /// script
# requires-python = ">=3.11"
# ///
"""Rebuild the full trade ledger (2026-06 ~ 2026-11-20) from monthly-review appendices + daily-decision Nov records.
Prices/qty/fees come from broker statement screenshots transcribed in the reviews; amount columns are ignored
because their gross/net convention differs between tables (see 2026-07.md)."""
import re, json, sys
ROOT = sys.argv[1]
NAME = {"中天科技":"sh600522","圣泉集团":"sh605589","凯中精密":"sz002823","工业富联":"sh601138","江化微":"sh603078",
 "泛微网络":"sh603039","亨通光电":"sh600487","低波红利":"sh563020","红利低波ETF":"sh563020","凯盛科技":"sh600552",
 "XD凯盛科":"sh600552","宏和科技":"sh603256","大唐发电":"sh601991","恒科技":"sh513260","恒生科技ETF":"sh513260",
 "半导体":"sh512480","半导体ETF":"sh512480","创新药指":"sz159858","创新药ETF":"sz159858","华安证券":"sh600909",
 "三花智控":"sz002050","中持股份":"sh603903","航天电器":"sz002025","华天科技":"sz002185","恒为科技":"sh603496",
 "兴业银行":"sh601166","招商银行":"sh600036","太极实业":"sh600667","中兴通讯":"sz000063"}
rows=[]
for m in ["06","07","08","09","10"]:
    acct = "银河" if m=="06" else "国泰"
    for line in open(f"{ROOT}/docs/monthly-review/2026-{m}.md"):
        if line.startswith("### 银河"): acct="银河"
        if line.startswith("### 国泰"): acct="国泰"
        c=[x.strip() for x in line.strip().strip("|").split("|")]
        if len(c)>=8 and re.fullmatch(r"\d+",c[0]) and c[2] in ("买入","卖出"):
            md,hm=c[1].split()
            fee=float(re.sub(r"[^\d.]","",c[7]))
            pos = "红利" if (len(c)>8 and "红利" in c[8]) else "短线"
            rows.append(dict(date=f"2026-{md}",time=hm,acct=acct,side=c[2],name=c[3],code=NAME[c[3]],
                             price=float(c[4]),qty=int(c[5].replace(",","")),fee=fee,book=pos,src=f"2026-{m}.md"))
# November (daily-decision execution records; all 国泰, short-term book except 大唐 is short-term too)
nov=[("2026-11-03","13:47","买入","工业富联",42.220,100,0.40),("2026-11-09","13:25","卖出","工业富联",43.230,200,5.15),
     ("2026-11-13","10:49","买入","工业富联",41.100,100,0.39),("2026-11-16","09:50","买入","工业富联",37.250,100,0.36),
     ("2026-11-16","14:07","卖出","大唐发电",4.530,500,1.34),("2026-11-16","14:09","买入","工业富联",36.100,100,0.35),
     ("2026-11-18","13:33","卖出","工业富联",38.060,200,4.54)]
for d,t,s,n,p,q,f in nov:
    rows.append(dict(date=d,time=t,acct="国泰",side=s,name=n,code=NAME[n],price=p,qty=q,fee=f,book="短线",src="daily-decision.md"))
rows.sort(key=lambda r:(r["date"],r["time"]))
json.dump(rows,open(f"{ROOT}/.scratch/style/ledger.json","w"),ensure_ascii=False,indent=0)
from collections import Counter
print(len(rows),"trades", Counter(r["date"][:7] for r in rows), Counter(r["acct"] for r in rows), Counter(r["book"] for r in rows))
