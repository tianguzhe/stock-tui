#!/usr/bin/env python3
"""screen-stocks.py 单元测试 — 直接测试真实筛选逻辑，避免测试副本漂移。"""
import importlib.util
import pathlib
import sys


SCRIPT = pathlib.Path(__file__).with_name("screen-stocks.py")
spec = importlib.util.spec_from_file_location("screen_stocks", SCRIPT)
screen_stocks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(screen_stocks)


def base_row(**overrides):
    row = {
        "code": "sh600000",
        "name": "测试股份",
        "hot_score": 0,
        "change_pct": 1.5,
        "score_total": 72,
        "adx": 42.0,
        "rs20": 85,
        "rs60": 85,
        "rs120": 85,
        "bias24": 5.0,
        "atr_pct": 5.0,
        "streak": 2,
        "ma20": 9.0,
        "sar_long": 1,
        "supertrend_long": 1,
        "obv_up": 1,
        "macd_hist": 1.0,
        "div_bear": 0,
        "perf_div_bear_win10": None,
        "perf_div_bear_n": None,
        "td_setup": "见顶/3",
        "td_countdown": "-/0",
        "vol_ratio": 1.0,
        "market_cap": 300,
        "turnover_rate": 10.0,
        "perf_trend_follow_bull_win10": None,
        "perf_trend_follow_bull_n": None,
        "perf_trend_follow_bull_avg10": None,
        "sig_overbought": 0,
        "perf_overbought_bear_win10": None,
        "perf_overbought_bear_n": None,
        "keltner_squeeze": 0,
        "donch_break20_bull": 0,
        "donch_break55_bull": 0,
        "sar_value": 9.5,
        "supertrend_value": 9.3,
        "close": 10.0,
    }
    row.update(overrides)
    return row


def test_tier_logic():
    tests = [
        ("红盘强势推荐", base_row(
            change_pct=2.8,
            score_total=72,
            adx=46.7,
            rs20=94,
            macd_hist=6.31,
            div_bear=1,
            perf_div_bear_win10=43,
            perf_div_bear_n=100,
            td_setup="见顶/4",
            vol_ratio=1.10,
            market_cap=267,
            turnover_rate=12.59,
        ), "⭐⭐⭐", "红盘+技术完美+顶背离W<50%+未到末端"),

        ("末端追高降为观察", base_row(
            change_pct=5.65,
            score_total=76,
            adx=49.7,
            rs20=89,
            macd_hist=0.49,
            div_bear=1,
            perf_div_bear_win10=48,
            perf_div_bear_n=64,
            td_setup="见顶/6",
            vol_ratio=1.54,
            market_cap=140,
            turnover_rate=17.45,
        ), "👁️观察", "涨幅/放量/TD/顶背离触发末端风险"),

        ("强势股深度回调观察", base_row(
            change_pct=-4.58,
            score_total=73,
            adx=41.7,
            rs20=92,
            macd_hist=1.83,
            td_setup="见顶/5",
            vol_ratio=1.02,
            market_cap=449,
            turnover_rate=8.55,
        ), "👁️观察", "强势股RS≥80,-5%~0%,量比<1.5"),

        ("一般股轻微回踩观察", base_row(
            change_pct=-0.44,
            score_total=72,
            adx=48.0,
            rs20=62,
            macd_hist=0.15,
            td_setup="见底/1",
            vol_ratio=0.98,
            market_cap=194,
            turnover_rate=11.86,
        ), "👁️观察", "一般股-2%~0%,量比<1.0"),

        ("基本面缺失排除", base_row(
            market_cap=0,
            turnover_rate=10.0,
        ), None, "候选必须有有效市值/换手率"),

        ("换手率过高排除", base_row(
            change_pct=5.65,
            score_total=76,
            adx=49.7,
            rs20=89,
            div_bear=1,
            perf_div_bear_win10=48,
            perf_div_bear_n=64,
            td_setup="见顶/6",
            vol_ratio=1.54,
            market_cap=140,
            turnover_rate=21.0,
        ), None, "换手率21%>20%"),

        ("RS20不足排除", base_row(rs20=55), None, "RS20=55<60"),

        ("顶背离不显著强趋势容忍", base_row(
            div_bear=1,
            perf_div_bear_win10=60,
            perf_div_bear_n=50,
        ), "⭐⭐⭐", "W=60%@N=50 Wilson下界46%<50 不显著，ADX42强趋势容忍"),

        ("顶背离显著有效排除", base_row(
            div_bear=1,
            perf_div_bear_win10=68,
            perf_div_bear_n=100,
        ), None, "W=68%@N=100 Wilson下界58%>50 显著有效"),

        ("超买且历史显著有效排除", base_row(
            score_total=79,
            adx=45.5,
            rs20=90,
            div_bear=1,
            perf_div_bear_win10=40,
            perf_div_bear_n=99,
            sig_overbought=1,
            perf_trend_follow_bull_win10=59,
            perf_trend_follow_bull_n=115,
            perf_overbought_bear_win10=65,
            perf_overbought_bear_n=100,
            vol_ratio=2.27,
            market_cap=516,
            turnover_rate=15.64,
        ), None, "超买反转W=65%@N=100 Wilson下界55%>50"),

        ("TD setup见顶8排除", base_row(td_setup="见顶/8"), None, "TD setup见顶/8≥8"),

        ("跌幅过大排除", base_row(
            change_pct=-3.5,
            score_total=67,
            adx=48.0,
            rs20=79,
            div_bear=1,
            perf_div_bear_win10=49,
            perf_div_bear_n=102,
            td_setup="见底/1",
            vol_ratio=1.32,
            market_cap=230,
            turnover_rate=10.57,
        ), None, "一般股-3.5%<-2%"),
    ]

    passed = 0
    failed = 0
    for name, data, expected, reason in tests:
        result = screen_stocks.tier(data)
        if result == expected:
            passed += 1
            print(f"✅ {name}: {expected or '排除'} ({reason})")
        else:
            failed += 1
            print(f"❌ {name}: 期望 {expected or '排除'}，实际 {result or '排除'} ({reason})")

    print(f"\n测试结果: {passed}/{len(tests)} 通过, {failed} 失败")
    return failed == 0


