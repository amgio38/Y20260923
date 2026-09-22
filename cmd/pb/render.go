package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// statusIcon：DATA_MODEL.md §6 的狀態顯示。
var statusIcon = map[domain.Status]string{
	domain.StatusTodo:       "⬜",
	domain.StatusInProgress: "🔶",
	domain.StatusReview:     "👀",
	domain.StatusBlocked:    "🚧",
	domain.StatusHold:       "⏸",
	domain.StatusDone:       "✅",
	domain.StatusCancel:     "❌",
}

func icon(s domain.Status) string {
	if v, ok := statusIcon[s]; ok {
		return v
	}
	return "❔"
}

// fmtTime：ISO8601（台北，同 DATA_MODEL.md §5 的存法）。
func fmtTime(t time.Time) string { return t.Format(time.RFC3339) }

// ---------- JSON（欄位命名對齊 docs/INTERFACE.md §3）----------

type nodeJSON struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	ParentID  string `json:"parent_id,omitempty"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Owner     string `json:"owner"`
	Priority  string `json:"priority"`
	Tags      string `json:"tags,omitempty"`
	Sort      int    `json:"sort,omitempty"`
	Body      string `json:"body,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toNodeJSON(n domain.Node) nodeJSON {
	return nodeJSON{
		ID: n.ID, Type: string(n.Type), ParentID: n.ParentID, Title: n.Title,
		Status: string(n.Status), Owner: n.Owner, Priority: string(n.Priority),
		Tags: n.Tags, Sort: n.Sort, Body: n.Body,
		CreatedAt: fmtTime(n.CreatedAt), UpdatedAt: fmtTime(n.UpdatedAt),
	}
}

func toNodesJSON(ns []domain.Node) []nodeJSON {
	out := make([]nodeJSON, 0, len(ns))
	for _, n := range ns {
		out = append(out, toNodeJSON(n))
	}
	return out
}

type linkJSON struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toLinkJSON(l domain.Link) linkJSON {
	return linkJSON{ID: l.ID, Kind: string(l.Kind), Target: l.Target, Note: l.Note, CreatedAt: fmtTime(l.CreatedAt)}
}

func toLinksJSON(ls []domain.Link) []linkJSON {
	out := make([]linkJSON, 0, len(ls))
	for _, l := range ls {
		out = append(out, toLinkJSON(l))
	}
	return out
}

type nodeDetailJSON struct {
	nodeJSON
	Links    []linkJSON `json:"links"`
	Children []nodeJSON `json:"children"`
}

// depJSON：`pb deps` 的輸出一列（Y20260920/REQ-V02-DEPS：沿用 domain.Link 的 FromID／Target／Note）。
type depJSON struct {
	FromID string `json:"from_id"`
	Target string `json:"target"`
	Note   string `json:"note,omitempty"`
}

func toDepsJSON(ls []domain.Link) []depJSON {
	out := make([]depJSON, 0, len(ls))
	for _, l := range ls {
		out = append(out, depJSON{FromID: l.FromID, Target: l.Target, Note: l.Note})
	}
	return out
}

