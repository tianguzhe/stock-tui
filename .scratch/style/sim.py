# /// script
# requires-python = ">=3.11"
# ///
"""Counterfactual replay of the real ledger under candidate discipline rules.
Each FIFO lot is replayed on daily closes from entry to its actual exit (open lots are marked at the last close).
A rule can only make a lot exit EARLIER at that day's close; freed cash is assumed idle (no re-entry), so
results measure loss avoided / profit forgone, not an optimised strategy."""
import json, sys, statistics as st
from collections import defaultdict
ROOT=sys.argv[1]
L=[r for r in json.load(open(f"{ROOT}/.scratch/style/ledger.json")) if r["code"] not in ("sh600036","sh601166")]
K={}
for c in {x["code"] for x in L}|{"sh000001"}:
    d=json.load(open(f"/tmp/stylek/{c}.json"))["data"][c]; rows=d.get("day") or d.get("qfqday")
    adj=lambda dd: 0.5 if (c=="sh512480" and dd<"2026-07-03") else 1.0
    K[c]={x[0]:float(x[2])*adj(x[0]) for x in rows}
TD=sorted(K["sh000001"]); TI={d:i for i,d in enumerate(TD)}; LAST=TD[-1]
# ---- build lots with kind + actual exits
lots=defaultdict(list); out=[]
for r in L:
    key=(r["acct"],r["code"]); q=r["qty"]
    if r["side"]=="买入":
        held=sum(x["q"] for x in lots[key]); avg=sum(x["q"]*x["c"] for x in lots[key])/held if held else None
        kind="new" if not held else ("avgdown" if r["price"]<avg else "pyramid")
        lots[key].append(dict(q=q,c=(r["price"]*q+r["fee"])/q,d=r["date"],kind=kind,code=r["code"],name=r["name"]))
    else:
        unit=(r["price"]*q-r["fee"])/q; rem=q
        while rem>0 and lots[key]:
            l=lots[key][0]; t=min(l["q"],rem)
            out.append(dict(l,q=t,exit=r["date"],xp=unit,open=False)); l["q"]-=t; rem-=t
            if l["q"]==0: lots[key].pop(0)
for key,ls in lots.items():
    for l in ls:
        if l["q"]: out.append(dict(l,exit=LAST,xp=K[l["code"]][LAST],open=True))
actual=sum(t["q"]*(t["xp"]-t["c"]) for t in out)
print(f"实际（FIFO 已实现 + 持仓按 {LAST} 收盘）: {actual:+,.0f}，lot 数 {len(out)}，其中仍持有 {sum(t['open'] for t in out)}")
def replay(stop=None, tstop=None, skip=set()):
    tot=0; hits=0
    for t in out:
        if t["kind"] in skip: continue
        i0,i1=TI[t["buy"]] if "buy" in t else TI[t["d"]],TI[t["exit"]]
        xp=t["xp"]; ex=False
        for i in range(i0+1,i1):          # only days strictly before the actual exit can change anything
            c=K[t["code"]].get(TD[i])
            if c is None: continue
            if stop and c<=t["c"]*(1-stop): xp=c; ex=True; break
            if tstop and i-i0>=tstop and c<=t["c"]: xp=c; ex=True; break
        hits+=ex; tot+=t["q"]*(xp-t["c"])
    return tot,hits
print("\n== 亏损硬上限（收盘跌破成本 X% 当日收盘离场）")
for s in (0.05,0.06,0.08,0.10,0.12,0.15):
    v,h=replay(stop=s); print(f"  −{int(s*100)}%: {v:+,.0f}（较实际 {v-actual:+,.0f}，触发 {h} 个 lot）")
print("\n== 时间止损（持有满 N 个交易日仍未盈利当日收盘离场）")
for n in (3,5,7,10,15):
    v,h=replay(tstop=n); print(f"  N={n}: {v:+,.0f}（较实际 {v-actual:+,.0f}，触发 {h}）")
