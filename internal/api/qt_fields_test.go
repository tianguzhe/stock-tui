package api

import (
	"encoding/json"
	"testing"
)

// buildQt 造一个 88 元素的 qt 数组(实测长度),只填被解析的索引位。
func buildQt(set map[int]string) []json.RawMessage {
	q := make([]json.RawMessage, 88)
	for i := range q {
		q[i] = json.RawMessage(`"0"`)
	}
	for i, v := range set {
		b, _ := json.Marshal(v)
		q[i] = b
	}
	return q
}

// TestParseProxyQtFieldMapping 锁定 proxy.qq.com qt 的字段映射。
//
// fixture 取自 sh601208 东材科技 2026-07-24 收盘,并由东财 push2 接口独立
// 逐位验证(东财价格类字段为 ×100 整数):
//
//	外盘 f49=259823      ←→ qt[7]=259823
//	内盘 f161=287047     ←→ qt[8]=287047
//	量比 f50=77 (0.77)   ←→ qt[49]=0.77
//	市净率 f167=698(6.98)←→ qt[46]=6.92   ← 曾被误当作量比
//
// 该股当日 PB 6.92 是量比 0.77 的九倍，正是旧实现把个股一律判成"放量"的原因。
func TestParseProxyQtFieldMapping(t *testing.T) {
	q := buildQt(map[int]string{
		qtName:       "东材科技",
		qtOutsideVol: "259823",
		qtInsideVol:  "287047",
		qtPB:         "6.92",
		qtLimitUp:    "45.63",
		qtLimitDown:  "37.33",
		qtVolRatio:   "0.77",
	})

	name, volRatio, inside, outside := parseProxyQt(q, "sh601208")
	if name != "东材科技" {
		t.Errorf("name = %q, want 东材科技", name)
	}
	if volRatio != 0.77 {
		t.Errorf("volRatio = %v, want 0.77 (qt[49]); 取到 6.92 说明又读成了 qt[46] 市净率", volRatio)
	}
	if outside != 259823 {
		t.Errorf("outsideVol = %v, want 259823 (qt[7], 东财 f49 外盘)", outside)
	}
	if inside != 287047 {
		t.Errorf("insideVol = %v, want 287047 (qt[8], 东财 f161 内盘)", inside)
	}
}

// TestParseProxyQtETFNoPB ETF 无市净率,qt[46] 返回 0.00。旧实现据此回退本地
// 量比因而"看起来正常",掩盖了个股上的错误——这里锁定 ETF 也走 qt[49]。
func TestParseProxyQtETFNoPB(t *testing.T) {
	q := buildQt(map[int]string{
		qtName:       "半导体ETF国联安",
		qtOutsideVol: "8334525",
		qtInsideVol:  "7884177",
		qtPB:         "0.00",
		qtVolRatio:   "0.76",
	})
	_, volRatio, _, _ := parseProxyQt(q, "sh512480")
	if volRatio != 0.76 {
		t.Errorf("ETF volRatio = %v, want 0.76", volRatio)
	}
}

// TestParseProxyQtShortOrMissing qt 缺失/过短时不 panic,名称回退代码、
// 数值留零交由调用方回退本地计算。
func TestParseProxyQtShortOrMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    []json.RawMessage
	}{
		{"nil", nil},
		{"empty", []json.RawMessage{}},
		{"仅1个元素", []json.RawMessage{json.RawMessage(`"x"`)}},
		{"短于内外盘索引", make([]json.RawMessage, 5)},
		{"短于量比索引", make([]json.RawMessage, 20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, vr, in, out := parseProxyQt(tc.q, "sh600000")
			if name != "sh600000" && tc.name != "仅1个元素" {
				t.Errorf("name = %q, want 回退为代码", name)
			}
			if vr != 0 {
				t.Errorf("volRatio = %v, want 0 (交由本地回退)", vr)
			}
			if tc.name == "短于内外盘索引" && (in != 0 || out != 0) {
				t.Errorf("inside/outside = %v/%v, want 0/0", in, out)
			}
		})
	}
}

// TestFetchEMWithRetryNilClient 导出入口传 nil client 不得 panic：包内
// FetchProxyKline / FetchEMKline 等都用 httpClientOrDefault 接受 nil，
// FetchEMWithRetry 曾直接 client.Do 而在 nil 上崩溃。
// 这里只要求"不 panic"，网络失败返回 error 是正常结果。
func TestFetchEMWithRetryNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("FetchEMWithRetry(nil, ...) panicked: %v", r)
		}
	}()
	// 指向保留域名，必然失败但必须以 error 返回而非 panic。
	_, err := FetchEMWithRetry(nil, "https://invalid.invalid/api/qt/stock/get?secid=1.601208")
	if err == nil {
		t.Log("unexpected success against invalid host, but no panic — acceptable")
	}
}