type historyJSON struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
	TS     string `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Field  string `json:"field,omitempty"`
	From   string `json:"from_val,omitempty"`
	To     string `json:"to_val,omitempty"`
	Note   string `json:"note,omitempty"`
}

func toHistoryJSON(hs []domain.HistoryEntry) []historyJSON {
	out := make([]historyJSON, 0, len(hs))
	for _, h := range hs {
		out = append(out, historyJSON{
			ID: h.ID, NodeID: h.NodeID, TS: fmtTime(h.TS), Actor: h.Actor,
			Action: string(h.Action), Field: h.Field, From: h.FromVal, To: h.ToVal, Note: h.Note,
		})
	}
	return out
}

type statsJSON struct {
	CountByStatus     map[string]int     `json:"count_by_status"`
	CountByOwner      map[string]int     `json:"count_by_owner"`
	AvgDwellDays      map[string]float64 `json:"avg_dwell_days"`
	ReqProgress       map[string]float64 `json:"req_progress"`
	SelfVerifiedCount int                `json:"self_verified_count"`
}

func toStatsJSON(st store.Stats) statsJSON {
	byStatus := make(map[string]int, len(st.CountByStatus))
	for k, v := range st.CountByStatus {
		byStatus[string(k)] = v
	}
	byOwner := st.CountByOwner
	if byOwner == nil {
		byOwner = map[string]int{}
	}
	progress := st.ReqProgress
	if progress == nil {
		progress = map[string]float64{}
	}
	dwell := st.AvgDwellDays
	if dwell == nil {
		dwell = map[string]float64{}
	}
	return statsJSON{CountByStatus: byStatus, CountByOwner: byOwner, AvgDwellDays: dwell, ReqProgress: progress, SelfVerifiedCount: st.SelfVerifiedCount}
}

type hookJSON struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	Actor     string `json:"actor"`
	Harness   string `json:"harness"`
	Target    string `json:"target"`
	CreatedAt string `json:"created_at"`
}

func toHookJSON(h store.Hook) hookJSON {
	return hookJSON{
		ID: h.ID, NodeID: h.NodeID, Actor: h.Actor,
		Harness: h.Harness, Target: h.Target, CreatedAt: fmtTime(h.CreatedAt),
	}
}

func toHooksJSON(hs []store.Hook) []hookJSON {
	out := make([]hookJSON, 0, len(hs))
	for _, h := range hs {
		out = append(out, toHookJSON(h))
	}
	return out
}

// printHooks：訂閱清單（節點、harness → target、訂閱者）。
func (a *app) printHooks(hs []store.Hook) {
	if len(hs) == 0 {
		fmt.Fprintln(a.stdout, "（沒有訂閱）")
		return
	}
	for _, h := range hs {
		fmt.Fprintf(a.stdout, "  %-48s %s → %-12s actor=%s\n", h.NodeID, h.Harness, h.Target, h.Actor)
	}
}

// writeJSON：縮排 JSON（讀取類子命令的 --json）。
func (a *app) writeJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---------- 文字 ----------

// treeDepth：在本回應的節點集合內算深度（父不在集合裡就從該節點起算）。
func treeDepth(nodes []domain.Node, n domain.Node) int {
	byID := make(map[string]domain.Node, len(nodes))
	for _, x := range nodes {
		byID[x.ID] = x
	}
	depth, cur := 0, n
	for i := 0; i <= len(nodes) && cur.ParentID != ""; i++ {
		parent, ok := byID[cur.ParentID]
		if !ok {
			break
		}
		cur = parent
		depth++
	}
	return depth
}

// printTree：縮排樹（INTERFACE.md §1：「樹用縮排」）。
func (a *app) printTree(nodes []domain.Node) {
	if len(nodes) == 0 {
		fmt.Fprintln(a.stdout, "（沒有符合的節點）")
		return
	}
	for _, n := range nodes {
		d := treeDepth(nodes, n)
		branch := ""
		if d > 0 {
			branch = strings.Repeat("   ", d-1) + "└─ "
		}
		fmt.Fprintf(a.stdout, "%s%s  %s %s  %s  %s\n", branch, n.ID, icon(n.Status), n.Status, n.Owner, n.Title)
	}
}

func (a *app) printNode(n domain.Node, links []domain.Link, children []domain.Node) {
	fmt.Fprintf(a.stdout, "ID       : %s\n", n.ID)
	fmt.Fprintf(a.stdout, "Type     : %s\n", n.Type)
	if n.ParentID != "" {
		fmt.Fprintf(a.stdout, "Parent   : %s\n", n.ParentID)
	}
	fmt.Fprintf(a.stdout, "Title    : %s\n", n.Title)
	fmt.Fprintf(a.stdout, "Status   : %s %s\n", icon(n.Status), n.Status)
	fmt.Fprintf(a.stdout, "Owner    : %s\n", n.Owner)
	fmt.Fprintf(a.stdout, "Priority : %s\n", n.Priority)
	if n.Tags != "" {
		fmt.Fprintf(a.stdout, "Tags     : %s\n", n.Tags)
	}
	fmt.Fprintf(a.stdout, "Created  : %s\n", fmtTime(n.CreatedAt))
	fmt.Fprintf(a.stdout, "Updated  : %s\n", fmtTime(n.UpdatedAt))
	fmt.Fprintf(a.stdout, "關聯 (%d)：\n", len(links))
	for _, l := range links {
		fmt.Fprintf(a.stdout, "  #%d %s → %s %s\n", l.ID, l.Kind, l.Target, l.Note)
	}
	fmt.Fprintf(a.stdout, "子節點 (%d)：\n", len(children))
	for _, c := range children {
		fmt.Fprintf(a.stdout, "  %s  %s %s  %s\n", c.ID, icon(c.Status), c.Status, c.Owner)
	}
	if n.Body != "" {
		fmt.Fprintf(a.stdout, "body：\n%s\n", n.Body)
	}
}

// printNodeList：搜尋結果等一行一顆。
func (a *app) printNodeList(nodes []domain.Node) {
	if len(nodes) == 0 {
		fmt.Fprintln(a.stdout, "（沒有命中）")
		return
	}
	for _, n := range nodes {
		fmt.Fprintf(a.stdout, "%s  %s  %s %s  %s  %s\n",
			n.ID, n.Type, icon(n.Status), n.Status, n.Owner, n.Title)
	}
}

// printDeps：依賴清單一列一行「from → target」。
func (a *app) printDeps(links []domain.Link) {
	if len(links) == 0 {
		fmt.Fprintln(a.stdout, "（沒有依賴）")
		return
	}
	for _, l := range links {
		line := l.FromID + " → " + l.Target
		if l.Note != "" {
			line += "  " + l.Note
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func (a *app) printHistory(hs []domain.HistoryEntry) {
	if len(hs) == 0 {
		fmt.Fprintln(a.stdout, "（沒有事件）")
		return
	}
	for _, h := range hs {
		line := fmt.Sprintf("%s  %s  %s", fmtTime(h.TS), h.Actor, h.Action)
		if h.Field != "" {
			line += "  " + h.Field
		}
		if h.FromVal != "" || h.ToVal != "" {
			line += "  " + h.FromVal + " → " + h.ToVal
		}
		if h.Note != "" {
			line += "  " + h.Note
		}
		fmt.Fprintln(a.stdout, line)
	}
}

// printStats：各狀態計數、每人手上（未結案）張數與平均滯留天數、每 REQ 完成度、自我驗收張數。
func (a *app) printStats(st store.Stats) {
	fmt.Fprintln(a.stdout, "狀態計數：")
	order := []domain.Status{
		domain.StatusTodo, domain.StatusInProgress, domain.StatusReview,
		domain.StatusBlocked, domain.StatusHold, domain.StatusDone, domain.StatusCancel,
	}
	for _, s := range order {
		fmt.Fprintf(a.stdout, "  %s %-12s %d\n", icon(s), s, st.CountByStatus[s])
	}
	fmt.Fprintln(a.stdout, "每人手上張數（未結案）：")
	if len(st.CountByOwner) == 0 {
		fmt.Fprintln(a.stdout, "  （無）")
	}
	for _, o := range domain.Owners {
		if n, ok := st.CountByOwner[o]; ok {
			fmt.Fprintf(a.stdout, "  %-12s %d\n", o, n)
		}
	}
	fmt.Fprintln(a.stdout, "平均滯留天數（未結案）：")
	if len(st.AvgDwellDays) == 0 {
		fmt.Fprintln(a.stdout, "  （無）")
	}
	for _, o := range domain.Owners {
		if d, ok := st.AvgDwellDays[o]; ok {
			fmt.Fprintf(a.stdout, "  %-12s %.1f 天\n", o, d)
		}
	}
	fmt.Fprintln(a.stdout, "REQ 完成度：")
	if len(st.ReqProgress) == 0 {
		fmt.Fprintln(a.stdout, "  （無）")
	}
	for id, p := range st.ReqProgress {
		fmt.Fprintf(a.stdout, "  %-40s %.0f%%\n", id, p*100)
	}
	fmt.Fprintf(a.stdout, "自我驗收張數：%d\n", st.SelfVerifiedCount)
}
