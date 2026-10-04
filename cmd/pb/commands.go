package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// 狀態集（DATA_MODEL.md §6）；單一來源＝domain.AllStatuses，避免各處各抄一份而漂移。
var validStatuses = domain.StatusNames()

func isValidStatusName(s string) bool { return domain.Status(s).IsKnown() }

// parseSince：--if-unmodified-since 的 RFC3339 解析（DATA_MODEL.md §11.6）。
func parseSince(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("--if-unmodified-since 需為 RFC3339（例 2026-09-20T01:03:00+08:00）")
	}
	return &t, nil
}

// ---------- init / seed ----------

func (a *app) cmdInit(args []string) int {
	fs := a.newFlagSet("init")
	db := a.dbFlag(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	return a.withStoreCreate(*db, func(ctx context.Context, st *store.Store) error {
		v, err := st.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "已初始化 %s（schema v%d）\n", *db, v)
		return nil
	})
}

func (a *app) cmdSeed(args []string) int {
	fs := a.newFlagSet("seed")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		if err := st.Seed(ctx, who); err != nil {
			return err
		}
		n, _, _, err := st.Get(ctx, seedProjectID)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "種子專案 %s 就緒（%s %s, owner=%s）\n", n.ID, icon(n.Status), n.Status, n.Owner)
		return nil
	})
}

// ---------- 讀取類 ----------

func (a *app) cmdTree(args []string) int {
	fs := a.newFlagSet("tree")
	db := a.dbFlag(fs)
	project := fs.String("project", "", "只看某專案子樹")
	status := fs.String("status", "", "狀態過濾")
	owner := fs.String("owner", "", "owner 過濾")
	tag := fs.String("tag", "", "tags 含此字串")
	typ := fs.String("type", "", "type 過濾（project／req／issue／report／bug／plan）")
	depth := fs.Int("depth", 0, "深度上限（0＝不限）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if *status != "" && !isValidStatusName(*status) {
		return a.usageErr("未知狀態 %q（可用：%s）", *status, strings.Join(validStatuses, "／"))
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		nodes, err := st.Tree(ctx, store.TreeFilter{
			Project: *project, Status: *status, Owner: *owner, Tag: *tag,
			Type: domain.NodeType(*typ), Depth: *depth,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodesJSON(nodes))
		}
		a.printTree(nodes)
		return nil
	})
}

func (a *app) cmdGet(args []string) int {
	fs := a.newFlagSet("get")
	db := a.dbFlag(fs)
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb get <id>")
	}
	id := fs.Arg(0)
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		n, links, children, err := st.Get(ctx, id)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(nodeDetailJSON{
				nodeJSON: toNodeJSON(n),
				Links:    toLinksJSON(links),
				Children: toNodesJSON(children),
			})
		}
		a.printNode(n, links, children)
		return nil
	})
}

func (a *app) cmdSearch(args []string) int {
	fs := a.newFlagSet("search")
	db := a.dbFlag(fs)
	project := fs.String("project", "", "限定專案")
	tag := fs.String("tag", "", "標籤整段相符（逗號分隔欄位，非子字串）")
	owner := fs.String("owner", "", "owner 全等")
	status := fs.String("status", "", "狀態多選（逗號分隔，如 todo,in_progress,review,blocked）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() > 1 {
		return a.usageErr("用法：pb search [query] [--project X] [--tag t] [--owner o] [--status s1,s2]")
	}
	query := ""
	if fs.NArg() == 1 {
		query = fs.Arg(0)
	}
	if query == "" && *project == "" && *tag == "" && *owner == "" && *status == "" {
		return a.usageErr("query／--project／--tag／--owner／--status 至少要有一個")
	}
	statuses, err := domain.ParseStatusList(*status)
	if err != nil {
		return a.usageErr("%v", err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		nodes, err := st.SearchFiltered(ctx, store.SearchFilter{
			Query: query, Project: *project, Tag: *tag, Owner: *owner, Statuses: statuses,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodesJSON(nodes))
		}
		a.printNodeList(nodes)
		return nil
	})
}

func (a *app) cmdDeps(args []string) int {
	fs := a.newFlagSet("deps")
	db := a.dbFlag(fs)
	project := fs.String("project", "", "限定專案（預設全庫）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		return a.usageErr("用法：pb deps [--project X] [--json]")
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		links, err := st.ListDependsOn(ctx, *project)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toDepsJSON(links))
		}
		a.printDeps(links)
		return nil
	})
}

