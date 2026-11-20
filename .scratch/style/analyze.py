# /// script
# requires-python = ">=3.11"
# ///
"""Trading-style analytics on the rebuilt ledger. FIFO per (account, code); fees included in P&L.
Dividend-book names (招行/兴业) are excluded from short-term statistics because they follow opposite rules."""
import json, sys, math, statistics as st
from collections import defaultdict, Counter
from datetime import date
ROOT=sys.argv[1]
L=[r for r in json.load(open(f"{ROOT}/.scratch/style/ledger.json")) if r["code"] not in ("sh600036","sh601166")]
K={}
for r in {x["code"] for x in L}|{"sh000001","sz399006"}:
    d=json.load(open(f"/tmp/stylek/{r}.json"))["data"][r]; rows=d.get("day") or d.get("qfqday")
    # sh512480 split 1→2 on 2026-07-03; unadjusted prices before it are ~2x and would fake a −50% day.
    adj=lambda c,d: 0.5 if (c=="sh512480" and d<"2026-07-03") else 1.0
    K[r]={x[0]:dict(o=float(x[1])*adj(r,x[0]),c=float(x[2])*adj(r,x[0]),h=float(x[3])*adj(r,x[0]),l=float(x[4])*adj(r,x[0])) for x in rows}
TD=sorted(K["sh000001"])  # trading calendar
TI={d:i for i,d in enumerate(TD)}
def fwd(code,d,n,px=None):
    i=TI[d]; 
    if i+n>=len(TD): return None
    d2=TD[i+n]; b=px if px else K[code][d]["c"]
    return 100*(K[code][d2]["c"]/b-1) if d2 in K[code] else None
def back(code,d,n):
    i=TI[d]
    if i-n<0: return None
    a,b=TD[i-1],TD[i-1-n] if i-1-n>=0 else None
    if b is None or a not in K[code] or b not in K[code]: return None
    return 100*(K[code][a]["c"]/K[code][b]["c"]-1)
def wil(k,n):
    if not n: return (0,0)
    z=1.96;p=k/n;d=1+z*z/n;c=(p+z*z/(2*n))/d;h=z*math.sqrt(p*(1-p)/n+z*z/(4*n*n))/d;return (round(100*(c-h),1),round(100*(c+h),1))
def pr(*a): print(*a)
# ---------- A. activity by month
pr("== A. 月度活跃度")
bym=defaultdict(list)
for r in L: bym[r["date"][:7]].append(r)
for m,rs in sorted(bym.items()):
    amt=sum(r["price"]*r["qty"] for r in rs); days=len({r["date"] for r in rs})
    pr(f"{m}: 笔数 {len(rs)}（买 {sum(r['side']=='买入' for r in rs)} / 卖 {sum(r['side']=='卖出' for r in rs)}）成交额 {amt:,.0f} 交易日 {days} 标的 {len({r['code'] for r in rs})} 单笔均额 {amt/len(rs):,.0f} 费用 {sum(r['fee'] for r in rs):.2f}")
# ---------- B. FIFO + buy classification + Odean
lots=defaultdict(list)   # (acct,code) -> [ [qty, unit_cost_incl_fee, date] ]
trips=[]; buys=[]; RG=RL=PG=PL=0
for r in L:
    key=(r["acct"],r["code"]); q=r["qty"]
    if r["side"]=="买入":
        held=sum(x[0] for x in lots[key])
        avg=(sum(x[0]*x[1] for x in lots[key])/held) if held else None
        kind="新开仓" if not held else ("浮亏加仓(摊平)" if r["price"]<avg else "浮盈加仓(顺势)")
        buys.append(dict(r,kind=kind,avg=avg,paper=(100*(r["price"]/avg-1) if avg else None)))
        lots[key].append([q,(r["price"]*q+r["fee"])/q,r["date"]])
    else:
        # Odean counting on sell days: every other position in the same account is a paper gain/loss
        for (a,c),ls in lots.items():
            if a!=r["acct"] or c==r["code"] or not ls: continue
            if r["date"] not in K[c]: continue
            hq=sum(x[0] for x in ls); avg=sum(x[0]*x[1] for x in ls)/hq
            if K[c][r["date"]]["c"]>avg: PG+=1
            else: PL+=1
        unit=(r["price"]*q-r["fee"])/q; rem=q; pnl=0; cost=0
        first=lots[key][0][2] if lots[key] else None
        while rem>0 and lots[key]:
            lq,lc,ld=lots[key][0]; take=min(lq,rem)
            trips.append(dict(code=r["code"],name=r["name"],acct=r["acct"],buy=ld,sell=r["date"],qty=take,
                              pnl=take*(unit-lc),ret=100*(unit/lc-1),hold=TI[r["date"]]-TI[ld]))
            pnl+=take*(unit-lc); cost+=take*lc
            lots[key][0][0]-=take; rem-=take
            if lots[key][0][0]==0: lots[key].pop(0)
        if pnl>0: RG+=1
        else: RL+=1
        # position that remains after a partial sale is also a paper position
        if lots[key]:
            hq=sum(x[0] for x in lots[key]); avg=sum(x[0]*x[1] for x in lots[key])/hq
            if K[r["code"]][r["date"]]["c"]>avg: PG+=1
            else: PL+=1
