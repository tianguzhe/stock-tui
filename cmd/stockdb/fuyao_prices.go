package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"stock-tui/internal/api"
	"stock-tui/internal/backtest"
)

// fuyaoPrices adapts api.FuyaoClient to backtest.PriceSource. Each code is
// fetched once per run and cached, so every price a trade sees (entry, window,
// stop open) shares one forward-adjustment base — the property snapshot rows,
// written on different days, cannot guarantee.
type fuyaoPrices struct {
	client     *api.FuyaoClient
	start, end string
	workers    int

	mu    sync.Mutex
	cache map[string]map[string]backtest.Bar
	errs  map[string]error
}

func newFuyaoPrices(start string) (*fuyaoPrices, error) {
	c, err := api.NewFuyaoClient(nil)
	if err != nil {
		return nil, fmt.Errorf("--price-source fuyao: %w", err)
	}
	// Windows extend past --end, so fetch through today; the service caps a
	// request at 10 years, which the snapshot range (2026-06 起) never reaches.
	end := time.Now().In(cst).Format("2006-01-02")
	return &fuyaoPrices{client: c, start: start, end: end, workers: 4,
		cache: map[string]map[string]backtest.Bar{}, errs: map[string]error{}}, nil
}

func (f *fuyaoPrices) Bars(code string) (map[string]backtest.Bar, error) {
	f.mu.Lock()
	if b, ok := f.cache[code]; ok {
		f.mu.Unlock()
		return b, nil
	}
	if err, ok := f.errs[code]; ok {
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()

	raw, err := f.client.DailyBars(code, f.start, f.end)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.errs[code] = err
		return nil, err
	}
	m := make(map[string]backtest.Bar, len(raw))
	for _, b := range raw {
		m[b.Date] = backtest.Bar{Open: b.Open, High: b.High, Low: b.Low, Close: b.Close}
	}
	f.cache[code] = m
	return m, nil
}

func (f *fuyaoPrices) Prefetch(codes []string) map[string]error {
	fmt.Fprintf(os.Stderr, "info: 扶摇预取 %d 只标的日K（%s ~ %s，%d 并发）...\n", len(codes), f.start, f.end, f.workers)
	t0 := time.Now()
	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < f.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range ch {
				f.Bars(c) // errors are recorded in f.errs
			}
		}()
	}
	for _, c := range codes {
		ch <- c
	}
	close(ch)
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	failed := make(map[string]error, len(f.errs))
	for c, err := range f.errs {
		failed[c] = err
	}
	fmt.Fprintf(os.Stderr, "info: 预取完成 %d 成功 / %d 失败，用时 %s\n", len(f.cache), len(failed), time.Since(t0).Round(time.Second))
	return failed
}

// resolvePriceSource maps the --price-source flag to a backtest.PriceSource;
// "snapshot" returns nil so the engines keep their original behaviour.
func resolvePriceSource(name, start string) (backtest.PriceSource, error) {
	switch name {
	case "", "snapshot":
		return nil, nil
	case "fuyao":
		return newFuyaoPrices(start)
	}
	return nil, fmt.Errorf("--price-source 只支持 snapshot | fuyao，收到 %q", name)
}
