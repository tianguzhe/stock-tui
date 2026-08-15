package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/market"
	"stock-tui/internal/snapshot"
	"stock-tui/internal/store"
)

// batchSaveCmd 从 instrument 表读取所有代码，并行拉取 K 线、计算指标，
// 串行写入 SQLite（避免 SQLITE_BUSY）。复用 store 包和 indicator 包，
// 内联 HTTP 拉取逻辑（省略 verbose 输出，仅打印进度）。
func batchSaveCmd(args []string) error {
	fs := flag.NewFlagSet("batch-save", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bars := fs.Int("n", 800, "number of daily bars")
	workers := fs.Int("P", 4, "parallel workers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workers < 1 {
		*workers = 1
	}
	if *bars <= 0 {
		return fmt.Errorf("-n must be positive")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// 从 instrument 表读取所有代码
	codes, err := allCodes(st.DB())
	if err != nil {
		return fmt.Errorf("read instrument codes: %w", err)
	}
	if len(codes) == 0 {
		fmt.Println("(instrument 表为空)")
		return nil
	}
	fmt.Fprintf(os.Stderr, "batch-save: %d stocks, %d workers\n", len(codes), *workers)

	// 任务管道 + 结果管道
	type result struct {
		code string
		err  error
	}
	tasks := make(chan string, len(codes))
	results := make(chan result, len(codes))

	// storeLock 串行化所有 SQLite 写操作（避免 SQLITE_BUSY）
	var storeLock sync.Mutex

	// 启动 worker 池
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{
				Timeout: 30 * time.Second,
				Transport: &http.Transport{
					DisableKeepAlives: true,
				},
			}
			for code := range tasks {
				err := saveOneStock(st, client, &storeLock, code, *bars)
				results <- result{code: code, err: err}
			}
		}()
	}

	// 发送任务
	go func() {
		for _, code := range codes {
			tasks <- code
		}
		close(tasks)
	}()

	// 等待 worker 完成
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果，打印进度
	var okCount, errCount int
	var lastReport time.Time
	for r := range results {
		if r.err != nil {
			errCount++
			fmt.Fprintf(os.Stderr, "ERR %s: %v\n", r.code, r.err)
		} else {
			okCount++
		}
		if time.Since(lastReport) > 5*time.Second {
			fmt.Fprintf(os.Stderr, "progress: %d/%d ok, %d/%d err\n", okCount, len(codes), errCount, len(codes))
			lastReport = time.Now()
		}
	}
	fmt.Printf("batch-save: %d success, %d failed out of %d\n", okCount, errCount, len(codes))
	return nil
}

func allCodes(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT code FROM instrument ORDER BY code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}

// saveOneStock 抓取单只股票日 K + 换手率，计算指标，保存快照。
// storeLock 串行化 SQLite 写操作（避免 SQLITE_BUSY）。
func saveOneStock(st *store.Store, client *http.Client, storeLock *sync.Mutex, code string, bars int) error {
	ck, ok := market.NormalizeCode(code)
	if !ok {
		return fmt.Errorf("invalid code: %s", code)
	}

	data, err := api.FetchDailyKline(client, ck, bars)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	snap := buildSnapshot(data)
	if snap.TradeDate == "" {
		return fmt.Errorf("无有效K线,跳过落库")
	}

	// 补市盈率等基本面
	if stocks, err := api.FetchStocks([]string{ck}); err == nil && len(stocks) > 0 {
		s := stocks[0]
		snap.TurnoverRate = s.TurnoverRate
		snap.MarketCap = s.MarketCap
		snap.PE = s.PE
	}

	storeLock.Lock()
	if err := st.UpsertInstrument(data.Code, data.Name, market.Prefix(data.Code), ""); err != nil {
		storeLock.Unlock()
		return fmt.Errorf("upsert: %w", err)
	}
	if err := st.SaveSnapshot(snap); err != nil {
		storeLock.Unlock()
		return fmt.Errorf("save: %w", err)
	}
	storeLock.Unlock()
	return nil
}

// ——— Snapshot 构建 ———
//
// 计算逻辑已收敛到 internal/snapshot.Build，与 cmd/indicator-analyze 的
// printAnalysis 共用同一实现——两者曾各自手工构造 ~90 字段的 Snapshot，
// 新增列必须同步改两处，容易漏改导致落库口径分叉。

func buildSnapshot(data api.KlineData) store.Snapshot {
	return snapshot.Build(data).Snap
}