pr("\n== B. FIFO 配对（含费）")
W=[t for t in trips if t["pnl"]>0]; Ls=[t for t in trips if t["pnl"]<=0]
tot=sum(t["pnl"] for t in trips)
pr(f"配对 {len(trips)} 组 已实现 {tot:+,.2f} 胜率 {100*len(W)/len(trips):.0f}% 均盈 {st.mean(t['pnl'] for t in W):+.0f} 均亏 {st.mean(t['pnl'] for t in Ls):+.0f} 盈亏比 {st.mean(t['pnl'] for t in W)/-st.mean(t['pnl'] for t in Ls):.2f}")
pr(f"收益率：盈利单中位 {st.median(t['ret'] for t in W):+.1f}% / 亏损单中位 {st.median(t['ret'] for t in Ls):+.1f}% ；最大单笔亏 {min(t['ret'] for t in trips):+.1f}% 最大单笔盈 {max(t['ret'] for t in trips):+.1f}%")
pr(f"持有期（交易日）：盈利单中位 {st.median(t['hold'] for t in W)} 日 / 亏损单中位 {st.median(t['hold'] for t in Ls)} 日")
pr(f"亏损单中 ≤ −10% 才止损的组数 {sum(t['ret']<=-10 for t in Ls)}/{len(Ls)}，合计 {sum(t['pnl'] for t in Ls if t['ret']<=-10):+,.0f}（占总亏损 {100*sum(t['pnl'] for t in Ls if t['ret']<=-10)/sum(t['pnl'] for t in Ls):.0f}%）")
for lo,hi,lab in [(0,3,"0–3日"),(4,7,"4–7日"),(8,15,"8–15日"),(16,999,">15日")]:
    g=[t for t in trips if lo<=t["hold"]<=hi]
    if g: pr(f"  持有 {lab}: {len(g)} 组 胜率 {100*sum(t['pnl']>0 for t in g)/len(g):.0f}% 合计 {sum(t['pnl'] for t in g):+,.0f}")
pr("按标的（已实现）:")
bc=defaultdict(float)
NM={r["code"]:r["name"] for r in L if r["name"] not in ("低波红利","恒科技","半导体","创新药指","XD凯盛科")}
for t in trips: bc[NM.get(t["code"],t["name"])]+=t["pnl"]
for n,v in sorted(bc.items(),key=lambda x:x[1]): pr(f"  {n} {v:+,.0f}")
PGR=RG/(RG+PG); PLR=RL/(RL+PL)
pr(f"\nOdean 处置效应：卖出日 RG={RG} PG={PG} RL={RL} PL={PL} → PGR={PGR:.3f} PLR={PLR:.3f} 比值 PGR/PLR={PGR/PLR:.2f}（>1 即倾向卖赢留亏）")
# ---------- C. buy kinds
pr("\n== C. 买入类型")
for k in ("新开仓","浮盈加仓(顺势)","浮亏加仓(摊平)"):
    g=[b for b in buys if b["kind"]==k]
    f10=[fwd(b["code"],b["date"],10,b["price"]) for b in g]; f10=[x for x in f10 if x is not None]
    amt=sum(b["price"]*b["qty"] for b in g)
    extra=f" 买入时浮亏中位 {st.median(b['paper'] for b in g):+.1f}%" if k.startswith("浮亏") else ""
    pr(f"{k}: {len(g)} 笔 金额 {amt:,.0f}（{100*amt/sum(b['price']*b['qty'] for b in buys):.0f}%）10日后 胜率 {100*sum(x>0 for x in f10)/len(f10):.0f}% {wil(sum(x>0 for x in f10),len(f10))} 均值 {st.mean(f10):+.2f}%{extra}")
