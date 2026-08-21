#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["requests"]
# ///
"""dividend-scan.py 分红序列判据的单元测试。

    uv run scripts/dividend-scan-test.py

只测纯函数（不发网络请求）。fixture **硬编码真实分红序列**，不读
data/dividend-cache/ —— 缓存已 gitignore，测试不能依赖它存在。

覆盖两组用例：
- 新增判据：div_cagr / payout_of / div_quality / sort_score
- **L1 回归**：docs/dividend-portfolio.md「L1 判据修正」记录的边界案例
  必须继续成立，确认新增维度没有动到既有筛选行为
"""
import importlib.util
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "dividend_scan", Path(__file__).resolve().parent / "dividend-scan.py")
ds = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(ds)

# ─────────────────── fixture：真实分红序列（元/股）───────────────────

XINGYE = {  # 601166 兴业银行：2023 年降 12.5%，此后微增——L1 放行的阴跌
    "2017": 0.650, "2018": 0.690, "2019": 0.762, "2020": 0.802, "2021": 1.035,
    "2022": 1.188, "2023": 1.040, "2024": 1.060, "2025": 1.066,
}
XINGYE_ANN = {"2022": {"eps": 4.20}, "2025": {"eps": 3.46}}

ZHAOHANG = {  # 600036 招商银行：9 年零下调
    "2017": 0.840, "2018": 0.940, "2019": 1.200, "2020": 1.253, "2021": 1.522,
    "2022": 1.738, "2023": 1.972, "2024": 2.000, "2025": 2.016,
}
ZHAOHANG_ANN = {"2022": {"eps": 5.26}, "2025": {"eps": 5.70}}

SANQI = {  # 002555 三七互娱：分红在涨，但 EPS 在降、派息率从 59.7% 抬到 78.0%
    "2017": 0.10, "2018": 0.30, "2019": 0.40, "2020": 0.50, "2021": 0.52,
    "2022": 0.80, "2023": 0.82, "2024": 1.00, "2025": 1.03,
}
SANQI_ANN = {"2022": {"eps": 1.34}, "2025": {"eps": 1.32}}

BATIAN = {  # 002170 芭田股份：低基数暴涨，3 年 CAGR +265%（0.015→0.73）
    "2017": 0.0, "2018": 0.0, "2019": 0.006, "2020": 0.01, "2021": 0.01,
    "2022": 0.015, "2023": 0.15, "2024": 0.28, "2025": 0.73,
}
BATIAN_ANN = {"2022": {"eps": 0.1365}, "2025": {"eps": 0.9439}}

YUTONG = {  # 600066 宇通客车：2020 年腰斩 1.0→0.5（每 10 股 10.0→5.0）
    "2017": 0.5, "2018": 0.5, "2019": 1.0, "2020": 0.5, "2021": 0.5,
    "2022": 1.0, "2023": 1.5, "2024": 1.5, "2025": 2.5,
}
BEIJING = {  # 601169 北京银行：2024→2025 降 13.1%，近 2 年恶化
    "2017": 0.267, "2018": 0.286, "2019": 0.305, "2020": 0.30, "2021": 0.305,
    "2022": 0.31, "2023": 0.32, "2024": 0.32, "2025": 0.278,
}

_fails: list[str] = []


def check(label: str, got, want, tol: float | None = None) -> None:
    ok = (abs(got - want) <= tol) if (tol is not None and got is not None) else got == want
    print(f"  {'✅' if ok else '❌'} {label}: got={got!r} want={want!r}")
    if not ok:
        _fails.append(label)


def row(series, annual, yld=5.0, cagr_years=3):
    return {"series": series, "annual": annual, "yield": yld,
            "div_cagr": ds.div_cagr(series, 2025, cagr_years)}


print("=== div_cagr：近 N 年分红复合增速 ===")
check("兴业 3 年 CAGR 为负（L1 放行的阴跌）", ds.div_cagr(XINGYE, 2025, 3), -3.55, 0.1)
check("兴业 8 年 CAGR 反而为正（远期基数稀释近期恶化）",
      ds.div_cagr(XINGYE, 2025, 8), 6.40, 0.1)