func (a *app) cmdHistory(args []string) int {
	fs := a.newFlagSet("history")
	db := a.dbFlag(fs)
	limit := fs.Int("limit", 20, "最多幾筆（預設 20）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb history <id> [--limit n]")
	}
	id := fs.Arg(0)
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		hs, err := st.History(ctx, id, *limit)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toHistoryJSON(hs))
		}
		a.printHistory(hs)
		return nil
	})
}

func (a *app) cmdStats(args []string) int {
	fs := a.newFlagSet("stats")
	db := a.dbFlag(fs)
	project := fs.String("project", "", "限定專案（預設全庫）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		stt, err := st.Stats(ctx, *project)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toStatsJSON(stt))
		}
		a.printStats(stt)
		return nil
	})
}

// ---------- 寫入類（一律要 actor）----------

func (a *app) cmdCreate(args []string) int {
	fs := a.newFlagSet("create")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	typ := fs.String("type", "", "節點型別（project／req／issue／report／bug／plan）")
	title := fs.String("title", "", "標題（必填）")
	parent := fs.String("parent", "", "父節點 id")
	id := fs.String("id", "", "指定 id（省略則自動生成，撞名即拒絕）")
	owner := fs.String("owner", "", "owner（預設 unassigned）")
	priority := fs.String("priority", "", "優先序（high／medium／low，預設 medium）")
	tags := fs.String("tags", "", "標籤（逗號分隔）")
	body := fs.String("body", "", "正文（markdown）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if *typ == "" {
		return a.usageErr("--type 是必填（project／req／issue／report／bug／plan）")
	}
	if *title == "" {
		return a.usageErr("--title 是必填")
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		n, err := st.Create(ctx, who, store.CreateInput{
			Type: domain.NodeType(*typ), Title: *title, ParentID: *parent, ID: *id,
			Owner: *owner, Priority: *priority, Tags: *tags, Body: *body,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodeJSON(n))
		}
		fmt.Fprintf(a.stdout, "已建立 %s（%s, %s %s, owner=%s）\n", n.ID, n.Type, icon(n.Status), n.Status, n.Owner)
		return nil
	})
}

func (a *app) cmdUpdate(args []string) int {
	fs := a.newFlagSet("update")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	title := fs.String("title", "", "標題")
	body := fs.String("body", "", "正文")
	owner := fs.String("owner", "", "owner")
	priority := fs.String("priority", "", "優先序")
	tags := fs.String("tags", "", "標籤")
	sortBy := fs.Int("sort", 0, "同層排序")
	since := fs.String("if-unmodified-since", "", "樂觀鎖：與現值不符即拒絕（RFC3339）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb update <id> [--title s] [--body s] [--owner o] [--priority p] [--tags s] [--sort n]")
	}
	id := fs.Arg(0)
	// 只有「使用者真的帶了」的旗標才算要改（flag.Visit 區分未帶與零值）。
	var in store.UpdateInput
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "title":
			v := *title
			in.Title = &v
		case "body":
			v := *body
			in.Body = &v
		case "owner":
			v := *owner
			in.Owner = &v
		case "priority":
			v := *priority
			in.Priority = &v
		case "tags":
			v := *tags
			in.Tags = &v
		case "sort":
			v := *sortBy
			in.Sort = &v
		}
	})
	if in.Title == nil && in.Body == nil && in.Owner == nil && in.Priority == nil && in.Tags == nil && in.Sort == nil {
		return a.usageErr("未指定任何要更新的欄位（--title／--body／--owner／--priority／--tags／--sort）")
	}
	expected, err := parseSince(*since)
	if err != nil {
		return a.usageErr("%s", err)
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		n, err := st.Update(ctx, who, id, in, expected)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodeJSON(n))
		}
		fmt.Fprintf(a.stdout, "已更新 %s（updated_at=%s）\n", n.ID, fmtTime(n.UpdatedAt))
		return nil
	})
}

