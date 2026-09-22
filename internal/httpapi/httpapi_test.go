package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"
	"project_board/internal/web"
)

const (
	projectID = "Y20260916"
	reqID     = projectID + "/REQ-MEMBER-CORE"
	issueA    = reqID + "/ISSUE-ADMIN-BFF-UT90-A"
	conc01    = reqID + "/ISSUE-CONC01"
	conc03    = reqID + "/ISSUE-CONC03"
	archReq   = projectID + "/REQ-ARCH"
	archStore = archReq + "/ISSUE-ARCH-STORE"
)

// ---- setup ----

func newSeededStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Seed(ctx, "human"); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	mustCreate(t, st, "human", store.CreateInput{Type: domain.TypeReq, ID: reqID, Title: "會員核心", ParentID: projectID, Owner: "yilong", Priority: "high"})
	mustCreate(t, st, "xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: issueA, Title: "admin-bff UT90 軌A", ParentID: reqID, Owner: "xiaoxia", Priority: "high", Tags: "admin-bff,ut90", Body: "補齊核心路徑單測"})
	mustCreate(t, st, "xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: conc01, Title: "併發案例", ParentID: reqID, Owner: "xiaoxia", Priority: "high"})
	mustCreate(t, st, "xiaoxia", store.CreateInput{Type: domain.TypeIssue, ID: conc03, Title: "併發寫入保護", ParentID: reqID, Priority: "high"})
	mustCreate(t, st, "yilong", store.CreateInput{Type: domain.TypeReq, ID: archReq, Title: "架構體檢", ParentID: projectID, Owner: "yilong", Tags: "arch,pending-decision", Body: "D4 母表建模待裁示"})
	mustCreate(t, st, "claude", store.CreateInput{Type: domain.TypeIssue, ID: archStore, Title: "store 分層整理", ParentID: archReq, Owner: "kaimadi", Tags: "arch,store,pending-decision"})

	// A：todo → in_progress → review → link commit → verify（actor==owner → self-verified）
	mustTransition(t, st, "xiaoxia", issueA, domain.StatusInProgress, "", nil)
	mustTransition(t, st, "xiaoxia", issueA, domain.StatusReview, "", nil)
	mustLink(t, st, "xiaoxia", issueA, domain.LinkCommit, "8094064", "")
	mustVerify(t, st, "xiaoxia", issueA, "覆蓋率 98%")

	mustTransition(t, st, "xiaoxia", conc01, domain.StatusInProgress, "", nil)

	// CONC03：depends_on + 轉 blocked（附理由）
	mustLink(t, st, "xiaoxia", conc03, domain.LinkDependsOn, conc01, "等 CONC01 併發案例")
	mustTransition(t, st, "xiaoxia", conc03, domain.StatusBlocked, "等 CONC01 併發案例", nil)

	// pending-decision：body 為空者靠最新 comment
	if err := st.Comment(ctx, "claude", archStore, "A7 CI Go 版本待裁示"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	return st
}

func mustCreate(t *testing.T, st *store.Store, actor string, in store.CreateInput) {
	t.Helper()
	if _, err := st.Create(context.Background(), actor, in); err != nil {
		t.Fatalf("Create %s: %v", in.ID, err)
	}
}

func mustTransition(t *testing.T, st *store.Store, actor, id string, to domain.Status, note string, exp *time.Time) {
	t.Helper()
	if _, err := st.Transition(context.Background(), actor, id, to, note, exp); err != nil {
		t.Fatalf("Transition %s→%s: %v", id, to, err)
	}
}

func mustLink(t *testing.T, st *store.Store, actor, fromID string, kind domain.LinkKind, target, note string) {
	t.Helper()
	if _, err := st.Link(context.Background(), actor, fromID, kind, target, note); err != nil {
		t.Fatalf("Link %s %s→%s: %v", fromID, kind, target, err)
	}
}

func mustVerify(t *testing.T, st *store.Store, actor, id, note string) {
	t.Helper()
	if _, err := st.Verify(context.Background(), actor, id, note); err != nil {
		t.Fatalf("Verify %s: %v", id, err)
	}
}

func newTestHandler(t *testing.T) http.Handler {
	return New(newSeededStore(t))
}

func doReq(h http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rr.Body.String())
	}
}

