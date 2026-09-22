package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
	"project_board/internal/web"
)

const (
	// pendingDecisionTag：`focus.awaiting_decision` 的資料來源（API_CONTRACT.md §5.1）。
	pendingDecisionTag = "pending-decision"
	// selfVerifiedPrefix：§11.2 自我驗收 verify 事件的 note 前綴。
	selfVerifiedPrefix = "[self-verified]"
	// recentLimit：焦點面板「最近異動」顯示筆數。
	recentLimit = 10
	// blockReasonScanLimit：往回找 transition→blocked 的視窗（§5.4 只需最新一筆，
	// 但那筆之後可能又有 comment，故抓一小段往回找第一個命中）。
	blockReasonScanLimit = 50
	// decisionCommentScanLimit：body 為空時往回找最新 comment 的視窗（§5.1）。
	decisionCommentScanLimit = 20
)

var taipei = time.FixedZone("Asia/Taipei", 8*60*60)

func formatTime(t time.Time) string { return t.In(taipei).Format(time.RFC3339) }

// storeSource 把 internal/store 的讀取結果組成 INTERFACE.md §3 的 JSON，實作 web.Source。
// PB05-A 的 FixtureSource 是它的假資料替身；phase 2 換成這個。
type storeSource struct{ st *store.Store }

func newSource(st *store.Store) *storeSource { return &storeSource{st: st} }

// ctx：web.Source（PB05-A 凍結）的方法不帶 context；本機內用、無長請求，
// 用 Background。未來若要 request-scoped cancellation 再擴介面。
func (s *storeSource) ctx() context.Context { return context.Background() }

// wrapNotFound：store.ErrNotFound → web.ErrNotFound，讓 web handler 轉 404；
// 其餘原樣往上（web 轉 500）。
func wrapNotFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return web.ErrNotFound
	}
	return err
}

func (s *storeSource) Health() (json.RawMessage, error) {
	v, err := s.st.SchemaVersion(s.ctx())
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "schema_version": v})
}

func (s *storeSource) Tree(q url.Values) (json.RawMessage, error) {
	nodes, err := s.st.Tree(s.ctx(), store.TreeFilter{
		Project: q.Get("project"),
		Status:  q.Get("status"),
		Owner:   q.Get("owner"),
		Tag:     q.Get("tag"),
		Type:    domain.NodeType(q.Get("type")),
	})
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return json.Marshal(buildTree(nodes))
}

func (s *storeSource) Node(id string) (json.RawMessage, error) {
	n, links, children, err := s.st.Get(s.ctx(), id)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return json.Marshal(toNodeDTO(n, links, children))
}

func (s *storeSource) History(id string, limit int) (json.RawMessage, error) {
	entries, err := s.st.History(s.ctx(), id, limit)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	out := make([]historyDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, toHistoryDTO(e))
	}
	return json.Marshal(out)
}

func (s *storeSource) Stats(q url.Values) (json.RawMessage, error) {
	project := q.Get("project")
	st, err := s.st.Stats(s.ctx(), project)
	if err != nil {
		return nil, err
	}
	focus, err := s.buildFocus(project)
	if err != nil {
		return nil, err
	}
	return json.Marshal(statsDTO{
		CountByStatus:     statusCounts(st.CountByStatus),
		CountByOwner:      st.CountByOwner,
		AvgDwellDays:      st.AvgDwellDays,
		ReqProgress:       st.ReqProgress,
		SelfVerifiedCount: st.SelfVerifiedCount,
		Focus:             focus,
	})
}

func (s *storeSource) Search(q url.Values) (json.RawMessage, error) {
	query := strings.TrimSpace(q.Get("q"))
	hits := make([]searchHit, 0)
	if query == "" {
		return json.Marshal(hits)
	}
	nodes, err := s.st.Search(s.ctx(), query, q.Get("project"))
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		hits = append(hits, searchHit{
			ID: n.ID, Type: string(n.Type), Title: n.Title, Status: string(n.Status), Owner: n.Owner,
		})
	}
	return json.Marshal(hits)
}