func (a *app) cmdMove(args []string) int {
	fs := a.newFlagSet("move")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	note := fs.String("note", "", "說明（轉 blocked 時為理由／reopen 時必填）")
	since := fs.String("if-unmodified-since", "", "樂觀鎖：與現值不符即拒絕（RFC3339）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 2 {
		return a.usageErr("用法：pb move <id> <status> [--note s] [--if-unmodified-since <ts>]")
	}
	id, to := fs.Arg(0), fs.Arg(1)
	if !isValidStatusName(to) {
		return a.usageErr("未知狀態 %q（可用：%s）", to, strings.Join(validStatuses, "／"))
	}
	expected, err := parseSince(*since)
	if err != nil {
		return a.usageErr("%s", err)
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		n, err := st.Transition(ctx, who, id, domain.Status(to), *note, expected)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodeJSON(n))
		}
		fmt.Fprintf(a.stdout, "已流轉 %s → %s %s（updated_at=%s）\n", n.ID, icon(n.Status), n.Status, fmtTime(n.UpdatedAt))
		return nil
	})
}

// cmdReparent：把節點（連全部子孫）搬到另一個父節點底下（REQ-MOVE-NODE）。
// 註：CLI 的 `move` 已被「狀態流轉」佔用（INTERFACE.md §1），搬單用 `reparent`。
func (a *app) cmdReparent(args []string) int {
	fs := a.newFlagSet("reparent")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	parent := fs.String("parent", "", "新的父節點 id（必填；同專案或別的專案）")
	note := fs.String("note", "", "說明（跨專案搬移時必填：理由）")
	since := fs.String("if-unmodified-since", "", "樂觀鎖：與現值不符即拒絕（RFC3339）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb reparent <id> --parent <new-parent> [--note s] [--if-unmodified-since <ts>]")
	}
	if *parent == "" {
		return a.usageErr("--parent 是必填（新的父節點 id）")
	}
	id := fs.Arg(0)
	expected, err := parseSince(*since)
	if err != nil {
		return a.usageErr("%s", err)
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		res, err := st.MoveNode(ctx, who, id, *parent, *note, expected)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toMoveResultJSON(res))
		}
		fmt.Fprintf(a.stdout, "已搬移 %s → %s（含子孫共 %d 個節點，updated_at=%s）\n",
			res.OldID, res.NewID, len(res.Moved), fmtTime(res.Node.UpdatedAt))
		return nil
	})
}

func (a *app) cmdAssign(args []string) int {
	fs := a.newFlagSet("assign")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 2 {
		return a.usageErr("用法：pb assign <id> <owner>")
	}
	id, owner := fs.Arg(0), fs.Arg(1)
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		n, err := st.Assign(ctx, who, id, owner)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodeJSON(n))
		}
		fmt.Fprintf(a.stdout, "已指派 %s → owner=%s\n", n.ID, n.Owner)
		return nil
	})
}

func (a *app) cmdLink(args []string) int {
	fs := a.newFlagSet("link")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	kind := fs.String("kind", "", "關聯種類（depends_on／file／commit／pr／url／doc／repo）")
	target := fs.String("target", "", "目標（depends_on 為節點 id；其餘為字串）")
	note := fs.String("note", "", "說明")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb link <id> --kind <k> --target <s> [--note s]")
	}
	if *kind == "" {
		return a.usageErr("--kind 是必填（depends_on／file／commit／pr／url／doc／repo）")
	}
	if *target == "" {
		return a.usageErr("--target 是必填")
	}
	id := fs.Arg(0)
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		l, err := st.Link(ctx, who, id, domain.LinkKind(*kind), *target, *note)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toLinkJSON(l))
		}
		fmt.Fprintf(a.stdout, "已關聯 %s #%d %s → %s\n", l.FromID, l.ID, l.Kind, l.Target)
		return nil
	})
}

func (a *app) cmdVerify(args []string) int {
	fs := a.newFlagSet("verify")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	note := fs.String("note", "", "驗收證據（必填：覆蓋率／測試結果／file:line）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb verify <id> --note <evidence>")
	}
	if *note == "" {
		return a.usageErr("--note（驗收證據）是必填")
	}
	id := fs.Arg(0)
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		n, err := st.Verify(ctx, who, id, *note)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toNodeJSON(n))
		}
		fmt.Fprintf(a.stdout, "已驗收 %s → %s %s（owner=%s）\n", n.ID, icon(n.Status), n.Status, n.Owner)
		return nil
	})
}

func (a *app) cmdComment(args []string) int {
	fs := a.newFlagSet("comment")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 2 {
		return a.usageErr("用法：pb comment <id> <text>")
	}
	id, text := fs.Arg(0), fs.Arg(1)
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		if err := st.Comment(ctx, who, id, text); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "已留言於 %s（actor=%s）\n", id, who)
		return nil
	})
}