// ---- 端點整合測試 ----

func TestHealthz(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/healthz")
	var body struct {
		OK            bool `json:"ok"`
		SchemaVersion int  `json:"schema_version"`
	}
	decode(t, rr, &body)
	if !body.OK || body.SchemaVersion < 1 {
		t.Fatalf("healthz = %+v", body)
	}
}

func findTreeNode(nodes []treeNode, id string) *treeNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
		if got := findTreeNode(nodes[i].Children, id); got != nil {
			return got
		}
	}
	return nil
}

func TestTreeNested(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/tree?project="+projectID)
	var roots []treeNode
	decode(t, rr, &roots)
	if len(roots) != 1 || roots[0].ID != projectID {
		t.Fatalf("roots = %+v", roots)
	}
	a := findTreeNode(roots, issueA)
	if a == nil {
		t.Fatalf("樹上找不到 %s", issueA)
	}
	if a.Type != "issue" || a.Status != "done" || a.Owner != "xiaoxia" || a.Priority != "high" {
		t.Fatalf("issueA = %+v", a)
	}
	if findTreeNode(roots, reqID) == nil {
		t.Fatalf("樹上找不到祖先 %s", reqID)
	}
	if !strings.Contains(rr.Body.String(), `"children":[]`) {
		t.Fatalf("葉節點 children 應為 []，body=%s", rr.Body.String())
	}
}

func TestTreeFilterBlockedKeepsAncestors(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/tree?status=blocked&project="+projectID)
	var roots []treeNode
	decode(t, rr, &roots)
	if findTreeNode(roots, conc03) == nil {
		t.Fatal("blocked 過濾應含 CONC03")
	}
	if findTreeNode(roots, reqID) == nil {
		t.Fatal("blocked 過濾應帶上祖先 REQ")
	}
	if findTreeNode(roots, issueA) != nil {
		t.Fatal("blocked 過濾不應含 done 的 A")
	}
}

func TestTreeProjectNotFound(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/tree?project=Y00000000")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestNodeDetail(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/"+issueA)
	var n nodeDTO
	decode(t, rr, &n)
	if n.ID != issueA || n.Type != "issue" || n.Status != "done" || n.Owner != "xiaoxia" ||
		n.Priority != "high" || n.Tags != "admin-bff,ut90" || n.Body == "" {
		t.Fatalf("node = %+v", n)
	}
	if len(n.Links) != 1 || n.Links[0].Kind != "commit" || n.Links[0].Target != "8094064" {
		t.Fatalf("links = %+v", n.Links)
	}
	if n.Children == nil || len(n.Children) != 0 {
		t.Fatalf("children = %+v", n.Children)
	}
	if !strings.HasSuffix(n.CreatedAt, "+08:00") || !strings.HasSuffix(n.UpdatedAt, "+08:00") {
		t.Fatalf("時間應為 +08:00：%s / %s", n.CreatedAt, n.UpdatedAt)
	}
	if _, err := time.Parse(time.RFC3339, n.CreatedAt); err != nil {
		t.Fatalf("created_at 非 RFC3339: %v", err)
	}
}

func TestNodeWithChildren(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/"+reqID)
	var n nodeDTO
	decode(t, rr, &n)
	if len(n.Children) != 3 {
		t.Fatalf("req children len = %d, want 3 (%+v)", len(n.Children), n.Children)
	}
	if n.Children[0].Type != "issue" {
		t.Fatalf("child = %+v", n.Children[0])
	}
}

func TestNodeNotFound(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/no/such")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHistory(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/node/"+issueA+"/history?limit=1")
	var one []historyDTO
	decode(t, rr, &one)
	if len(one) != 1 {
		t.Fatalf("limit=1 len = %d", len(one))
	}
	if one[0].Action != "verify" || !strings.HasPrefix(one[0].Note, selfVerifiedPrefix) {
		t.Fatalf("最新事件應為 self-verified verify: %+v", one[0])
	}

	rr = doReq(h, http.MethodGet, "/api/node/"+issueA+"/history")
	var all []historyDTO
	decode(t, rr, &all)
	if len(all) < 5 {
		t.Fatalf("history len = %d, want >= 5", len(all))
	}
}

