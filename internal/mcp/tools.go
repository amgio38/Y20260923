package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// ---------------------------------------------------------------------------
// 參數 helpers：缺必要參數／型別不對一律回 error（上層轉 isError:true，
// INTERFACE.md §2「不靜默修正」）。
// ---------------------------------------------------------------------------

func reqStr(args map[string]any, name string) (string, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required param %q", name)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("missing required param %q", name)
	}
	return strings.TrimSpace(s), nil
}

// reqText：內文類（comment text／verify note 前置檢查）——不 trim，但拒空字串。
func reqText(args map[string]any, name string) (string, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required param %q", name)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("missing required param %q", name)
	}
	return s, nil
}

func optStr(args map[string]any, name string) (string, bool) {
	v, ok := args[name]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// optInt：JSON number（float64）或數字字串；缺席回 def。
func optInt(args map[string]any, name string, def int) (int, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return def, nil
	}
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("invalid param %q: %v", name, v)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("invalid param %q: must be a number", name)
	}
}

func reqInt64(args map[string]any, name string) (int64, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required param %q", name)
	}
	switch n := v.(type) {
	case float64:
		return int64(n), nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid param %q: %v", name, v)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("invalid param %q: must be a number", name)
	}
}

// optTime：expected_updated_at（RFC3339 字串）；缺席回 nil（不做樂觀鎖，§11.6）。
// 帶了但 parse 不過→回錯（不靜默忽略，否則呼叫端以為有鎖其實沒有）。
func optTime(args map[string]any, name string) (*time.Time, error) {
	s, ok := optStr(args, name)
	if !ok || strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid param %q (want RFC3339): %v", name, s)
	}
	return &t, nil
}

// ---------------------------------------------------------------------------
// 20 個 pb_* tools（v0.1 的 14 個照 INTERFACE.md §2 表格順序；v0.2 加 pb_deps
// 緊跟 pb_search；v0.3 加 pb_hook／pb_unhook／pb_hooks 收尾；
// v0.4 加 pb_commit_attach／pb_set_repo；tools/list 照此原樣輸出）。
// ---------------------------------------------------------------------------

type toolHandler func(ctx context.Context, st *store.Store, args map[string]any) (any, error)