def test_td_format():
    """复现 bug：td_countdown 落库格式为 "见顶/N"（tdSignalText），而非 "C顶N"。"""
    ok = True

    # signals() 应对 countdown 顶部序列生成 ⚠️C顶N 警示
    sig = screen_stocks.signals(base_row(td_countdown="见顶/7"))
    if "⚠️C顶7" in sig:
        print("✅ signals countdown警示: 见顶/7 → ⚠️C顶7")
    else:
        print(f"❌ signals countdown警示: 见顶/7 未生成 ⚠️C顶7，实际: {sig}")
        ok = False

    # 见底/空 countdown 不触发警示
    for cdwn in ("见底/5", "-/0", ""):
        sig = screen_stocks.signals(base_row(td_countdown=cdwn))
        if "⚠️C顶" in sig:
            print(f"❌ signals countdown警示误报: {cdwn!r} → {sig}")
            ok = False
        else:
            print(f"✅ signals countdown不误报: {cdwn!r}")

    # sort_key 对 countdown 顶部序列 ≥7 施加惩罚 50（tuple 第2位）
    p7 = screen_stocks.sort_key(base_row(td_countdown="见顶/7"))[1]
    p6 = screen_stocks.sort_key(base_row(td_countdown="见顶/6"))[1]
    if p7 == 50 and p6 == 0:
        print("✅ sort_key countdown惩罚: 见顶/7 → 50, 见顶/6 → 0")
    else:
        print(f"❌ sort_key countdown惩罚: 见顶/7 → {p7}（期望50）, 见顶/6 → {p6}（期望0）")
        ok = False

    return ok