check("招行 3 年 CAGR 为正", ds.div_cagr(ZHAOHANG, 2025, 3), 5.07, 0.1)
check("数据不足返回 None", ds.div_cagr({"2025": 1.0}, 2025, 3), None)
check("首年为 0 返回 None", ds.div_cagr({"2022": 0, "2025": 1.0}, 2025, 3), None)
check("years<1 返回 None", ds.div_cagr(ZHAOHANG, 2025, 0), None)

print("\n=== payout_of：单年派息率 ===")
check("兴业 2025 派息率", ds.payout_of(XINGYE, XINGYE_ANN, 2025), 30.81, 0.1)
check("兴业 2022 派息率", ds.payout_of(XINGYE, XINGYE_ANN, 2022), 28.29, 0.1)
check("EPS 缺失返回 None", ds.payout_of(XINGYE, {}, 2025), None)

print("\n=== div_quality：分红增长的来源分解 ===")
check("兴业 → 🔴 分红负增",
      ds.div_quality(row(XINGYE, XINGYE_ANN), 2025, 3)[0], "🔴")
check("招行 → 🟢 利润驱动",
      ds.div_quality(row(ZHAOHANG, ZHAOHANG_ANN), 2025, 3)[0], "🟢")
check("三七互娱 → 🟡 派息率驱动（分红涨但 EPS 降）",
      ds.div_quality(row(SANQI, SANQI_ANN), 2025, 3)[0], "🟡")
check("芭田 → 🟢（EPS 同步大涨，非缓冲垫驱动）",
      ds.div_quality(row(BATIAN, BATIAN_ANN), 2025, 3)[0], "🟢")
check("序列不足 → —",
      ds.div_quality(row({"2025": 1.0}, {}), 2025, 3)[0], "—")

print("\n=== sort_score：复合排序分与截断 ===")
check("兴业 5.89% 被负增速拖低", ds.sort_score(row(XINGYE, XINGYE_ANN, 5.89)), 2.34, 0.1)
check("招行 5.18% 被正增速抬高", ds.sort_score(row(ZHAOHANG, ZHAOHANG_ANN, 5.18)), 10.25, 0.1)
check("芭田 +265% 截断到 +15（否则霸榜）",
      ds.sort_score(row(BATIAN, BATIAN_ANN, 6.50)), 21.50, 0.01)
check("CAGR 缺失按 0 计，不判否",
      ds.sort_score(row({"2025": 1.0}, {}, 5.00)), 5.00, 0.01)
check("复合排序下招行 > 兴业（当期股息率则相反）",
      ds.sort_score(row(ZHAOHANG, ZHAOHANG_ANN, 5.18))
      > ds.sort_score(row(XINGYE, XINGYE_ANN, 5.89)), True)

print("\n=== implied_pb：由 ROE 与 EPS 反推市净率 ===")
# 兴业 price 18.10 / eps 3.46 / roe 9.15 → 腾讯 qt 实测 PB 0.47
check("兴业 PB 与行情接口吻合", ds.implied_pb(18.10, 3.46, 9.15), 0.479, 0.01)
check("招行 PB 与行情接口吻合", ds.implied_pb(38.90, 5.70, 13.44), 0.917, 0.01)
check("EPS 为 0 返回 None", ds.implied_pb(10.0, 0, 10.0), None)
check("ROE 为负返回 None", ds.implied_pb(10.0, 1.0, -5.0), None)

print("\n=== 恒等式：股息率 ≡ 派息率 × ROE ÷ PB ===")
for nm, price, eps, roe, dps in [("兴业", 18.10, 3.46, 9.15, 1.066),
                                 ("招行", 38.90, 5.70, 13.44, 2.016)]:
    pb = ds.implied_pb(price, eps, roe)
    check(f"{nm} 还原股息率", (dps / eps * 100) * (roe / 100) / pb, dps / price * 100, 0.01)

