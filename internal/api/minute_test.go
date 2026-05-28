package api

import "testing"

func TestParseMinutePayload(t *testing.T) {
	body := []byte(`{
		"code": 0,
		"msg": "",
		"data": {
			"sh600519": {
				"qt": {
					"sh600519": ["1","贵州茅台","600519","1275.98","1303.00","1290.00"]
				},
				"m1": [
					["202605281456","1274.51","1275.96","1276.01","1274.51","275.00",{},"0.22"],
					["202605281457","1275.98","1275.33","1275.98","1274.98","208.00",{},"0.17"],
					["202605281459","1275.33","1275.33","1275.33","1275.33","0.00",{},"0.00"]
				],
				"prec": "1303.00"
			}
		}
	}`)

	got, err := parseMinutePayload("sh600519", body)
	if err != nil {
		t.Fatalf("parseMinutePayload() error = %v", err)
	}
	if got.Name != "贵州茅台" {
		t.Fatalf("Name = %q, want 贵州茅台", got.Name)
	}
	assertFloat(t, "PClose", got.PClose, 1303.00)
	if got.Precision != 2 {
		t.Fatalf("Precision = %d, want 2", got.Precision)
	}
	if len(got.Points) != 3 {
		t.Fatalf("len(Points) = %d, want 3", len(got.Points))
	}
	if got.Points[0].Time != "14:56" {
		t.Fatalf("first Time = %q, want 14:56", got.Points[0].Time)
	}
	assertFloat(t, "first Price", got.Points[0].Price, 1275.96)
	assertFloat(t, "first Volume", got.Points[0].Volume, 275)
	assertFloat(t, "zero volume", got.Points[2].Volume, 0)
}

func TestParseMinutePayloadEmptyPoints(t *testing.T) {
	body := []byte(`{
		"code": 0,
		"data": {
			"sh600519": {
				"qt": {"sh600519": ["1","贵州茅台","600519","1275.98","1303.00"]},
				"m1": []
			}
		}
	}`)

	got, err := parseMinutePayload("sh600519", body)
	if err != nil {
		t.Fatalf("parseMinutePayload() error = %v", err)
	}
	if len(got.Points) != 0 {
		t.Fatalf("len(Points) = %d, want 0", len(got.Points))
	}
	assertFloat(t, "PClose", got.PClose, 1303.00)
}

func TestParseMinutePayloadSinglePoint(t *testing.T) {
	body := []byte(`{
		"code": 0,
		"data": {
			"sh600519": {
				"qt": {"sh600519": ["1","贵州茅台","600519","1275.9","1303.0"]},
				"m1": [["202605280931","1300.00","1301.00","1301.00","1300.00","10.00",{},"0.01"]]
			}
		}
	}`)

	got, err := parseMinutePayload("sh600519", body)
	if err != nil {
		t.Fatalf("parseMinutePayload() error = %v", err)
	}
	if len(got.Points) != 1 {
		t.Fatalf("len(Points) = %d, want 1", len(got.Points))
	}
	if got.Precision != 1 {
		t.Fatalf("Precision = %d, want 1", got.Precision)
	}
	if got.Points[0].Time != "09:31" {
		t.Fatalf("Time = %q, want 09:31", got.Points[0].Time)
	}
}