type toolDef struct {
	Name   string
	Desc   string
	Schema map[string]any
	Handle toolHandler
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

func schema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

var tools = []toolDef{
	{
		Name: "pb_tree",
		Desc: "看樹：節點精簡陣列（id/type/title/status/owner/children_count）。唯讀。",
		Schema: schema(map[string]any{
			"project": strProp("project id，不過濾可省略"),
			"status":  strProp("狀態過濾"),
			"owner":   strProp("owner 過濾"),
			"tag":     strProp("tags 含此字串（子字串，跟 CLI tree --tag 一樣）"),
			"type":    strProp("project/req/issue/report/bug/plan 過濾"),
			"depth":   numProp("深度限制，0＝不限"),
		}),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			f := store.TreeFilter{}
			if v, ok := optStr(args, "project"); ok {
				f.Project = v
			}
			if v, ok := optStr(args, "status"); ok {
				f.Status = v
			}
			if v, ok := optStr(args, "owner"); ok {
				f.Owner = v
			}
			if v, ok := optStr(args, "tag"); ok {
				f.Tag = v
			}
			if v, ok := optStr(args, "type"); ok {
				f.Type = domain.NodeType(v)
			}
			depth, err := optInt(args, "depth", 0)
			if err != nil {
				return nil, err
			}
			f.Depth = depth
			nodes, err := st.Tree(ctx, f)
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(nodes))
			for _, n := range nodes {
				// children_count 取實際子節點數（逐一 Get；v0.1 資料量小，正確優先）。
				_, _, children, err := st.Get(ctx, n.ID)
				if err != nil {
					return nil, err
				}
				out = append(out, briefJSON(n, len(children)))
			}
			return out, nil
		},
	},
	{
		Name: "pb_get",
		Desc: "取單一節點全文＋links＋children。唯讀。",
		Schema: schema(map[string]any{
			"id": strProp("節點 id"),
		}, "id"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			node, links, children, err := st.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			return getJSON(node, links, children), nil
		},
	},
	{
		Name: "pb_create",
		Desc: "開單。actor 必要；id 省略時依 DATA_MODEL.md §7／§11.5 自動生成（撞名直接回錯，不加尾碼）。",
		Schema: schema(map[string]any{
			"actor":     strProp("寫入者，須在 owner 名冊內"),
			"type":      strProp("project/req/issue/report/bug/plan"),
			"title":     strProp("標題"),
			"parent_id": strProp("父節點 id（project 不用）"),
			"id":        strProp("指定 id，省略自動生成"),
			"owner":     strProp("負責人，省略＝unassigned"),
			"priority":  strProp("high/medium/low，省略＝medium"),
			"tags":      strProp("逗號分隔"),
			"body":      strProp("markdown 正文"),
		}, "actor", "type", "title"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			typ, err := reqStr(args, "type")
			if err != nil {
				return nil, err
			}
			title, err := reqText(args, "title")
			if err != nil {
				return nil, err
			}
			in := store.CreateInput{Type: domain.NodeType(typ), Title: title}
			if v, ok := optStr(args, "parent_id"); ok {
				in.ParentID = v
			}
			if v, ok := optStr(args, "id"); ok {
				in.ID = v
			}
			if v, ok := optStr(args, "owner"); ok {
				in.Owner = v
			}
			if v, ok := optStr(args, "priority"); ok {
				in.Priority = v
			}
			if v, ok := optStr(args, "tags"); ok {
				in.Tags = v
			}
			if v, ok := optStr(args, "body"); ok {
				in.Body = v
			}
			node, err := st.Create(ctx, actor, in)
			if err != nil {
				return nil, err
			}
			return nodeJSON(node), nil
		},
	},
	{
		Name: "pb_update",
		Desc: "部分更新。actor 必要；expected_updated_at 可選（帶了就做樂觀鎖，§11.6）。",
		Schema: schema(map[string]any{
			"actor":               strProp("寫入者，須在 owner 名冊內"),
			"id":                  strProp("節點 id"),
			"title":               strProp("新標題"),
			"body":                strProp("新正文"),
			"owner":               strProp("新負責人"),
			"priority":            strProp("high/medium/low"),
			"tags":                strProp("逗號分隔"),
			"sort":                numProp("同層排序"),
			"expected_updated_at": strProp("樂觀鎖（RFC3339），不符回 conflict"),
		}, "actor", "id"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			var in store.UpdateInput
			if v, ok := optStr(args, "title"); ok {
				in.Title = &v
			}
			if v, ok := optStr(args, "body"); ok {
				in.Body = &v
			}
			if v, ok := optStr(args, "owner"); ok {
				in.Owner = &v
			}
			if v, ok := optStr(args, "priority"); ok {
				in.Priority = &v
			}
			if v, ok := optStr(args, "tags"); ok {
				in.Tags = &v
			}
			if n, ok := args["sort"]; ok && n != nil {
				i, err := optInt(args, "sort", 0)
				if err != nil {
					return nil, err
				}
				in.Sort = &i
			}
			exp, err := optTime(args, "expected_updated_at")
			if err != nil {
				return nil, err
			}
			node, err := st.Update(ctx, actor, id, in, exp)
			if err != nil {
				return nil, err
			}
			return nodeJSON(node), nil
		},
	},
	{
		// 為什麼改：tools/list 是呼叫端唯一會讀的說明；沒寫允許的邊，模型會先送 to=done。
		// 改了什麼：描述與 to 參數補上允許邊、blocked／reopen 的 note、以及收單要走 pb_verify。不改狀態機。
		Name: "pb_transition",
		Desc: "改狀態，不是驗收。actor 必填。to 只能走參數說明裡的允許邊；不在邊上的組合回 isError（illegal transition），不會靜默改狀態，也不會自動改去別的狀態。from 與 to 相同是 no-op，不寫 history。轉 blocked：note 非空，或該節點已有 kind=depends_on 的 link，否則 missing block reason。done→in_progress 是 reopen，note 必填。review→done 這支技術上收，但不檢查證據，history 記的是 transition 不是 verify；收單請改叫 pb_verify。",
		Schema: schema(map[string]any{
			"actor":               strProp("寫入者，須在 owner 名冊內：xiaoxia、kaimake、kaimadi、yilong、claude、human、unassigned"),
			"id":                  strProp("節點 id，例如 Y20260920/REQ-MCP-TOOL-DESC/ISSUE-TRANSITION-VERIFY-DESC"),
			"to":                  strProp("目標狀態，只能是 todo、in_progress、review、blocked、hold、done、cancel 其中一個。允許的邊：todo→in_progress|blocked|hold|cancel；in_progress→review|blocked|hold|cancel；review→done|in_progress|blocked|cancel；blocked→in_progress|hold|cancel；hold→todo|in_progress|cancel；done→in_progress；cancel→todo。todo、in_progress、blocked、hold、cancel 直接 to=done 會被拒。收單不要用 to=done，改叫 pb_verify。"),
			"note":                strProp("說明。轉 blocked 時必填（或該節點已有 depends_on）；done→in_progress 重開時必填；其他轉移可省略"),
			"expected_updated_at": strProp("樂觀鎖，值為節點目前的 updated_at（RFC3339）。帶了且不符回 conflict，不寫入"),
		}, "actor", "id", "to"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			to, err := reqStr(args, "to")
			if err != nil {
				return nil, err
			}
			note, _ := optStr(args, "note")
			exp, err := optTime(args, "expected_updated_at")
			if err != nil {
				return nil, err
			}
			node, err := st.Transition(ctx, actor, id, domain.Status(to), note, exp)
			if err != nil {
				return nil, err
			}
			return nodeJSON(node), nil
		},
	},
	{
		Name: "pb_assign",
		Desc: "派單（改 owner）。actor 必要。",
		Schema: schema(map[string]any{
			"actor": strProp("寫入者，須在 owner 名冊內"),
			"id":    strProp("節點 id"),
			"owner": strProp("新負責人，須在 owner 名冊內"),
		}, "actor", "id", "owner"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			owner, err := reqStr(args, "owner")
			if err != nil {
				return nil, err
			}
			node, err := st.Assign(ctx, actor, id, owner)
			if err != nil {
				return nil, err
			}
			return nodeJSON(node), nil
		},
	},
	{
		Name: "pb_link",
		Desc: "掛關聯。actor 必要；kind=depends_on 會驗證 target 節點存在（§11.4）。",
		Schema: schema(map[string]any{
			"actor":   strProp("寫入者，須在 owner 名冊內"),
			"from_id": strProp("來源節點 id"),
			"kind":    strProp("depends_on/file/commit/pr/url/doc/repo"),
			"target":  strProp("depends_on→節點 id；其餘→字串"),
			"note":    strProp("說明"),
		}, "actor", "from_id", "kind", "target"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			fromID, err := reqStr(args, "from_id")
			if err != nil {
				return nil, err
			}
			kind, err := reqStr(args, "kind")
			if err != nil {
				return nil, err
			}
			target, err := reqText(args, "target")
			if err != nil {
				return nil, err
			}
			note, _ := optStr(args, "note")
			link, err := st.Link(ctx, actor, fromID, domain.LinkKind(kind), target, note)
			if err != nil {
				return nil, err
			}
			return linkJSON(link), nil
		},
	},
	{
		Name: "pb_unlink",
		Desc: "拆關聯。actor 必要。",
		Schema: schema(map[string]any{
			"actor":   strProp("寫入者，須在 owner 名冊內"),
			"link_id": numProp("link id"),
		}, "actor", "link_id"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			linkID, err := reqInt64(args, "link_id")
			if err != nil {
				return nil, err
			}
			if err := st.Unlink(ctx, actor, linkID); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		},
	},
	{
		Name: "pb_verify",
		Desc: "正規收單，把 review 收成 done。呼叫前 status 必須已經是 review，否則 isError（不會幫你先轉 review，也不接受從 todo 或 in_progress 直接收）。note 必填，當作證據寫進 history，action=verify。成功後 status=done。actor 與節點 owner 相同時，note 前面自動加 [self-verified]，不阻擋。不要用 pb_transition 的 to=done 代替這支：那支不要求證據，history 也不是 verify。",
		Schema: schema(map[string]any{
			"actor": strProp("驗收者，須在 owner 名冊內：xiaoxia、kaimake、kaimadi、yilong、claude、human、unassigned"),
			"id":    strProp("要收的節點 id。該節點目前 status 必須是 review"),
			"note":  strProp("驗收證據，必填。寫測試指令與結果、覆蓋率、commit，或 file:line。空字串會被拒"),
		}, "actor", "id", "note"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			note, err := reqText(args, "note")
			if err != nil {
				return nil, err
			}
			node, err := st.Verify(ctx, actor, id, note)
			if err != nil {
				return nil, err
			}
			return nodeJSON(node), nil
		},
	},
	{
		Name: "pb_comment",
		Desc: "留言（只寫 history，不動節點）。actor、text 必要。",
		Schema: schema(map[string]any{
			"actor": strProp("寫入者，須在 owner 名冊內"),
			"id":    strProp("節點 id"),
			"text":  strProp("留言內容"),
		}, "actor", "id", "text"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			text, err := reqText(args, "text")
			if err != nil {
				return nil, err
			}
			if err := st.Comment(ctx, actor, id, text); err != nil {
				return nil, err
			}
			entries, err := st.History(ctx, id, 1)
			if err != nil {
				return nil, err
			}
			if len(entries) == 0 {
				return map[string]any{"ok": true}, nil
			}
			return historyJSON(entries[0]), nil
		},
	},
	{
		Name: "pb_search",
		Desc: "全文搜尋（FTS5 trigram：title／body／tags 子字串，中文子字串可查；3 字元以下退回 LIKE）。唯讀。query 可省略，但 query／project／tag／owner 至少一個。tag 是逗號分隔欄位的整段相符（非子字串），owner 全等。回精簡陣列（id／type／status／owner／title，不含 body）。",
		Schema: schema(map[string]any{
			"query":   strProp("關鍵字，可省略"),
			"project": strProp("限 project id，不限可省略"),
			"tag":     strProp("標籤整段相符（逗號分隔欄位，非子字串）"),
			"owner":   strProp("owner 全等"),
		}),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			query, _ := optStr(args, "query")
			query = strings.TrimSpace(query)
			project, _ := optStr(args, "project")
			project = strings.TrimSpace(project)
			tag, _ := optStr(args, "tag")
			tag = strings.TrimSpace(tag)
			owner, _ := optStr(args, "owner")
			owner = strings.TrimSpace(owner)
			if query == "" && project == "" && tag == "" && owner == "" {
				return nil, fmt.Errorf("query／project／tag／owner 至少要有一個")
			}
			nodes, err := st.SearchAdvanced(ctx, query, project, tag, owner)
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(nodes))
			for _, n := range nodes {
				out = append(out, searchBriefJSON(n))
			}
			return out, nil
		},
	},
	{
		Name: "pb_deps",
		Desc: "依賴清單：kind=depends_on 的關聯（from → target，沿用 link 的 from_id／target／note）。唯讀；project 可省略（空＝全庫），非空只回 from_id 屬於該專案子樹的關聯。",
		Schema: schema(map[string]any{
			"project": strProp("project id，不過濾可省略"),
		}),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			project, _ := optStr(args, "project")
			links, err := st.ListDependsOn(ctx, strings.TrimSpace(project))
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(links))
			for _, l := range links {
				out = append(out, linkJSON(l))
			}
			return out, nil
		},
	},
	{
		Name: "pb_history",
		Desc: "取節點事件（新→舊）。唯讀；limit 省略＝不限。",
		Schema: schema(map[string]any{
			"id":    strProp("節點 id"),
			"limit": numProp("筆數，省略＝不限"),
		}, "id"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			limit, err := optInt(args, "limit", 0)
			if err != nil {
				return nil, err
			}
			entries, err := st.History(ctx, id, limit)
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(entries))
			for _, e := range entries {
				out = append(out, historyJSON(e))
			}
			return out, nil
		},
	},
	{
		Name: "pb_stats",
		Desc: "統計：各狀態計數／每人手上（未結案）張數／每 REQ 完成度／自我驗收張數（§11.2）。唯讀。",
		Schema: schema(map[string]any{
			"project": strProp("project id，省略＝全庫"),
		}),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			project, _ := optStr(args, "project")
			stats, err := st.Stats(ctx, project)
			if err != nil {
				return nil, err
			}
			return statsJSON(stats), nil
		},
	},
	{
		Name: "pb_delete",
		Desc: "刪節點：僅 report 或無子、無 link 的節點可刪，否則回錯。actor 必要。",
		Schema: schema(map[string]any{
			"actor": strProp("寫入者，須在 owner 名冊內"),
			"id":    strProp("節點 id"),
		}, "actor", "id"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			id, err := reqStr(args, "id")
			if err != nil {
				return nil, err
			}
			if err := st.Delete(ctx, actor, id); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		},
	},
	{
		Name: "pb_hook",
		Desc: "訂閱節點狀態變動：node_id 或其子孫異動時喚醒 harness 的 target。只寫訂閱，不喚醒（喚醒是 serve 的事，不准經 shell／exec herdr）。重複訂閱＝更新訂閱者。harness 這輪只收 herdr。",
		Schema: schema(map[string]any{
			"actor":   strProp("訂閱者，須在 owner 名冊內"),
			"node_id": strProp("訂閱的節點 id（等於異動節點或是它的祖先）"),
			"harness": strProp("這輪只收 herdr"),
			"target":  strProp("herdr 的 agent 名，須符合 ^[a-z][a-z0-9_-]{0,31}$"),
		}, "actor", "node_id", "harness", "target"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			nodeID, err := reqStr(args, "node_id")
			if err != nil {
				return nil, err
			}
			harness, err := reqStr(args, "harness")
			if err != nil {
				return nil, err
			}
			target, err := reqStr(args, "target")
			if err != nil {
				return nil, err
			}
			h, err := st.Hook(ctx, store.HookInput{
				Actor: actor, NodeID: nodeID, Harness: harness, Target: target,
			})
			if err != nil {
				return nil, err
			}
			return hookJSON(h), nil
		},
	},
	{
		Name: "pb_unhook",
		Desc: "取消訂閱。不存在回錯（不靜默成功）。只刪訂閱，不碰喚醒。",
		Schema: schema(map[string]any{
			"actor":   strProp("寫入者，須在 owner 名冊內"),
			"node_id": strProp("訂閱的節點 id"),
			"harness": strProp("這輪只收 herdr"),
			"target":  strProp("herdr 的 agent 名"),
		}, "actor", "node_id", "harness", "target"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			nodeID, err := reqStr(args, "node_id")
			if err != nil {
				return nil, err
			}
			harness, err := reqStr(args, "harness")
			if err != nil {
				return nil, err
			}
			target, err := reqStr(args, "target")
			if err != nil {
				return nil, err
			}
			if err := st.Unhook(ctx, actor, nodeID, harness, target); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		},
	},
	{
		Name: "pb_hooks",
		Desc: "列出訂閱（node_id、harness、target 排序）。唯讀；node_id 省略＝全部。",
		Schema: schema(map[string]any{
			"node_id": strProp("只列此節點的訂閱，省略＝全部"),
		}),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			nodeID, _ := optStr(args, "node_id")
			hooks, err := st.Hooks(ctx, strings.TrimSpace(nodeID))
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(hooks))
			for _, h := range hooks {
				out = append(out, hookJSON(h))
			}
			return out, nil
		},
	},
	{
		Name: "pb_commit_attach",
		Desc: "從 commit 訊息自動把 sha 掛到訊息中的單（link kind=commit）。sha／message 必填（呼叫端從 git 取得：sha=git rev-parse HEAD、message=git log -1 --format=%B）。訊息中的節點 id（Y<8碼>/…）存在才掛；沒單號或查無此單落在 skipped。只掛 link，不改狀態、不自動 move／done。",
		Schema: schema(map[string]any{
			"actor":   strProp("寫入者，須在 owner 名冊內"),
			"sha":     strProp("commit sha（完整或短）"),
			"message": strProp("commit 訊息（含單號，如 #Y20260920/REQ-…/ISSUE-…）"),
			"note":    strProp("說明；省略＝訊息第一行"),
		}, "actor", "sha", "message"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			sha, err := reqStr(args, "sha")
			if err != nil {
				return nil, err
			}
			msg, err := reqStr(args, "message")
			if err != nil {
				return nil, err
			}
			note, _ := optStr(args, "note")
			if note == "" {
				note = firstLineOf(msg)
			}
			ids := domain.ExtractNodeIDs(msg)
			linked := make([]string, 0, len(ids))
			skipped := make([]string, 0)
			for _, id := range ids {
				if _, _, _, err := st.Get(ctx, id); err != nil {
					skipped = append(skipped, id)
					continue
				}
				if _, err := st.Link(ctx, actor, id, domain.LinkCommit, sha, note); err != nil {
					return nil, err
				}
				linked = append(linked, id)
			}
			return map[string]any{"sha": sha, "linked": linked, "skipped": skipped}, nil
		},
	},
	{
		Name: "pb_set_repo",
		Desc: "設定 project 的 repo（link kind=repo）。actor／project_id／url 必填，path 選填。idempotent：先移除該專案既有的 repo link 再掛。只掛 link，不改狀態。",
		Schema: schema(map[string]any{
			"actor":      strProp("寫入者，須在 owner 名冊內"),
			"project_id": strProp("project 節點 id"),
			"url":        strProp("repo URL（GitHub 或本地來源）"),
			"path":       strProp("本地 git 路徑（選填）"),
		}, "actor", "project_id", "url"),
		Handle: func(ctx context.Context, st *store.Store, args map[string]any) (any, error) {
			actor, err := reqStr(args, "actor")
			if err != nil {
				return nil, err
			}
			pid, err := reqStr(args, "project_id")
			if err != nil {
				return nil, err
			}
			url, err := reqStr(args, "url")
			if err != nil {
				return nil, err
			}
			path, _ := optStr(args, "path")
			node, links, _, err := st.Get(ctx, pid)
			if err != nil {
				return nil, err
			}
			if node.Type != domain.TypeProject {
				return nil, fmt.Errorf("repo set：%s 不是 project（type=%s）", pid, node.Type)
			}
			for _, l := range links {
				if l.Kind == domain.LinkRepo {
					if err := st.Unlink(ctx, actor, l.ID); err != nil {
						return nil, err
					}
				}
			}
			if _, err := st.Link(ctx, actor, pid, domain.LinkRepo, url, path); err != nil {
				return nil, err
			}
			return map[string]any{"project": pid, "url": url, "path": path}, nil
		},
	},
}

// firstLineOf：取訊息第一行（給 pb_commit_attach 當預設 note）。
func firstLineOf(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// hookJSON：pb_hook／pb_hooks 回的訂閱形狀（沿 store.Hook 欄位）。
func hookJSON(h store.Hook) map[string]any {
	return map[string]any{
		"id": h.ID, "node_id": h.NodeID, "actor": h.Actor,
		"harness": h.Harness, "target": h.Target, "created_at": fmtT(h.CreatedAt),
	}
}

// searchBriefJSON：pb_search 回的精簡形狀（id／type／status／owner／title，
// 一龍裁示不含 body；跟 briefJSON 不同，不帶 children_count，免逐一 Get）。
func searchBriefJSON(n domain.Node) map[string]any {
	return map[string]any{
		"id": n.ID, "type": string(n.Type), "title": n.Title,
		"status": string(n.Status), "owner": n.Owner,
	}
}

func findTool(name string) *toolDef {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func toolSchemas() []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name": t.Name, "description": t.Desc, "inputSchema": t.Schema,
		})
	}
	return out
}