bym=Counter((b["date"][:7],b["kind"]) for b in buys)
for m in sorted({b['date'][:7] for b in buys}):
    pr(f"  {m}: "+" / ".join(f"{k} {bym[(m,k)]}" for k in ("新开仓","浮盈加仓(顺势)","浮亏加仓(摊平)")))
# ---------- D. entry/exit context
pr("\n== D. 买卖时点")
def ctx(rs,lab):
    b5=[back(r["code"],r["date"],5) for r in rs]; b5=[x for x in b5 if x is not None]
    dchg=[100*(K[r["code"]][r["date"]]["c"]/K[r["code"]][TD[TI[r["date"]]-1]]["c"]-1) for r in rs if TD[TI[r["date"]]-1] in K[r["code"]]]
    pos=[(r["price"]-K[r["code"]][r["date"]]["l"])/(K[r["code"]][r["date"]]["h"]-K[r["code"]][r["date"]]["l"]) for r in rs if K[r["code"]][r["date"]]["h"]>K[r["code"]][r["date"]]["l"]]
    f5=[fwd(r["code"],r["date"],5,r["price"]) for r in rs]; f5=[x for x in f5 if x is not None]
    f10=[fwd(r["code"],r["date"],10,r["price"]) for r in rs]; f10=[x for x in f10 if x is not None]
    i10=[fwd("sh000001",r["date"],10) for r in rs]; i10=[x for x in i10 if x is not None]
    pr(f"{lab} n={len(rs)}：前5日涨幅中位 {st.median(b5):+.2f}%（>0 占 {100*sum(x>0 for x in b5)/len(b5):.0f}%）｜当日涨跌中位 {st.median(dchg):+.2f}%｜成交价日内分位中位 {100*st.median(pos):.0f}%（>50% 占 {100*sum(x>0.5 for x in pos)/len(pos):.0f}%）｜成交后5日 {st.mean(f5):+.2f}% 10日 {st.mean(f10):+.2f}%（胜率 {100*sum(x>0 for x in f10)/len(f10):.0f}% {wil(sum(x>0 for x in f10),len(f10))}）同期上证10日 {st.mean(i10):+.2f}%")
ctx([r for r in L if r["side"]=="买入"],"买入")
ctx([r for r in L if r["side"]=="卖出"],"卖出")
pr("  （卖出行的「成交后」收益 >0 表示卖飞，<0 表示卖对）")
# ---------- E. time of day
pr("\n== E. 下单时段")
def slot(t):
    h=int(t[:2])*60+int(t[3:])
    return "09:30-10:00" if h<600 else "10:00-11:30" if h<=690 else "13:00-14:30" if h<870 else "14:30-15:00"
for s in ("09:30-10:00","10:00-11:30","13:00-14:30","14:30-15:00"):
    g=[r for r in L if slot(r["time"])==s]
    pr(f"{s}: {len(g)} 笔（买 {sum(r['side']=='买入' for r in g)} / 卖 {sum(r['side']=='卖出' for r in g)}）")
# ---------- F. concentration
pr("\n== F. 集中度（买入金额）")
ba=defaultdict(float)
for r in L:
    if r["side"]=="买入": ba[r["code"]]+=r["price"]*r["qty"]
T=sum(ba.values())
NM2={r["code"]:r["name"] for r in L}
for n,v in sorted(ba.items(),key=lambda x:-x[1])[:6]: pr(f"  {NM.get(n,NM2[n])} {v:,.0f}（{100*v/T:.1f}%）")
pr(f"  富联买入笔数 {sum(1 for r in L if r['name']=='工业富联' and r['side']=='买入')} / 全部买入 {sum(1 for r in L if r['side']=='买入')}")
# ---------- G. same-day / round-trip churn
same=sum(1 for t in trips if t["hold"]==0); pr(f"\n== G. T+0/次日：持有 0 日配对 {same}，1 日 {sum(1 for t in trips if t['hold']==1)}")
json.dump(trips,open(f"{ROOT}/.scratch/style/trips.json","w"),ensure_ascii=False)
json.dump(buys,open(f"{ROOT}/.scratch/style/buys.json","w"),ensure_ascii=False)
# ---------- H. P&L attribution by buy kind, realized + open lots marked at last close
pr("\n== H. 按买入类型归因（已实现 + 持仓浮盈，按最后收盘价）")
lots2=defaultdict(list); attr=defaultdict(float); cnt=Counter()
for r in L:
    key=(r["acct"],r["code"]); q=r["qty"]
    if r["side"]=="买入":
        held=sum(x[0] for x in lots2[key]); avg=(sum(x[0]*x[1] for x in lots2[key])/held) if held else None
        kind="新开仓" if not held else ("浮亏加仓(摊平)" if r["price"]<avg else "浮盈加仓(顺势)")
        lots2[key].append([q,(r["price"]*q+r["fee"])/q,kind])
    else:
        unit=(r["price"]*q-r["fee"])/q; rem=q
        while rem>0 and lots2[key]:
            lq,lc,kd=lots2[key][0]; take=min(lq,rem); attr[kd]+=take*(unit-lc); rem-=take; lots2[key][0][0]-=take
            if lots2[key][0][0]==0: lots2[key].pop(0)