def test_new_indicators():
    """avg10 硬过滤 + Squeeze/Donchian 标记与排序加分。"""
    ok = True

    # avg10 ≤ 0 且样本充分 → 排除
    cases = [
        ("avg10为负排除", base_row(
            perf_trend_follow_bull_win10=50,
            perf_trend_follow_bull_n=20,
            perf_trend_follow_bull_avg10=-0.5,
        ), None),
        ("avg10为正放行", base_row(
            perf_trend_follow_bull_win10=50,
            perf_trend_follow_bull_n=20,
            perf_trend_follow_bull_avg10=6.2,
        ), "⭐⭐⭐"),
        ("avg10样本不足放行", base_row(
            perf_trend_follow_bull_win10=50,
            perf_trend_follow_bull_n=9,
            perf_trend_follow_bull_avg10=-0.5,
        ), "⭐⭐⭐"),
        ("avg10为NULL放行", base_row(
            perf_trend_follow_bull_win10=50,
            perf_trend_follow_bull_n=20,
            perf_trend_follow_bull_avg10=None,
        ), "⭐⭐⭐"),
    ]
    for name, data, expected in cases:
        result = screen_stocks.tier(data)
        if result == expected:
            print(f"✅ {name}: {expected or '排除'}")
        else:
            print(f"❌ {name}: 期望 {expected or '排除'}，实际 {result or '排除'}")
            ok = False

    # signals 标记：破D55 覆盖 破D20；Squeeze 仅标记；趋势A10 展示
    sig = screen_stocks.signals(base_row(
        donch_break55_bull=1, donch_break20_bull=1, keltner_squeeze=1,
        perf_trend_follow_bull_n=20, perf_trend_follow_bull_avg10=6.25,
    ))
    checks = [
        ("破D55" in sig, "破D55 标记"),
        ("破D20" not in sig, "破D55 覆盖 破D20 不叠加"),
        ("Squeeze压缩" in sig, "Squeeze 标记"),
        ("趋势A10=+6.2%" in sig, "趋势A10 展示"),
    ]
    sig20 = screen_stocks.signals(base_row(donch_break20_bull=1))
    checks.append(("破D20" in sig20, "仅破D20 标记"))
    for passed, name in checks:
        if passed:
            print(f"✅ signals {name}")
        else:
            print(f"❌ signals {name}，实际: {sig} / {sig20}")
            ok = False

    # sort_key breakout 分量（tuple 第5位，负值=加分提前）
    b55 = screen_stocks.sort_key(base_row(donch_break55_bull=1))[4]
    b20 = screen_stocks.sort_key(base_row(donch_break20_bull=1))[4]
    b0 = screen_stocks.sort_key(base_row())[4]
    if (b55, b20, b0) == (-2, -1, 0):
        print("✅ sort_key breakout加分: D55→-2, D20→-1, 无→0")
    else:
        print(f"❌ sort_key breakout加分: 实际 ({b55}, {b20}, {b0})，期望 (-2, -1, 0)")
        ok = False

    return ok


def test_financial_rules():
    """第1批金融学规则：综合动量、归一化乖离、连涨/换手降级、广度闸门、翻空警示。"""
    ok = True

    tier_cases = [
        # 乖离波动归一化：bias24/atr>4 触发末端降级
        ("乖离超4倍ATR降级观察", base_row(bias24=30.0, atr_pct=6.0), "👁️观察"),
        ("乖离3.3倍ATR不降级", base_row(bias24=20.0, atr_pct=6.0), "⭐⭐⭐"),
        # atr 缺失时退回固定阈值 25
        ("ATR缺失乖离26降级", base_row(bias24=26.0, atr_pct=0), "👁️观察"),
        # 连涨≥5日 短期反转风险降级
        ("连涨5日降级观察", base_row(streak=5), "👁️观察"),
        ("连涨4日不降级", base_row(streak=4), "⭐⭐⭐"),
        # 换手 15-20 梯度降级（>20 已在 _fund_ok 排除）
        ("换手16%降级观察", base_row(turnover_rate=16.0), "👁️观察"),
        ("换手14%不降级", base_row(turnover_rate=14.0), "⭐⭐⭐"),
        # 中期动量保险：rs60<45 排除
        ("rs60不足排除", base_row(rs60=40), None),
        ("rs60缺失放行", base_row(rs60=None), "⭐⭐⭐"),
    ]
    for name, data, expected in tier_cases:
        result = screen_stocks.tier(data)
        if result == expected:
            print(f"✅ {name}: {expected or '排除'}")
        else:
            print(f"❌ {name}: 期望 {expected or '排除'}，实际 {result or '排除'}")
            ok = False

    # 排序首键 = 综合动量 0.3*rs20+0.5*rs60+0.2*rs120
    m = screen_stocks.sort_key(base_row(rs20=90, rs60=70, rs120=50))[0]
    want = -(0.3 * 90 + 0.5 * 70 + 0.2 * 50)
    if abs(m - want) < 1e-9:
        print(f"✅ sort_key 综合动量首键: {-m:.1f}")
    else:
        print(f"❌ sort_key 综合动量首键: 实际 {-m}，期望 {-want}")
        ok = False

    # 持仓翻空警示（含修正：st=1/sar=0 不再误显示双多）
    stance_cases = [
        ({"sar_long": 1, "supertrend_long": 1}, "SAR/ST双多"),
        ({"sar_long": 1, "supertrend_long": 0}, "SAR多/⚠️ST翻空"),
        ({"sar_long": 0, "supertrend_long": 1}, "ST多/⚠️SAR翻空"),
        ({"sar_long": 0, "supertrend_long": 0}, "⚠️SAR/ST双空"),
    ]
    for overrides, want_mark in stance_cases:
        sig = screen_stocks.signals(base_row(**overrides))
        if want_mark in sig:
            print(f"✅ signals 趋势stance: {want_mark}")
        else:
            print(f"❌ signals 趋势stance: 期望含 {want_mark}，实际 {sig}")
            ok = False

    # 市场广度
    snap_weak = {f"c{i}": base_row(close=8.0 if i < 7 else 10.0, ma20=9.0) for i in range(10)}
    b = screen_stocks.market_breadth(snap_weak)
    if abs(b - 30.0) < 1e-9:
        print(f"✅ market_breadth: 30%")
    else:
        print(f"❌ market_breadth: 实际 {b}，期望 30.0")
        ok = False

    return ok