// Deps：`/api/deps?project=`（v0.2 依賴清單；呼叫 store.ListDependsOn，不自行寫 SQL）。
// project 空＝全庫；只回 kind=depends_on。
func (s *storeSource) Deps(q url.Values) (json.RawMessage, error) {
	links, err := s.st.ListDependsOn(s.ctx(), q.Get("project"))
	if err != nil {
		return nil, err
	}
	out := make([]depDTO, 0, len(links))
	for _, l := range links {
		out = append(out, depDTO{FromID: l.FromID, Target: l.Target, Note: l.Note})
	}
	return json.Marshal(out)
}

// Checklist：`/api/checklist?project=`（v0.3 母表清單；呼叫 store.Checklist，不自行寫 SQL）。
// project 空＝全庫；沒有 item 回 []（不是 null）。
func (s *storeSource) Checklist(q url.Values) (json.RawMessage, error) {
	rows, err := s.st.Checklist(s.ctx(), q.Get("project"))
	if err != nil {
		return nil, err
	}
	out := make([]checklistDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, checklistDTO{
			Key: r.Key, ItemID: r.ItemID, Title: r.Title,
			Status: string(r.Status), Owner: r.Owner, IssueID: r.IssueID,
		})
	}
	return json.Marshal(out)
}

// Report：`/api/report?project=&week=YYYY-MM-DD`（v0.3 週報；呼叫 store.Weekly）。
// 不帶 week＝本週；week 格式錯→400（web.ErrBadRequest）。三塊欄位對齊 cmd/pb weeklyJSON。
func (s *storeSource) Report(q url.Values) (json.RawMessage, error) {
	ref := time.Now()
	if w := strings.TrimSpace(q.Get("week")); w != "" {
		parsed, err := time.ParseInLocation("2006-01-02", w, taipei)
		if err != nil {
			return nil, web.ErrBadRequest
		}
		ref = parsed
	}
	rep, err := s.st.Weekly(s.ctx(), q.Get("project"), ref)
	if err != nil {
		return nil, err
	}
	return json.Marshal(reportDTO{
		WeekStart: formatTime(rep.WeekStart),
		WeekEnd:   formatTime(rep.WeekEnd),
		Did:       toHistoryDTOs(rep.Did),
		Closed:    toHistoryDTOs(rep.Closed),
		Open:      toReportNodes(rep.Open),
	})
}

// Meta：`/api/meta`（V05-DASHBOARD-META-API）。types 來自 DB node_types（依 sort 排序，
// 新增 type 自動反映）；owners 直接回 domain.Owners（Go 端唯一真相，不新開表）；
// statuses／tabs 來自 domain.StatusDefs／TabDefs（顯示用 metadata，狀態機轉移規則不受影響）。
// web.Source 的方法一律不帶 context（見上面 ctx 註解），故簽名為 Meta()。
func (s *storeSource) Meta() (json.RawMessage, error) {
	defs, err := s.st.ListNodeTypes(s.ctx())
	if err != nil {
		return nil, err
	}
	types := make([]metaTypeDTO, 0, len(defs))
	for _, d := range defs {
		types = append(types, metaTypeDTO{
			Key: string(d.Key), Label: d.Label,
			IDPrefix: d.IDPrefix, ParentType: string(d.ParentType), Sort: d.Sort,
		})
	}
	statusDefs := domain.StatusDefs()
	statuses := make([]metaStatusDTO, 0, len(statusDefs))
	for _, d := range statusDefs {
		statuses = append(statuses, metaStatusDTO{
			Key: string(d.Key), Label: d.Label, Icon: d.Icon, Color: d.Color, Sort: d.Sort,
		})
	}
	tabDefs := domain.TabDefs()
	tabs := make([]metaTabDTO, 0, len(tabDefs))
	for _, d := range tabDefs {
		tabs = append(tabs, metaTabDTO{Key: d.Key, Label: d.Label, Sort: d.Sort})
	}
	return json.Marshal(metaDTO{Types: types, Owners: domain.Owners, Statuses: statuses, Tabs: tabs})
}

func toHistoryDTOs(entries []domain.HistoryEntry) []historyDTO {
	out := make([]historyDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, toHistoryDTO(e))
	}
	return out
}