last=TD[-1]; openv=defaultdict(float)
for (a,c),ls in lots2.items():
    for lq,lc,kd in ls:
        if lq: openv[kd]+=lq*(K[c][last]["c"]-lc); cnt[kd]+=lq
for kd in ("新开仓","浮盈加仓(顺势)","浮亏加仓(摊平)"):
    pr(f"{kd}: 已实现 {attr[kd]:+,.0f} + 持仓浮盈 {openv[kd]:+,.0f} = {attr[kd]+openv[kd]:+,.0f}（仍持有 {cnt[kd]} 股/份）")
pr(f"合计 已实现 {sum(attr.values()):+,.0f} 持仓浮盈 {sum(openv.values()):+,.0f}（截至 {last}）")
# ---------- I. down-day buying / up-day selling share, and execution quality
bb=[r for r in L if r["side"]=="买入"]; ss=[r for r in L if r["side"]=="卖出"]
def dc(r): 
    p=TD[TI[r["date"]]-1]; return K[r["code"]][r["date"]]["c"]/K[r["code"]][p]["c"]-1
pr(f"\n== I. 买在下跌日 {100*sum(dc(r)<0 for r in bb)/len(bb):.0f}%（≤ −3% 占 {100*sum(dc(r)<=-0.03 for r in bb)/len(bb):.0f}%）；卖在上涨日 {100*sum(dc(r)>0 for r in ss)/len(ss):.0f}%（≥ +3% 占 {100*sum(dc(r)>=0.03 for r in ss)/len(ss):.0f}%）")
fb=[r for r in bb if r["code"]=="sh601138"]
f10=[fwd(r["code"],r["date"],10,r["price"]) for r in fb]; f10=[x for x in f10 if x is not None]
pr(f"富联买入 {len(fb)} 笔：10 日后胜率 {100*sum(x>0 for x in f10)/len(f10):.0f}% {wil(sum(x>0 for x in f10),len(f10))} 均值 {st.mean(f10):+.2f}%")
# ---------- J. dip-buy vs others, and holding-period exit rule check
pr("\n== J. 买入当日涨跌分组 → 10 日后（相对成交价）")
for lab,f in [("当日 ≤ −3%",lambda x:x<=-0.03),("−3% ~ 0",lambda x:-0.03<x<0),("当日 ≥ 0",lambda x:x>=0)]:
    g=[r for r in bb if f(dc(r))]; x=[fwd(r["code"],r["date"],10,r["price"]) for r in g]; x=[v for v in x if v is not None]
    if x: pr(f"{lab}: n={len(x)} 胜率 {100*sum(v>0 for v in x)/len(x):.0f}% {wil(sum(v>0 for v in x),len(x))} 均值 {st.mean(x):+.2f}% 中位 {st.median(x):+.2f}%")
pr("\n== K. 卖出当日涨跌分组 → 卖出后 10 日（>0＝卖飞）")
for lab,f in [("当日 ≥ +3%",lambda x:x>=0.03),("0 ~ +3%",lambda x:0<=x<0.03),("当日 < 0",lambda x:x<0)]:
    g=[r for r in ss if f(dc(r))]; x=[fwd(r["code"],r["date"],10,r["price"]) for r in g]; x=[v for v in x if v is not None]
    if x: pr(f"{lab}: n={len(x)} 卖后上涨占 {100*sum(v>0 for v in x)/len(x):.0f}% {wil(sum(v>0 for v in x),len(x))} 均值 {st.mean(x):+.2f}%")