func TestHistoryNotFound(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/no/such/history")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestStats(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/stats?project="+projectID)
	var s statsDTO
	decode(t, rr, &s)

	if len(s.CountByStatus) != 7 {
		t.Fatalf("count_by_status 應有 7 鍵: %+v", s.CountByStatus)
	}
	if s.CountByStatus["done"] != 1 || s.CountByStatus["blocked"] != 1 || s.CountByStatus["cancel"] != 0 {
		t.Fatalf("count_by_status = %+v", s.CountByStatus)
	}
	// CountByOwner＝未結案（§5.3）：A 已 done，故 xiaoxia 只有 CONC01。
	if s.CountByOwner["xiaoxia"] != 1 || s.CountByOwner["human"] != 1 ||
		s.CountByOwner["unassigned"] != 1 || s.CountByOwner["kaimadi"] != 1 || s.CountByOwner["yilong"] != 2 {
		t.Fatalf("count_by_owner(open) = %+v", s.CountByOwner)
	}
	if s.SelfVerifiedCount != 1 {
		t.Fatalf("self_verified_count = %d", s.SelfVerifiedCount)
	}

	if len(s.Focus.Blocked) != 1 || s.Focus.Blocked[0].ID != conc03 || s.Focus.Blocked[0].Reason != "等 CONC01 併發案例" {
		t.Fatalf("focus.blocked = %+v", s.Focus.Blocked)
	}
	if len(s.Focus.AwaitingDecision) != 2 {
		t.Fatalf("focus.awaiting_decision = %+v", s.Focus.AwaitingDecision)
	}
	notes := map[string]string{}
	for _, d := range s.Focus.AwaitingDecision {
		notes[d.ID] = d.Note
	}
	if !strings.Contains(notes[archReq], "D4") {
		t.Fatalf("archReq note(取 body) = %q", notes[archReq])
	}
	if !strings.Contains(notes[archStore], "A7") {
		t.Fatalf("archStore note(取最新 comment) = %q", notes[archStore])
	}
	if len(s.Focus.Recent) == 0 {
		t.Fatal("focus.recent 不應為空")
	}
	if len(s.Focus.SelfVerified) != 1 || s.Focus.SelfVerified[0].ID != issueA ||
		s.Focus.SelfVerified[0].Owner != "xiaoxia" {
		t.Fatalf("focus.self_verified = %+v", s.Focus.SelfVerified)
	}
}

func TestStatsBadProject(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/stats?project=Y00000000")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSearch(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/search?q=admin&project="+projectID)
	var hits []searchHit
	decode(t, rr, &hits)
	found := false
	for _, x := range hits {
		if x.ID == issueA {
			found = true
		}
	}
	if !found {
		t.Fatalf("q=admin 應命中 A: %+v", hits)
	}

	rr = doReq(h, http.MethodGet, "/api/search")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("空查詢應回 [], got %q", got)
	}

	rr = doReq(h, http.MethodGet, "/api/search?q=zzznope")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("無命中應回 [], got %q", got)
	}
}

func TestDeps(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/deps?project="+projectID)
	var deps []depDTO
	decode(t, rr, &deps)
	if len(deps) != 1 || deps[0].FromID != conc03 || deps[0].Target != conc01 || deps[0].Note == "" {
		t.Fatalf("deps = %+v", deps)
	}

	rr = doReq(h, http.MethodGet, "/api/deps")
	var all []depDTO
	decode(t, rr, &all)
	if len(all) != 1 {
		t.Fatalf("全庫 deps = %+v", all)
	}

	rr = doReq(h, http.MethodGet, "/api/deps?project=Y00000000")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("無關專案應回 [], got %q", got)
	}
}

// TestStatsAvgDwell：/api/stats 要透傳 store 的 avg_dwell_days（v0.3 進度視圖）。
func TestStatsAvgDwell(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/stats?project="+projectID)
	var s statsDTO
	decode(t, rr, &s)
	if len(s.AvgDwellDays) == 0 {
		t.Fatalf("avg_dwell_days 不應為空: %+v", s.AvgDwellDays)
	}
	if _, ok := s.AvgDwellDays["unassigned"]; !ok {
		t.Fatalf("未結案的 unassigned 應有滯留天數: %+v", s.AvgDwellDays)
	}
}