print("\n=== earnings_vol / earnings_position：周期股识别 ===")
BATIAN_ANNUAL = {  # 净利 yoy 剧烈波动 + EPS 三年涨 7 倍 = 周期股伪装成红利股
    "2019": {"eps": 0.0344, "yoy": 230.2}, "2020": {"eps": 0.0873, "yoy": 153.7},
    "2021": {"eps": 0.0911, "yoy": 4.4}, "2022": {"eps": 0.1365, "yoy": 50.1},
    "2023": {"eps": 0.2916, "yoy": 114.1}, "2024": {"eps": 0.4564, "yoy": 57.7},
    "2025": {"eps": 0.9439, "yoy": 122.8},
}
ZH_ANNUAL = {  # 招行：九年 yoy 平稳
    "2017": {"eps": 2.78, "yoy": 13.0}, "2018": {"eps": 3.13, "yoy": 14.8},
    "2019": {"eps": 3.62, "yoy": 15.3}, "2020": {"eps": 3.79, "yoy": 4.8},
    "2021": {"eps": 4.61, "yoy": 23.2}, "2022": {"eps": 5.26, "yoy": 15.1},
    "2023": {"eps": 5.63, "yoy": 6.2}, "2024": {"eps": 5.66, "yoy": 1.2},
    "2025": {"eps": 5.70, "yoy": 1.2},
}
check("芭田盈利波动 σ 极高", ds.earnings_vol(BATIAN_ANNUAL, 7), 75.0, 3.0)
check("招行盈利波动 σ 低", ds.earnings_vol(ZH_ANNUAL, 7), 8.0, 3.0)
check("样本 <3 返回 None", ds.earnings_vol({"2025": {"eps": 1, "yoy": 5}}, 7), None)
check("芭田盈利位置 3.24x（周期高位）",
      ds.earnings_position(BATIAN_ANNUAL, 0.9439, 7), 3.24, 0.05)
check("招行盈利位置 1.16x（常态）",
      ds.earnings_position(ZH_ANNUAL, 5.70, 7), 1.16, 0.05)
check("EPS 样本不足返回 None",
      ds.earnings_position({"2025": {"eps": 1.0}}, 1.0, 7), None)

print("\n=== normalized_yield：周期平滑后的可持续股息率 ===")
batian = {"annual": BATIAN_ANNUAL, "payout": 77.3, "price": 11.23}
check("芭田正常化后由 6.50% 塌到 ~2%", ds.normalized_yield(batian, 7), 2.01, 0.05)

print("\n=== dq_alerts：结构性警示 ===")


def r_of(**kw):
    base = dict(div_cagr=5.0, payout=35.0, roe=13.0, evol=8.0, q_np_yoy=1.0,
                pb=0.92, div_ocf=50.0, epos=1.1, ind1="金融", norm_yield=4.5)
    base.update(kw)
    return base


check("芭田型 → 周期高位警示",
      any("周期高位" in a for a in ds.dq_alerts(r_of(epos=3.24, norm_yield=2.01))), True)
check("华夏型 → 价值陷阱警示",
      any("价值陷阱" in a for a in ds.dq_alerts(r_of(pb=0.34, roe=8.3))), True)
check("兴业型 → 陷阱 + 负增两条",
      len(ds.dq_alerts(r_of(pb=0.48, roe=9.2, div_cagr=-3.5))), 2)
check("招行型 → 无警示", ds.dq_alerts(r_of()), [])
check("低PB 但 ROE 高 → 不算陷阱", ds.dq_alerts(r_of(pb=0.7, roe=14.0)), [])

print("\n=== dq_score：八项等权加总 ===")
check("招行型满分", ds.dq_score(r_of())[0], 9)
check("满分项数为 9", ds.dq_score(r_of())[1], 9)
check("营收数据缺失 → 营收匹配项按通过（不是风控闸门）",
      "营收匹配" in ds.dq_score(r_of(q_rev_yoy=None))[2], False)
check("久立型 → 营收匹配失分",
      "营收匹配" in ds.dq_score(r_of(q_np_yoy=1.43, q_rev_yoy=-29.79))[2], True)
check("芭田型失分含盈利位置",
      "盈利位置" in ds.dq_score(r_of(epos=3.24, payout=77.3, evol=75.0, ind1="基础化工",
                                 div_ocf=48.0, roe=25.9))[2], True)
check("金融用派息率而非现金流覆盖（招行 35.4% ≤40% 通过）",
      "分红覆盖" in ds.dq_score(r_of(payout=35.4, div_ocf=None))[2], False)
check("非金融缺现金流数据 → 覆盖项失分",
      "分红覆盖" in ds.dq_score(r_of(ind1="家电", div_ocf=None))[2], True)