func toReportNodes(nodes []domain.Node) []reportNodeDTO {
	out := make([]reportNodeDTO, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, reportNodeDTO{
			ID: n.ID, Type: string(n.Type), Title: n.Title, Status: string(n.Status), Owner: n.Owner,
		})
	}
	return out
}

// ---- focus 組裝（API_CONTRACT.md §5）----
func (s *storeSource) buildFocus(project string) (focusDTO, error) {
	f := focusDTO{
		Blocked:          []focusBlocked{},
		AwaitingDecision: []focusDecision{},
		Recent:           []focusRecent{},
		SelfVerified:     []focusSelf{},
	}

	// 🚧 卡點：Tree(status=blocked) ＋ 最新一筆 transition→blocked 的 note。
	blocked, err := s.st.Tree(s.ctx(), store.TreeFilter{Project: project, Status: string(domain.StatusBlocked)})
	if err != nil {
		return f, wrapNotFound(err)
	}
	for _, n := range blocked {
		if n.Status != domain.StatusBlocked {
			continue // Tree 會帶祖先保持樹形，這裡只取本體
		}
		reason, err := s.blockReason(n.ID)
		if err != nil {
			return f, err
		}
		f.Blocked = append(f.Blocked, focusBlocked{ID: n.ID, Title: n.Title, Owner: n.Owner, Reason: reason})
	}

	// ❓ 待裁示：tags 含 pending-decision（§5.1），說明取 body 或最新 comment。
	pend, err := s.st.Tree(s.ctx(), store.TreeFilter{Project: project, Tag: pendingDecisionTag})
	if err != nil {
		return f, wrapNotFound(err)
	}
	for _, n := range pend {
		if !strings.Contains(n.Tags, pendingDecisionTag) {
			continue
		}
		note, err := s.decisionNote(n)
		if err != nil {
			return f, err
		}
		f.AwaitingDecision = append(f.AwaitingDecision, focusDecision{ID: n.ID, Title: n.Title, Note: note})
	}

	// 🕒 最近異動：跨節點 RecentHistory（§5.2）。
	recent, err := s.st.RecentHistory(s.ctx(), project, recentLimit)
	if err != nil {
		return f, err
	}
	for _, e := range recent {
		f.Recent = append(f.Recent, focusRecent{
			TS: formatTime(e.TS), Actor: e.Actor, NodeID: e.NodeID,
			Action: string(e.Action), Summary: recentSummary(e),
		})
	}

	// ⚠ 自我驗收：窮舉查詢(SelfVerifiedHistory)，跟 Stats.SelfVerifiedCount 同一條WHERE，
	// 數字才會跟這裡列出來的清單對得上。舊版用有limit的RecentHistory視窗掃，節點多的
	// 專案(如Y20260916)舊的self-verified事件會被擠出視窗外，變成「標題寫4張、底下卻列不
	// 出東西」——2026-09-22 導演發現回報。
	sv, err := s.st.SelfVerifiedHistory(s.ctx(), project)
	if err != nil {
		return f, err
	}
	for _, e := range sv {
		title, owner := "", ""
		if n, _, _, err := s.st.Get(s.ctx(), e.NodeID); err == nil {
			title, owner = n.Title, n.Owner
		}
		f.SelfVerified = append(f.SelfVerified, focusSelf{ID: e.NodeID, Title: title, Owner: owner, Note: e.Note})
	}
	return f, nil
}

// blockReason：由新往舊找第一筆 transition→blocked 的 note（History 為 id DESC）。
func (s *storeSource) blockReason(id string) (string, error) {
	entries, err := s.st.History(s.ctx(), id, blockReasonScanLimit)
	if err != nil {
		return "", wrapNotFound(err)
	}
	for _, e := range entries {
		if e.Action == domain.ActionTransition && e.ToVal == string(domain.StatusBlocked) {
			return e.Note, nil
		}
	}
	return "", nil
}