// TestChecklist：v0.3 母表（item 索引 → depends_on 指回 issue）。
func TestChecklist(t *testing.T) {
	st := newSeededStore(t)
	const itemID = reqID + "/ITEM-A1"
	mustCreate(t, st, "xiaoxia", store.CreateInput{
		Type: domain.TypeItem, ID: itemID, Title: "四服務界線", ParentID: reqID, Owner: "xiaoxia",
	})
	mustLink(t, st, "xiaoxia", itemID, domain.LinkDependsOn, issueA, "對應單")
	h := New(st)

	rr := doReq(h, http.MethodGet, "/api/checklist?project="+projectID)
	var rows []checklistDTO
	decode(t, rr, &rows)
	if len(rows) != 1 {
		t.Fatalf("checklist = %+v", rows)
	}
	if rows[0].Key != "A1" || rows[0].ItemID != itemID || rows[0].IssueID != issueA || rows[0].Owner != "xiaoxia" {
		t.Fatalf("列內容 = %+v", rows[0])
	}

	rr = doReq(h, http.MethodGet, "/api/checklist?project=Y00000000")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("無關專案應回 [], got %q", got)
	}
}

// TestReport：v0.3 週報（did／closed／open）；week 格式錯→400。
func TestReport(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/report?project="+projectID)
	var rep reportDTO
	decode(t, rr, &rep)
	if rep.WeekStart == "" || rep.WeekEnd == "" {
		t.Fatalf("週報缺週界: %+v", rep)
	}
	if len(rep.Did) == 0 || len(rep.Closed) == 0 || len(rep.Open) == 0 {
		t.Fatalf("週報三塊不應為空: %+v", rep)
	}
	if rep.Closed[0].Action != "verify" {
		t.Errorf("closed 只收 verify: %+v", rep.Closed)
	}
	for _, n := range rep.Open {
		if n.Status != "in_progress" && n.Status != "review" {
			t.Errorf("open 只列 in_progress／review: %+v", n)
		}
	}

	rr = doReq(h, http.MethodGet, "/api/report?week=2026-09-10")
	if rr.Code != http.StatusOK {
		t.Fatalf("合法 week status = %d", rr.Code)
	}

	rr = doReq(h, http.MethodGet, "/api/report?week=2026/09/10")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法 week status = %d, want 400", rr.Code)
	}
}

