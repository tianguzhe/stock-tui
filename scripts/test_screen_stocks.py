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
        "sig_overbought": 0,
        "perf_overbought_bear_win10": None,
        "perf_overbought_bear_n": None,
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

        ("顶背离胜率高排除", base_row(
            div_bear=1,
            perf_div_bear_win10=60,
            perf_div_bear_n=50,
        ), None, "顶背离W=60%≥50%"),

        ("超买且历史有效排除", base_row(
            score_total=79,
            adx=45.5,
            rs20=90,
            div_bear=1,
            perf_div_bear_win10=40,
            perf_div_bear_n=99,
            sig_overbought=1,
            perf_trend_follow_bull_win10=59,
            perf_trend_follow_bull_n=115,
            perf_overbought_bear_win10=58.3,
            perf_overbought_bear_n=48,
            vol_ratio=2.27,
            market_cap=516,
            turnover_rate=15.64,
        ), None, "超买且历史胜率58.3%>55%"),

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


if __name__ == "__main__":
    success = test_tier_logic()
    sys.exit(0 if success else 1)
