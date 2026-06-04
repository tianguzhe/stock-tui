// Command stockdb manages the tagged instrument list and queries analysis
// snapshots persisted by `indicator-analyze -save` into the SQLite store.
//
// Subcommands:
//
//	stockdb tag add <code> <标签>     attach a sector/group tag
//	stockdb tag rm  <code> <标签>     detach a tag
//	stockdb list --tag <标签>         list instruments under a tag
//	stockdb history <code> [-n 15]    show a symbol's snapshot history
//	stockdb screen [--tag X] [--min-adx 25] [--max-j 80] [--min-score 60]
//	                                  filter latest snapshots by condition
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"stock-tui/internal/market"
	"stock-tui/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageErr()
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "tag":
		return cmdTag(rest)
	case "list":
		return cmdList(rest)
	case "history":
		return cmdHistory(rest)
	case "screen":
		return cmdScreen(rest)
	case "rs-rank":
		return cmdRSRank(rest)
	default:
		return usageErr()
	}
}

func usageErr() error {
	return fmt.Errorf(`usage:
  stockdb tag add <code> <标签>
  stockdb tag rm  <code> <标签>
  stockdb list --tag <标签>
  stockdb history <code> [-n 15]
  stockdb screen [--tag X] [--min-adx 25] [--max-j 80] [--min-score 60]
  stockdb rs-rank                         compute RS20/RS60/RS120 percentile ranks`)
}

func openStore() (*store.Store, error) {
	return store.Open(store.DefaultPath())
}

// normalize maps a user-typed code to the provider form, erroring on bad input.
func normalize(raw string) (string, error) {
	code, ok := market.NormalizeCode(raw)
	if !ok {
		return "", fmt.Errorf("invalid code: %s", raw)
	}
	return code, nil
}

func cmdTag(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: stockdb tag <add|rm> <code> <标签>")
	}
	action := args[0]
	code, err := normalize(args[1])
	if err != nil {
		return err
	}
	tag := args[2]

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	switch action {
	case "add":
		if err := st.AddTag(code, tag); err != nil {
			return err
		}
		fmt.Printf("tagged %s with %q\n", code, tag)
	case "rm":
		if err := st.RemoveTag(code, tag); err != nil {
			return err
		}
		fmt.Printf("removed tag %q from %s\n", tag, code)
	default:
		return fmt.Errorf("unknown tag action %q (want add|rm)", action)
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tag := fs.String("tag", "", "filter by tag")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" {
		return fmt.Errorf("usage: stockdb list --tag <标签>")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	items, err := st.ListByTag(*tag)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Printf("(无标的属于标签 %q)\n", *tag)
		return nil
	}
	fmt.Printf("标签 %q (%d 只):\n", *tag, len(items))
	for _, in := range items {
		name := in.Name
		if name == "" {
			name = "(未分析)"
		}
		fmt.Printf("  %-10s %s\n", in.Code, name)
	}
	return nil
}

func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("n", 15, "number of recent snapshots")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: stockdb history <code> [-n 15]")
	}
	code, err := normalize(fs.Arg(0))
	if err != nil {
		return err
	}
	if *limit <= 0 {
		return fmt.Errorf("-n must be positive")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.History(code, *limit)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Printf("(%s 暂无快照, 先用 indicator-analyze -save %s)\n", code, code)
		return nil
	}
	fmt.Printf("%s 近 %d 条快照演变:\n", code, len(rows))
	fmt.Printf("%-12s %8s %7s %7s %6s %6s %6s  %s\n", "date", "close", "pct%", "SCORE", "Δ", "J", "ADX", "TD")
	for _, r := range rows {
		fmt.Printf("%-12s %8.3f %+6.2f %5d %+5d %6.1f %6.1f  setup=%s cd=%s\n",
			r.TradeDate, r.Close, r.ChangePct, r.ScoreTotal, r.ScoreDelta, r.KDJ_J, r.ADX, r.TDSetup, r.TDCountdown)
	}
	return nil
}

func cmdRSRank(_ []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	n, err := st.UpdateRSRankings()
	if err != nil {
		return fmt.Errorf("rs-rank: %w", err)
	}
	fmt.Printf("rs-rank: updated %d stocks\n", n)
	return nil
}

func cmdScreen(args []string) error {
	fs := flag.NewFlagSet("screen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tag := fs.String("tag", "", "limit to a tag")
	minADX := fs.Float64("min-adx", 0, "minimum ADX")
	maxJ := fs.Float64("max-j", 0, "maximum KDJ-J (0 disabled unless set)")
	minScore := fs.Int("min-score", 0, "minimum SCORE total")
	if err := fs.Parse(args); err != nil {
		return err
	}

	f := store.Filter{
		Tag:      *tag,
		MinADX:   *minADX,
		MinScore: *minScore,
	}
	// max-j needs an explicit guard: 0 is a valid bound, so only apply when set.
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "max-j" {
			f.MaxJ = *maxJ
			f.UseMaxJ = true
		}
	})

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.Screen(f)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("(无符合条件的标的)")
		return nil
	}
	fmt.Printf("符合条件 %d 只 (按 SCORE 降序):\n", len(rows))
	fmt.Printf("%-10s %-10s %7s %6s %6s %6s  %s\n", "code", "name", "close", "SCORE", "J", "ADX", "date")
	for _, r := range rows {
		name := r.Name
		if name == "" {
			name = "(未命名)"
		}
		fmt.Printf("%-10s %-10s %7.3f %5d %6.1f %6.1f  %s\n",
			r.Instrument.Code, name, r.Close, r.ScoreTotal, r.KDJ_J, r.ADX, r.TradeDate)
	}
	return nil
}
