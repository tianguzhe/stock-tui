package holdings

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkipsCommentsAndBlanks(t *testing.T) {
	raw := `# 银河证券账户
sh601138:61.551:2,sz159858:0.709:151

# 国泰海通证券账户
sh601991:6.603:5
`
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d holdings, want 3: %+v", len(got), got)
	}
	if got[0].Code != "sh601138" || got[0].Shares != 2 {
		t.Errorf("got[0] = %+v, want sh601138 with 2 手", got[0])
	}
	if got[2].Code != "sh601991" || got[2].Cost != 6.603 {
		t.Errorf("got[2] = %+v, want sh601991 @6.603", got[2])
	}
}

// 同一代码分散在两个券商账户是常态；合并成本必须按手数加权，
// 否则浮盈与止损距离会整体算错，且不会有任何报错。
func TestParseMergesDuplicateCodesByWeightedCost(t *testing.T) {
	raw := `# 银河
sh600909:8.965:2,sh512480:1.122:12
# 国泰海通
sh600909:9.465:6,sh512480:1.351:8`

	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d holdings, want 2 after merge: %+v", len(got), got)
	}

	byCode := map[string]Holding{}
	for _, h := range got {
		byCode[h.Code] = h
	}

	// (8.965*2 + 9.465*6) / 8 = 9.340
	if h := byCode["sh600909"]; h.Shares != 8 || math.Abs(h.Cost-9.34) > 1e-9 {
		t.Errorf("sh600909 = %+v, want 8 手 @9.340", h)
	}
	// (1.122*12 + 1.351*8) / 20 = 1.2136
	if h := byCode["sh512480"]; h.Shares != 20 || math.Abs(h.Cost-1.2136) > 1e-9 {
		t.Errorf("sh512480 = %+v, want 20 手 @1.2136", h)
	}
}

func TestParsePreservesFirstAppearanceOrder(t *testing.T) {
	got, err := Parse("sz002185:17.9:3,sh600909:8.9:2,sz002185:18.1:1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 || got[0].Code != "sz002185" || got[1].Code != "sh600909" {
		t.Fatalf("order not preserved: %+v", got)
	}
}

// 静默跳过坏行会让持仓少算一笔而毫无症状，故一律报错。
func TestParseRejectsMalformedItems(t *testing.T) {
	for _, raw := range []string{
		"sh601138:61.551",     // 缺字段
		"sh601138:61.551:2:3", // 多字段
		"sh601138:abc:2",      // 成本非数字
		"sh601138:61.551:xyz", // 手数非数字
		"sh601138:0:2",        // 成本为零
		"sh601138:61.551:0",   // 手数为零
		"sh601138:61.551:-2",  // 手数为负
		":61.551:2",           // 代码为空
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", raw)
		}
	}
}

func TestParseEmptyInputYieldsNoHoldings(t *testing.T) {
	got, err := Parse("# 只有注释\n\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

// 行尾注释不应吞掉同一行前面的持仓。
func TestParseStripsTrailingComment(t *testing.T) {
	got, err := Parse("sh601138:61.551:2  # 07-19 加仓")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 || got[0].Shares != 2 {
		t.Fatalf("got %+v, want single 2-手 holding", got)
	}
}

func TestLoadReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".holdings")
	if err := os.WriteFile(path, []byte("# acct\nsh600909:8.965:2\nsh600909:9.465:6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Shares != 8 || math.Abs(got[0].Cost-9.34) > 1e-9 {
		t.Fatalf("got %+v, want merged 8 手 @9.340", got)
	}
}

// 调用方需要能把"没有持仓文件"与"文件损坏"区分开。
func TestLoadMissingFileWrapsErrNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("Load(missing) = nil error, want error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v does not wrap os.ErrNotExist", err)
	}
}