// TestMeta：v0.5 型別／負責人清單（types 來自 node_types、owners 來自 domain.Owners）。
func TestMeta(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/meta")
	var m metaDTO
	decode(t, rr, &m)

	if len(m.Types) != 7 {
		t.Fatalf("types len = %d, want 7: %+v", len(m.Types), m.Types)
	}
	// 依 sort 排序，值與凍結合約一致。
	wantKeys := []string{"project", "req", "issue", "report", "item", "bug", "plan"}
	for i, d := range m.Types {
		if d.Key != wantKeys[i] || d.Sort != i+1 {
			t.Fatalf("types[%d] = %+v, want key=%s sort=%d", i, d, wantKeys[i], i+1)
		}
	}
	if bug := m.Types[5]; bug.Label != "BUG" || bug.IDPrefix != "BUG" || bug.ParentType != "req" {
		t.Fatalf("bug meta = %+v", bug)
	}
	if plan := m.Types[6]; plan.Label != "計畫" || plan.IDPrefix != "PLAN" || plan.ParentType != "" {
		t.Fatalf("plan meta = %+v", m.Types[6])
	}
	if len(m.Owners) != 7 {
		t.Fatalf("owners len = %d, want 7: %+v", len(m.Owners), m.Owners)
	}
	for i, o := range domain.Owners {
		if m.Owners[i] != o {
			t.Fatalf("owners[%d] = %q, want %q", i, m.Owners[i], o)
		}
	}
	// statuses：7 筆顯示 metadata（V05-STATUS-META-API），值與 dashboard 舊 var STATUS
	// 一字一致、依 sort 排序。
	if len(m.Statuses) != 7 {
		t.Fatalf("statuses len = %d, want 7: %+v", len(m.Statuses), m.Statuses)
	}
	wantStatuses := []metaStatusDTO{
		{Key: "todo", Label: "未開始", Icon: "○", Color: "#5f6368", Sort: 1},
		{Key: "in_progress", Label: "進行中", Icon: "◐", Color: "#1a73e8", Sort: 2},
		{Key: "review", Label: "待驗收", Icon: "◑", Color: "#f9ab00", Sort: 3},
		{Key: "blocked", Label: "卡住", Icon: "●", Color: "#d93025", Sort: 4},
		{Key: "hold", Label: "暫緩", Icon: "◌", Color: "#80868b", Sort: 5},
		{Key: "done", Label: "完成", Icon: "✔", Color: "#188038", Sort: 6},
		{Key: "cancel", Label: "不做", Icon: "✕", Color: "#9aa0a6", Sort: 7},
	}
	for i, d := range m.Statuses {
		if d != wantStatuses[i] {
			t.Fatalf("statuses[%d] = %+v, want %+v", i, d, wantStatuses[i])
		}
	}
	// tabs：4 個 dashboard 分頁（V05-STATUS-META-API 追加），值與舊 var TAB_LABEL 一致。
	if len(m.Tabs) != 4 {
		t.Fatalf("tabs len = %d, want 4: %+v", len(m.Tabs), m.Tabs)
	}
	wantTabs := []metaTabDTO{
		{Key: "focus", Label: "焦點", Sort: 1},
		{Key: "progress", Label: "進度", Sort: 2},
		{Key: "checklist", Label: "母表", Sort: 3},
		{Key: "weekly", Label: "週報", Sort: 4},
	}
	for i, d := range m.Tabs {
		if d != wantTabs[i] {
			t.Fatalf("tabs[%d] = %+v, want %+v", i, d, wantTabs[i])
		}
	}
	// 欄位名為 snake_case（id_prefix／parent_type／icon／color），不可洩漏 Go 欄位名。
	body := rr.Body.String()
	if !strings.Contains(body, `"id_prefix"`) || !strings.Contains(body, `"parent_type"`) ||
		!strings.Contains(body, `"statuses"`) || !strings.Contains(body, `"tabs"`) ||
		!strings.Contains(body, `"icon"`) || !strings.Contains(body, `"color"`) ||
		strings.Contains(body, "IDPrefix") || strings.Contains(body, "ParentType") ||
		strings.Contains(body, "Icon") || strings.Contains(body, "Color") {
		t.Fatalf("meta JSON 欄位名不符合約：%s", body)
	}
}

// TestMetaErrorOnClosedStore：資料來源錯誤要往上傳（web 轉 500），不吞掉。
func TestMetaErrorOnClosedStore(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "closed-meta.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := newSource(st).Meta(); err == nil {
		t.Fatal("關閉的 store Meta 應回錯")
	}
}

func TestHealthErrorOnClosedStore(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := newSource(st).Health(); err == nil {
		t.Fatal("關閉的 store 應回錯")
	}
}

func TestDashboardHTMLAndRouting(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/")
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET / = %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	if !strings.Contains(rr.Body.String(), "ProjectBoard") {
		t.Fatal("dashboard HTML 應含 ProjectBoard")
	}

	// SPA fallback：非 API 的未知路徑回單頁（deep-link）；/api/ 下的未知路徑仍 404。
	if rr := doReq(h, http.MethodGet, "/nope"); rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/nope SPA fallback = %d %s, want 200 text/html", rr.Code, rr.Header().Get("Content-Type"))
	}
	if rr := doReq(h, http.MethodGet, "/api/nope"); rr.Code != http.StatusNotFound {
		t.Fatalf("/api/nope = %d, want 404", rr.Code)
	}
	if rr := doReq(h, http.MethodPost, "/api/tree"); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/tree = %d, want 405", rr.Code)
	}
}

// ---- 單元測試（DTO／helper）----