print("\n== 组合")
for s,n in ((0.08,7),(0.08,10),(0.10,10),(0.06,5)):
    v,h=replay(stop=s,tstop=n); print(f"  −{int(s*100)}% + N={n}: {v:+,.0f}（较实际 {v-actual:+,.0f}）")
print("\n== 不做某类买入（该类 lot 整体剔除）")
for k,lab in (("avgdown","不摊平"),("pyramid","不顺势加仓")):
    v,_=replay(skip={k}); print(f"  {lab}: {v:+,.0f}（较实际 {v-actual:+,.0f}）")
v,_=replay(stop=0.08,tstop=7,skip={"avgdown"}); print(f"  不摊平 + −8% + N=7: {v:+,.0f}（较实际 {v-actual:+,.0f}）")
# ---- winners cut by the rules (cost of the rule)
print("\n== 规则误伤（被提前打掉、但实际最终盈利的 lot）")
for s,n in ((0.08,None),(None,7),(0.08,7)):
    lost=0; cnt=0
    for t in out:
        if t["xp"]<=t["c"]: continue
        i0,i1=TI[t["d"]],TI[t["exit"]]
        for i in range(i0+1,i1):
            c=K[t["code"]].get(TD[i])
            if c is None: continue
            if (s and c<=t["c"]*(1-s)) or (n and i-i0>=n and c<=t["c"]):
                lost+=t["q"]*(t["xp"]-c); cnt+=1; break
    print(f"  stop={s} N={n}: 误伤 {cnt} 个盈利 lot，少赚 {lost:,.0f}")
# ---- single-name concentration
print("\n== 单票集中度：每个交易日收盘时最大单票成本占全部持仓成本比例")
mx=[]
for d in TD[TI['2026-06-01']:]:
    pos=defaultdict(float)
    for t in out:
        if t["d"]<=d<t["exit"] or (t["open"] and t["d"]<=d): pos[t["code"]]+=t["q"]*t["c"]
    if pos: mx.append((d,max(pos.values())/sum(pos.values()),max(pos,key=pos.get)))
over=[m for m in mx if m[1]>0.4]
print(f"  交易日 {len(mx)}，最大单票 > 40% 的天数 {len(over)}；当前 {mx[-1][2]} {100*mx[-1][1]:.0f}%")
print("\n== 稳健性：按建仓时间拆两段（6~7 月 / 8~11 月），各规则较实际的改善")
seg=[("06-07",lambda d:d<"2026-08-01"),("08-11",lambda d:d>="2026-08-01")]
base_out=out
for lab,f in seg:
    out=[t for t in base_out if f(t["d"])]
    act=sum(t["q"]*(t["xp"]-t["c"]) for t in out)
    res=[f"实际 {act:+,.0f}"]
    for s,n,sk,nm in ((0.08,None,set(),"−8%"),(0.05,None,set(),"−5%"),(None,3,set(),"N=3"),(None,7,set(),"N=7"),(0.08,7,set(),"−8%+N7"),(None,None,{"avgdown"},"不摊平")):
        v,_=replay(stop=s,tstop=n,skip=sk); res.append(f"{nm} {v-act:+,.0f}")
    print(f"  {lab}（lot {len(out)}）: "+" | ".join(res))
out=base_out
# winners that needed time: how long did eventual winners stay underwater?
uw=[]
for t in out:
    if t["xp"]<=t["c"]: continue
    i0,i1=TI[t["d"]],TI[t["exit"]]; days=sum(1 for i in range(i0+1,i1) if K[t["code"]].get(TD[i],1e9)<=t["c"])
    worst=min([K[t["code"]].get(TD[i],t["c"]) for i in range(i0+1,i1)] or [t["c"]])
    uw.append((days,100*(worst/t["c"]-1)))
print(f"\n最终盈利 lot {len(uw)} 个：持有中曾跌破成本的天数中位 {st.median(x[0] for x in uw)}，最深浮亏中位 {st.median(x[1] for x in uw):+.1f}%，最深浮亏 ≤ −8% 的 {sum(x[1]<=-8 for x in uw)} 个")
