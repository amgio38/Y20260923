package main

import (
	"context"
	"fmt"
	"sort"

	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V03-CHECKLIST 的 PO 裁示（2026-09-20）。
//
//	pb checklist --project <id> [--json]
//	- 母表只做索引：一列 item（<req>/ITEM-<KEY>）用 depends_on 指向既有的 issue。
//	- 依 KEY 第一個字母分組（A–K）輸出 markdown：編號、標題、狀態、owner。
//	- 沒有 item 就印空表（只有表頭），exit 0。
//	- 只讀：已匯入的 TOTAL_CHECKLIST markdown 節點一律不碰。

func (a *app) cmdChecklist(args []string) int {
	fs := a.newFlagSet("checklist")
	db := a.dbFlag(fs)
	project := fs.String("project", "", "限定專案（預設全庫）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		return a.usageErr("用法：pb checklist [--project X] [--json]")
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		rows, err := st.Checklist(ctx, *project)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toChecklistJSON(rows))
		}
		a.printChecklist(*project, rows)
		return nil
	})
}

type checklistJSON struct {
	Key     string `json:"key"`
	ItemID  string `json:"item_id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Owner   string `json:"owner"`
	IssueID string `json:"issue_id,omitempty"`
}

func toChecklistJSON(rows []store.ChecklistRow) []checklistJSON {
	out := make([]checklistJSON, 0, len(rows))
	for _, r := range rows {
		out = append(out, checklistJSON{
			Key: r.Key, ItemID: r.ItemID, Title: r.Title,
			Status: string(r.Status), Owner: r.Owner, IssueID: r.IssueID,
		})
	}
	return out
}

// printChecklist：markdown。分組＝KEY 的第一個字母（A–K 依序，其他字母照排）。
// 沒有 item 就只印表頭（空表），不另外印說明文字。
func (a *app) printChecklist(project string, rows []store.ChecklistRow) {
	title := project
	if title == "" {
		title = "全庫"
	}
	fmt.Fprintf(a.stdout, "# 母表（%s）\n\n", title)
	if len(rows) == 0 {
		fmt.Fprint(a.stdout, checklistTableHead)
		return
	}
	groups := groupByLetter(rows)
	for _, g := range groups {
		fmt.Fprintf(a.stdout, "## %s\n\n", g.letter)
		fmt.Fprint(a.stdout, checklistTableHead)
		for _, r := range g.rows {
			fmt.Fprintf(a.stdout, "| %s | %s | %s %s | %s |\n",
				r.Key, oneLine(r.Title), icon(r.Status), r.Status, r.Owner)
		}
		fmt.Fprintln(a.stdout)
	}
}

const checklistTableHead = "| 編號 | 標題 | 狀態 | Owner |\n|---|---|---|---|\n"

type checklistGroup struct {
	letter string
	rows   []store.ChecklistRow
}

// groupByLetter：依 KEY 首字母分組；字母排序（A…K…），組內維持 store 給的母表序。
func groupByLetter(rows []store.ChecklistRow) []checklistGroup {
	byLetter := map[string][]store.ChecklistRow{}
	var letters []string
	for _, r := range rows {
		l := r.Key
		if l == "" {
			l = "?"
		} else {
			l = l[:1]
		}
		if _, ok := byLetter[l]; !ok {
			letters = append(letters, l)
		}
		byLetter[l] = append(byLetter[l], r)
	}
	sort.Strings(letters)
	out := make([]checklistGroup, 0, len(letters))
	for _, l := range letters {
		out = append(out, checklistGroup{letter: l, rows: byLetter[l]})
	}
	return out
}