func TestWrapNotFound(t *testing.T) {
	if got := wrapNotFound(store.ErrNotFound); !errors.Is(got, web.ErrNotFound) {
		t.Fatalf("store.ErrNotFound 應轉 web.ErrNotFound, got %v", got)
	}
	other := errors.New("boom")
	if got := wrapNotFound(other); !errors.Is(got, other) {
		t.Fatalf("其他錯誤應原樣, got %v", got)
	}
}

func TestFormatTime(t *testing.T) {
	got := formatTime(time.Date(2026, 9, 20, 1, 3, 0, 0, time.UTC))
	if got != "2026-09-20T09:03:00+08:00" {
		t.Fatalf("formatTime = %s", got)
	}
}

func TestStatusCountsSeedsAllSeven(t *testing.T) {
	out := statusCounts(map[domain.Status]int{domain.StatusDone: 3})
	if len(out) != 7 || out["done"] != 3 || out["cancel"] != 0 {
		t.Fatalf("statusCounts = %+v", out)
	}
}

func TestBuildTreeOrphanAndEmpty(t *testing.T) {
	if got := buildTree(nil); len(got) != 0 {
		t.Fatalf("buildTree(nil) = %+v", got)
	}
	flat := []domain.Node{
		{ID: "orphan", ParentID: "missing-parent"},
		{ID: "root"},
		{ID: "child", ParentID: "root"},
	}
	got := buildTree(flat)
	if len(got) != 2 {
		t.Fatalf("roots = %+v (orphan 應自成根)", got)
	}
	var root *treeNode
	for i := range got {
		if got[i].ID == "root" {
			root = &got[i]
		}
	}
	if root == nil || len(root.Children) != 1 || root.Children[0].ID != "child" {
		t.Fatalf("root = %+v", root)
	}
}

func TestRecentSummaryAllActions(t *testing.T) {
	cases := []struct {
		e    domain.HistoryEntry
		want string
	}{
		{domain.HistoryEntry{Action: domain.ActionTransition, FromVal: "todo", ToVal: "in_progress"}, "todo → in_progress"},
		{domain.HistoryEntry{Action: domain.ActionTransition, FromVal: "todo", ToVal: "blocked", Note: "等 X"}, "todo → blocked（等 X）"},
		{domain.HistoryEntry{Action: domain.ActionVerify, Note: "98%"}, "verify：98%"},
		{domain.HistoryEntry{Action: domain.ActionLink, Field: "commit", ToVal: "8094064"}, "link commit → 8094064"},
		{domain.HistoryEntry{Action: domain.ActionUnlink, Field: "commit", FromVal: "8094064"}, "unlink commit → 8094064"},
		{domain.HistoryEntry{Action: domain.ActionComment, Note: "hi"}, "comment：hi"},
		{domain.HistoryEntry{Action: domain.ActionAssign, FromVal: "unassigned", ToVal: "xiaoxia"}, "assign unassigned → xiaoxia"},
		{domain.HistoryEntry{Action: domain.ActionUpdate, Field: "body"}, "update body"},
		{domain.HistoryEntry{Action: domain.ActionCreate, Note: "建立 X"}, "建立 X"},
		{domain.HistoryEntry{Action: domain.ActionCreate}, "建立"},
		{domain.HistoryEntry{Action: "weird", Note: "note"}, "note"},
		{domain.HistoryEntry{Action: "weird"}, "weird"},
	}
	for _, c := range cases {
		if got := recentSummary(c.e); got != c.want {
			t.Errorf("recentSummary(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
}

func TestBlockReasonNoneWhenNeverBlocked(t *testing.T) {
	st := newSeededStore(t)
	src := newSource(st)
	reason, err := src.blockReason(issueA)
	if err != nil {
		t.Fatalf("blockReason: %v", err)
	}
	if reason != "" {
		t.Fatalf("A 從未 blocked，reason 應為空，got %q", reason)
	}
}

func TestDecisionNoteEmptyWhenNoBodyNoComment(t *testing.T) {
	st := newSeededStore(t)
	src := newSource(st)
	n, _, _, err := st.Get(context.Background(), conc01)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	note, err := src.decisionNote(n)
	if err != nil {
		t.Fatalf("decisionNote: %v", err)
	}
	if note != "" {
		t.Fatalf("conc01 無 body 無 comment，note 應為空，got %q", note)
	}
}