// decisionNote：body 優先；空則取最新一筆 comment（History 為 id DESC）。
func (s *storeSource) decisionNote(n domain.Node) (string, error) {
	if body := strings.TrimSpace(n.Body); body != "" {
		return body, nil
	}
	entries, err := s.st.History(s.ctx(), n.ID, decisionCommentScanLimit)
	if err != nil {
		return "", wrapNotFound(err)
	}
	for _, e := range entries {
		if e.Action == domain.ActionComment {
			return e.Note, nil
		}
	}
	return "", nil
}

// ---- domain → DTO ----

func buildTree(flat []domain.Node) []treeNode {
	present := make(map[string]bool, len(flat))
	for _, n := range flat {
		present[n.ID] = true
	}
	byParent := make(map[string][]domain.Node, len(flat))
	roots := make([]domain.Node, 0)
	for _, n := range flat {
		if n.ParentID == "" || !present[n.ParentID] {
			roots = append(roots, n)
			continue
		}
		byParent[n.ParentID] = append(byParent[n.ParentID], n)
	}
	out := make([]treeNode, 0, len(roots))
	for _, r := range roots {
		out = append(out, toTreeNode(r, byParent))
	}
	return out
}

func toTreeNode(n domain.Node, byParent map[string][]domain.Node) treeNode {
	children := make([]treeNode, 0, len(byParent[n.ID]))
	for _, c := range byParent[n.ID] {
		children = append(children, toTreeNode(c, byParent))
	}
	return treeNode{
		ID: n.ID, Type: string(n.Type), Title: n.Title, Status: string(n.Status),
		Owner: n.Owner, Priority: string(n.Priority), Children: children,
	}
}

func toNodeDTO(n domain.Node, links []domain.Link, children []domain.Node) nodeDTO {
	ls := make([]linkDTO, 0, len(links))
	for _, l := range links {
		ls = append(ls, linkDTO{Kind: string(l.Kind), Target: l.Target, Note: l.Note})
	}
	cs := make([]childDTO, 0, len(children))
	for _, c := range children {
		cs = append(cs, childDTO{ID: c.ID, Type: string(c.Type), Status: string(c.Status)})
	}
	return nodeDTO{
		ID: n.ID, Type: string(n.Type), Title: n.Title, Status: string(n.Status),
		Owner: n.Owner, Priority: string(n.Priority), Tags: n.Tags, Body: n.Body,
		CreatedAt: formatTime(n.CreatedAt), UpdatedAt: formatTime(n.UpdatedAt),
		Links: ls, Children: cs,
	}
}

func toHistoryDTO(e domain.HistoryEntry) historyDTO {
	return historyDTO{
		ID: e.ID, NodeID: e.NodeID, TS: formatTime(e.TS), Actor: e.Actor,
		Action: string(e.Action), Field: e.Field, FromVal: e.FromVal, ToVal: e.ToVal, Note: e.Note,
	}
}

func statusCounts(m map[domain.Status]int) map[string]int {
	out := make(map[string]int, len(m))
	// 七種狀態一律給鍵（缺的補 0），讓 dashboard 頂列統計穩定。
	for _, s := range []domain.Status{
		domain.StatusTodo, domain.StatusInProgress, domain.StatusReview, domain.StatusBlocked,
		domain.StatusHold, domain.StatusDone, domain.StatusCancel,
	} {
		out[string(s)] = 0
	}
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// recentSummary：把事件壓成一行給焦點面板「最近異動」顯示。
func recentSummary(e domain.HistoryEntry) string {
	switch e.Action {
	case domain.ActionTransition:
		s := e.FromVal + " → " + e.ToVal
		if e.Note != "" {
			s += "（" + e.Note + "）"
		}
		return s
	case domain.ActionVerify:
		return "verify：" + e.Note
	case domain.ActionLink:
		return "link " + e.Field + " → " + e.ToVal
	case domain.ActionUnlink:
		return "unlink " + e.Field + " → " + e.FromVal
	case domain.ActionComment:
		return "comment：" + e.Note
	case domain.ActionAssign:
		return "assign " + e.FromVal + " → " + e.ToVal
	case domain.ActionUpdate:
		return "update " + e.Field
	case domain.ActionCreate:
		if e.Note != "" {
			return e.Note
		}
		return "建立"
	default:
		if e.Note != "" {
			return e.Note
		}
		return string(e.Action)
	}
}
