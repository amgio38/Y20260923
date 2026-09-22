package main

import (
	"context"
	"fmt"

	"project_board/internal/importer"
	"project_board/internal/store"
)

// cmdImport：pb import <dir> [--project Y20260916] [--dry-run]。
// 為什麼先有 dry-run：歷史檔一百多份，分類錯了不該直接灌正式庫。
// 匯入目標預設 Y20260916（會員中心）。這支命令本身是 Y20260920 產品線的功能。
func (a *app) cmdImport(args []string) int {
	fs := a.newFlagSet("import")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	project := fs.String("project", seedProjectID, "匯入目標專案（會員中心是 Y20260916）")
	dry := fs.Bool("dry-run", false, "只列計畫，不寫 DB")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "錯誤：pb import <目錄>")
		return exitUsage
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	files, err := importer.LoadDir(fs.Arg(0))
	if err != nil {
		return a.fail(err)
	}
	items := importer.Plan(*project, files)
	if *dry {
		a.printPlan(items)
		nCreate, nSkip := countPlan(items)
		fmt.Fprintf(a.stdout, "dry-run：將建立 %d，略過 %d\n", nCreate, nSkip)
		return exitOK
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		res, err := importer.Apply(ctx, st, who, *project, items, false)
		if err != nil {
			return err
		}
		for _, w := range res.Warnings {
			fmt.Fprintln(a.stderr, "警告："+w)
		}
		fmt.Fprintf(a.stdout, "已匯入 %s：建立 %d，略過 %d\n", *project, res.Created, res.Skipped)
		return nil
	})
}

func (a *app) printPlan(items []importer.Item) {
	for _, it := range items {
		if it.Action != "create" {
			fmt.Fprintf(a.stdout, "skip  %s  %s\n", it.Title, it.Reason)
			continue
		}
		fmt.Fprintf(a.stdout, "create %-7s %-12s %s  owner=%s  parent=%s\n", it.Type, it.Status, it.ID, it.Owner, it.ParentID)
	}
}

func countPlan(items []importer.Item) (create, skip int) {
	for _, it := range items {
		if it.Action == "create" {
			create++
		} else {
			skip++
		}
	}
	return create, skip
}