def test_wilson_and_stop():
    """第2/3批：Wilson 置信界、顶背离三态、止损列与建议仓位。"""
    ok = True

    # Wilson 区间数值钉死（N=10、win=40% → [16.8%, 68.7%]）
    lo, hi = screen_stocks._wilson_bounds(40, 10)
    if abs(lo - 16.8) < 0.1 and abs(hi - 68.7) < 0.1:
        print(f"✅ Wilson 区间: 40%@N=10 → [{lo:.1f}, {hi:.1f}]")
    else:
        print(f"❌ Wilson 区间: 实际 [{lo:.1f}, {hi:.1f}]，期望 [16.8, 68.7]")
        ok = False

    tier_cases = [
        # 无样本顶背离：旧版排除（无数据=有罪），新版降级观察
        ("顶背离无样本降级观察", base_row(
            div_bear=1, perf_div_bear_win10=None, perf_div_bear_n=None,
        ), "👁️观察"),
        # 不显著 + 弱趋势(ADX<38) → 观察
        ("顶背离不显著弱趋势观察", base_row(
            div_bear=1, perf_div_bear_win10=45, perf_div_bear_n=30, adx=36.0,
        ), "👁️观察"),
        # 超买不显著(58.3%@N=48 下界44%)不再排除
        ("超买不显著放行", base_row(
            sig_overbought=1, perf_overbought_bear_win10=58.3, perf_overbought_bear_n=48,
        ), "⭐⭐⭐"),
        # 追涨显著差（上界<50）排除：win=20%@N=100 上界≈28.8%
        ("追涨显著差排除", base_row(
            perf_trend_follow_bull_win10=20, perf_trend_follow_bull_n=100,
        ), None),
        # 追涨小样本差不排除：win=20%@N=5 上界≈62.4%
        ("追涨小样本不排除", base_row(
            perf_trend_follow_bull_win10=20, perf_trend_follow_bull_n=5,
        ), "⭐⭐⭐"),
    ]
    for name, data, expected in tier_cases:
        result = screen_stocks.tier(data)
        if result == expected:
            print(f"✅ {name}: {expected or '排除'}")
        else:
            print(f"❌ {name}: 期望 {expected or '排除'}，实际 {result or '排除'}")
            ok = False

    # 止损列
    stop = screen_stocks._stop_text(base_row())
    if stop == "9.50(-5.0%)":
        print(f"✅ 止损列: {stop}")
    else:
        print(f"❌ 止损列: 实际 {stop}，期望 9.50(-5.0%)")
        ok = False
    stop_none = screen_stocks._stop_text(base_row(sar_value=None))
    if stop_none == "—":
        print("✅ 止损列缺失显示 —")
    else:
        print(f"❌ 止损列缺失: 实际 {stop_none}")
        ok = False

    # 建议仓位：close=10, sar=9.5, 风险预算 68000*1%=680 → 680/0.5=1360 → 1300股
    hint = screen_stocks._position_hint(base_row(), 68000)
    if hint == "建议≤1300股":
        print(f"✅ 建议仓位: {hint}")
    else:
        print(f"❌ 建议仓位: 实际 {hint!r}，期望 建议≤1300股")
        ok = False
    if screen_stocks._position_hint(base_row(sar_value=10.5), 68000) == "":
        print("✅ 止损在上方（空头SAR）不给仓位建议")
    else:
        print("❌ 止损在上方应返回空")
        ok = False

    return ok


if __name__ == "__main__":
    success = test_tier_logic()
    success = test_td_format() and success
    success = test_new_indicators() and success
    success = test_financial_rules() and success
    success = test_wilson_and_stop() and success
    sys.exit(0 if success else 1)
