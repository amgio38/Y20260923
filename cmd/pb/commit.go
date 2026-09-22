package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-COMMIT（2026-09-20 學長裁示）。
//
//	pb commit attach [--sha <sha>] [--message-file <path>] [--dry-run]
//	- 從 commit 訊息找出單號（node id，形如 Y20260920/REQ-…/ISSUE-…），逐一
//	  link kind=commit，把 sha 接到單上。
//	- 訊息裡沒有單號、或那個 id 板上不存在 → 略過（不報錯），避免亂連。
//	- sha 省略時跑 `git rev-parse HEAD`；訊息省略時讀 stdin（供 post-commit hook 餵）。
//	- 只建立 link，不動單的狀態（不自動 move／done）。

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func (a *app) cmdCommit(args []string) int {
	if len(args) == 0 || args[0] != "attach" {
		return a.usageErr("用法：pb commit attach [--sha <sha>] [--message-file <path>] [--dry-run]")
	}
	fs := a.newFlagSet("commit")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	sha := fs.String("sha", "", "commit sha（省略＝git rev-parse HEAD）")
	msgFile := fs.String("message-file", "", "commit 訊息檔（省略＝讀 stdin）")
	dry := fs.Bool("dry-run", false, "只列出會掛的關聯，不寫入")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args[1:])); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		return a.usageErr("用法：pb commit attach [--sha <sha>] [--message-file <path>] [--dry-run]")
	}

	shaVal := *sha
	if shaVal == "" {
		out, err := exec.Command("git", "rev-parse", "HEAD").Output()
		if err != nil {
			return a.fail(fmt.Errorf("取不到 git HEAD（請帶 --sha）：%w", err))
		}
		shaVal = strings.TrimSpace(string(out))
	}
	if shaVal == "" {
		return a.usageErr("--sha 是必填（或需在 git 工作區內）")
	}

	msg, err := readMessage(*msgFile)
	if err != nil {
		return a.fail(err)
	}
	ids := domain.ExtractNodeIDs(msg)
	if len(ids) == 0 {
		fmt.Fprintln(a.stdout, "訊息中沒有單號，未掛任何 commit")
		return exitOK
	}

	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	subject := firstLine(msg)
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		var linked, skipped []string
		for _, id := range ids {
			if _, _, _, err := st.Get(ctx, id); err != nil {
				skipped = append(skipped, id)
				continue
			}
			if !*dry {
				if _, err := st.Link(ctx, who, id, domain.LinkCommit, shaVal, subject); err != nil {
					return err
				}
			}
			linked = append(linked, id)
		}
		if *asJSON {
			return a.writeJSON(map[string]any{
				"sha": shaVal, "linked": linked, "skipped": skipped, "dry_run": *dry,
			})
		}
		for _, id := range linked {
			verb := "已掛 commit"
			if *dry {
				verb = "（dry-run）會掛 commit"
			}
			fmt.Fprintf(a.stdout, "%s %s → %s\n", verb, shortSHA(shaVal), id)
		}
		for _, id := range skipped {
			fmt.Fprintf(a.stdout, "略過（板上無此單）%s\n", id)
		}
		return nil
	})
}

// readMessage：msgFile 空＝讀 stdin。
func readMessage(msgFile string) (string, error) {
	if msgFile != "" {
		b, err := os.ReadFile(msgFile)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
