package main

import (
	"context"
	"fmt"
	"time"

	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V03-WEEKLY 的 PO 裁示（2026-09-20）。
//
//	pb report [--week YYYY-MM-DD] [--project X] [--json]
//	- 不帶 --week＝本週；--week 取該日所在那一週（Asia/Taipei 週一 00:00 起算）。
//	- 三塊：誰做了什麼、收了什麼（verify）、還開著什麼。
//	- 只讀：不發通知、不寫信（REST GET /api/report 回同一份 JSON 的欄位命名）。

// weekLayout：--week 的輸入格式（該日所在週）。
const weekLayout = "2006-01-02"

func (a *app) cmdReport(args []string) int {
	fs := a.newFlagSet("report")
	db := a.dbFlag(fs)
	week := fs.String("week", "", "哪一週（YYYY-MM-DD；省略＝本週）")
	project := fs.String("project", "", "限定專案（預設全庫）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		return a.usageErr("用法：pb report [--week YYYY-MM-DD] [--project X] [--json]")
	}
	ref, err := parseWeekRef(*week, time.Now())
	if err != nil {
		return a.usageErr("%s", err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		rep, err := st.Weekly(ctx, *project, ref)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toWeeklyJSON(rep))
		}
		a.printWeekly(rep)
		return nil
	})
}

// parseWeekRef：--week 空字串＝now；否則用台北時區解析 YYYY-MM-DD。
// 「該日所在週」由 store.WeekStart 決定（週一 00:00）。
func parseWeekRef(week string, now time.Time) (time.Time, error) {
	if week == "" {
		return now, nil
	}
	d, err := time.ParseInLocation(weekLayout, week, taipeiZone())
	if err != nil {
		return time.Time{}, fmt.Errorf("--week 需為 YYYY-MM-DD（例 2026-09-15）")
	}
	return d, nil
}

type weeklyJSON struct {
	WeekStart string        `json:"week_start"`
	WeekEnd   string        `json:"week_end"`
	Did       []historyJSON `json:"did"`
	Closed    []historyJSON `json:"closed"`
	Open      []nodeJSON    `json:"open"`
}

func toWeeklyJSON(rep store.WeeklyReport) weeklyJSON {
	return weeklyJSON{
		WeekStart: fmtTime(rep.WeekStart),
		WeekEnd:   fmtTime(rep.WeekEnd),
		Did:       toHistoryJSON(rep.Did),
		Closed:    toHistoryJSON(rep.Closed),
		Open:      toNodesJSON(rep.Open),
	}
}

// printWeekly：三塊純文字（markdown 風格的行，不畫表格）。
func (a *app) printWeekly(rep store.WeeklyReport) {
	fmt.Fprintf(a.stdout, "週報 %s ~ %s（Asia/Taipei，不含下週一）\n",
		rep.WeekStart.Format(weekLayout), rep.WeekEnd.Format(weekLayout))

	fmt.Fprintf(a.stdout, "誰做了什麼（%d）：\n", len(rep.Did))
	if len(rep.Did) == 0 {
		fmt.Fprintln(a.stdout, "  （無）")
	}
	for _, h := range rep.Did {
		fmt.Fprintf(a.stdout, "  %s  %-12s %-10s %s  %s\n",
			fmtTime(h.TS), h.Actor, h.Action, h.NodeID, oneLine(h.Note))
	}

	fmt.Fprintf(a.stdout, "收了什麼（%d）：\n", len(rep.Closed))
	if len(rep.Closed) == 0 {
		fmt.Fprintln(a.stdout, "  （無）")
	}
	for _, h := range rep.Closed {
		fmt.Fprintf(a.stdout, "  %s  %-12s %s  %s\n", fmtTime(h.TS), h.Actor, h.NodeID, oneLine(h.Note))
	}

	fmt.Fprintf(a.stdout, "還開著什麼（%d）：\n", len(rep.Open))
	if len(rep.Open) == 0 {
		fmt.Fprintln(a.stdout, "  （無）")
	}
	for _, n := range rep.Open {
		fmt.Fprintf(a.stdout, "  %-12s %s  %-12s %s\n", n.Status, n.ID, n.Owner, n.Title)
	}
}

// oneLine：note 可能多行；週報一行一筆，換行以空白接起來。
func oneLine(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			out = append(out, ' ')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// taipeiZone：讀取類命令顯示用（與 store 內部同一個固定位移）。
func taipeiZone() *time.Location { return time.FixedZone("Asia/Taipei", 8*60*60) }