print("\n=== rev_np_diverge：营收/净利背离（久立案例）===")
# 久立 2026Q1：净利 +1.43% 而营收 -29.79%，3 个月后中报预减 -50~-55%
JL = dict(q_np_yoy=1.43, q_rev_yoy=-29.79)
check("久立背离 31.2pct", ds.rev_np_diverge(JL), 31.22, 0.05)
check("招行无背离", ds.rev_np_diverge(dict(q_np_yoy=1.52, q_rev_yoy=3.81)), -2.29, 0.05)
check("数据缺失返回 None", ds.rev_np_diverge(dict(q_np_yoy=None, q_rev_yoy=1.0)), None)
check("久立 → 触发红旗",
      any("营收背离" in a for a in ds.dq_alerts(r_of(**JL))), True)
check("三七 → 触发红旗（净利+59/营收-12）",
      any("营收背离" in a for a in ds.dq_alerts(r_of(q_np_yoy=59.02, q_rev_yoy=-12.32))), True)
check("🔑 芭田营收为正 → 不算红旗（背离 40.8 但营收 +19.4%）",
      any("营收背离" in a for a in ds.dq_alerts(r_of(q_np_yoy=60.26, q_rev_yoy=19.42))), False)
check("🔑 华夏反向背离 → 不算红旗（营收+35.3/净利-1.5）",
      any("营收背离" in a for a in ds.dq_alerts(r_of(q_np_yoy=-1.50, q_rev_yoy=35.33))), False)

print("\n=== latest_earnings_signal：业绩预告优先于季报 ===")
JL_FULL = dict(q_np_yoy=1.43, q_date="2026Q1", fc_lower=-55.0, fc_upper=-50.0,
               fc_date="2026-06-30")
check("久立：预告期(6-30) 晚于季报期(Q1) → 用预告 -55%",
      ds.latest_earnings_signal(JL_FULL), (-55.0, "预告2026-06"))
check("久立 L6 被拦（-55% < -10%）", ds.q_yoy_ok(JL_FULL, -10.0), False)
check("🔑 若只看季报则会放行（+1.43% > -10%）——这正是修复的盲区",
      ds.q_yoy_ok(dict(q_np_yoy=1.43, q_date="2026Q1"), -10.0), True)
check("无预告 → 回落到季报",
      ds.latest_earnings_signal(dict(q_np_yoy=5.0, q_date="2026Q1"))[1], "季报")
check("预告期早于季报期 → 用季报（正式值已出）",
      ds.latest_earnings_signal(dict(q_np_yoy=5.0, q_date="2026Q3",
                                     fc_lower=-30.0, fc_date="2026-06-30"))[0], 5.0)
check("季报缺失且无预告 → 判否（风控向保护侧失败）",
      ds.q_yoy_ok(dict(q_np_yoy=None, q_date=""), -10.0), False)
check("预告为正不拦", ds.q_yoy_ok(dict(q_np_yoy=1.0, q_date="2026Q1", fc_lower=20.0,
                                  fc_upper=30.0, fc_date="2026-06-30"), -10.0), True)

print("\n=== L1 回归：既有筛选行为不得改变 ===")
check("招行 max_cut = 0（从未下调）", ds.max_cut(ZHAOHANG), 0.0, 0.01)
check("兴业 max_cut = -12.5%（2023）", ds.max_cut(XINGYE), -12.46, 0.1)
check("兴业 recent_no_cut(2) 仍为 True（L1 确实放行）",
      ds.recent_no_cut(XINGYE, 2025, 2), True)
check("宇通 max_cut = -50%（腰斩，L1 应拦）", ds.max_cut(YUTONG), -50.0, 0.1)
check("北京银行 recent_no_cut(2) = False（2025 刚降）",
      ds.recent_no_cut(BEIJING, 2025, 2), False)
check("兴业 streak_no_cut = 2（旧 L1 计数器缺陷的来源）",
      ds.streak_no_cut(XINGYE, 2025), 2)

print(f"\n{'='*54}")
if _fails:
    print(f"❌ {len(_fails)} 条失败：{', '.join(_fails)}")
    sys.exit(1)
print("✅ 全部通过")
