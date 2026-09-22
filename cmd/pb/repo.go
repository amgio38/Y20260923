package main

import (
	"context"
	"fmt"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V04-GIT-INTEGRATION/ISSUE-GIT-REPO（2026-09-20 學長裁示）。
//
//	pb repo set <project-id> --url <repo-url> [--path <local-path>] --actor <a>
//	pb repo show <project-id> [--json]
//	- 專案（project 根）↔ 一個 repo：用 link kind=repo（target=repo URL、note=本地路徑）表達。
//	- set 是 idempotent：先移除該專案既有的 repo link，再掛新的。

func (a *app) cmdRepo(args []string) int {
	if len(args) == 0 {
		return a.usageErr("用法：pb repo set <project-id> --url <url> [--path <p>]｜pb repo show <project-id>")
	}
	switch args[0] {
	case "set":
		return a.cmdRepoSet(args[1:])
	case "show", "get":
		return a.cmdRepoShow(args[1:])
	default:
		return a.usageErr("用法：pb repo set <project-id> --url <url> [--path <p>]｜pb repo show <project-id>")
	}
}

// repoOf：從節點的 links 取第一條 repo link（沒有回零值）。
func repoOf(links []domain.Link) (url, path string, ok bool) {
	for _, l := range links {
		if l.Kind == domain.LinkRepo {
			return l.Target, l.Note, true
		}
	}
	return "", "", false
}

func (a *app) cmdRepoSet(args []string) int {
	fs := a.newFlagSet("repo")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	url := fs.String("url", "", "repo URL（GitHub 或本地來源位置）")
	path := fs.String("path", "", "本地 git 路徑（選填）")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb repo set <project-id> --url <url> [--path <p>]")
	}
	if *url == "" {
		return a.usageErr("--url 是必填")
	}
	id := fs.Arg(0)
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		node, links, _, err := st.Get(ctx, id)
		if err != nil {
			return err
		}
		if node.Type != domain.TypeProject {
			return fmt.Errorf("repo set：%s 不是 project（type=%s）", id, node.Type)
		}
		for _, l := range links {
			if l.Kind == domain.LinkRepo {
				if err := st.Unlink(ctx, who, l.ID); err != nil {
					return err
				}
			}
		}
		if _, err := st.Link(ctx, who, id, domain.LinkRepo, *url, *path); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "已設定 %s 的 repo → %s", id, *url)
		if *path != "" {
			fmt.Fprintf(a.stdout, "（本地 %s）", *path)
		}
		fmt.Fprintln(a.stdout)
		return nil
	})
}

func (a *app) cmdRepoShow(args []string) int {
	fs := a.newFlagSet("repo")
	db := a.dbFlag(fs)
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb repo show <project-id> [--json]")
	}
	id := fs.Arg(0)
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		_, links, _, err := st.Get(ctx, id)
		if err != nil {
			return err
		}
		url, path, ok := repoOf(links)
		if *asJSON {
			return a.writeJSON(map[string]any{"project": id, "url": url, "path": path, "set": ok})
		}
		if !ok {
			fmt.Fprintf(a.stdout, "%s 尚未設定 repo\n", id)
			return nil
		}
		fmt.Fprintf(a.stdout, "%s repo: %s", id, url)
		if path != "" {
			fmt.Fprintf(a.stdout, "（本地 %s）", path)
		}
		fmt.Fprintln(a.stdout)
		return nil
	})
}
