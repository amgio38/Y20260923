package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

const sampleNode = "Y20260916/REQ-MEMBER-CORE/ISSUE-ADMIN-BFF-UT90-A"

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	src, err := NewFixtureSource()
	if err != nil {
		t.Fatalf("NewFixtureSource: %v", err)
	}
	return NewHandler(src)
}

func doReq(h http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandlerImplementsHTTPHandler(t *testing.T) {
	var _ http.Handler = newTestHandler(t)
}

func TestHealthz(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/healthz")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var body struct {
		OK            bool `json:"ok"`
		SchemaVersion int  `json:"schema_version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.OK || body.SchemaVersion != 1 {
		t.Fatalf("body = %+v", body)
	}
}

func TestDashboardHTML(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rr.Body.String()
	// Google 後台風格硬性規格（INTERFACE.md §4）：系統字體＋灰階色票。
	for _, want := range []string{
		"ProjectBoard",
		"-apple-system",
		"#f8f9fa", "#dadce0", "#202124", "#5f6368",
		"#1a73e8", "#f9ab00", "#d93025", "#188038",
		"焦點", "卡點", "依賴", "待裁示", "最近異動", "每人手上張數", "自我驗收",
		"進度", "母表", "週報",
		"/api/tree", "/api/stats", "/api/node/", "/api/deps", "/api/checklist", "/api/report",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少 %q", want)
		}
	}
	// 無外部字型／框架／CDN 依賴。
	for _, bad := range []string{"http://", "https://", "<link", "cdn.", "googleapis", "unpkg", "jsdelivr"} {
		if strings.Contains(body, bad) {
			t.Errorf("dashboard.html 不應含外部依賴 %q", bad)
		}
	}
}

// TestDashboardDarkMode：INTERFACE.md §4 黑夜模式規格（純前端，不動 API）。
func TestDashboardDarkMode(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	// 切換鈕（header 右上角、太陽／月亮兩 icon）。
	for _, want := range []string{
		`id="theme-toggle"`, `class="theme-toggle"`, `aria-label="切換為暗色模式"`,
		`class="icon-sun"`, `class="icon-moon"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少切換鈕元素 %q", want)
		}
	}
	// CSS custom properties 覆寫（非整份 CSS 複製）。
	if !strings.Contains(body, `[data-theme="dark"]`) {
		t.Error(`缺少 [data-theme="dark"] 覆寫`)
	}
	if !strings.Contains(body, "color-scheme: dark") {
		t.Error("缺少 color-scheme: dark")
	}
	if strings.Count(body, ":root") != 1 {
		t.Errorf(":root 應只出現一次（勿重抄整份 CSS），got %d", strings.Count(body, ":root"))
	}
	for _, tok := range []string{"--bg:", "--card:", "--border:", "--text:", "--muted:", "--hover:", "--selected:", "--accent:", "--st-done:"} {
		if got := strings.Count(body, tok); got != 2 {
			t.Errorf("%s 應只出現兩次（亮＋暗），got %d", tok, got)
		}
	}
	// 暗色 token 值（2026-09-22 導演指定：對齊 claude.ai cds token）。
	for _, want := range []string{
		"#151515", "#20201f", "rgba(255, 255, 255, .10)", "#f0efec", "#898781",
		"rgba(255, 255, 255, .06)", "rgba(217, 119, 87, .16)", "#d97757",
		"#c98500", "#e66767", "#6d6b67", "#91d68b", "#52514e",
		// Claude Code 風格 icon＋品牌小圖示（小蝦頭像，內嵌 /assets 提供）。
		`class="brand-mark"`, `"sicon"`, `src="/assets/avatar.png"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少 claude.ai／Claude Code 風格元素 %q", want)
		}
	}
	// localStorage 記憶 + 首次跟隨系統。
	for _, want := range []string{"localStorage", "pb-theme", "prefers-color-scheme", "matchMedia"} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少主題邏輯 %q", want)
		}
	}
	// 主題需在 <body> 之前套用（避免 FOUC）。
	if i, j := strings.Index(body, "pb-theme"), strings.Index(body, "<body>"); i < 0 || j < 0 || i > j {
		t.Errorf("主題初始套用應在 <body> 之前 (pb-theme@%d, body@%d)", i, j)
	}
}

// TestDashboardProjectFilter：樹的專案下拉（v0.2，只動前端、不改 API）。
func TestDashboardProjectFilter(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	// 專案下拉與負責人下拉並列；狀態改由頂部 statbar chip 單一控制，這裡不重做。
	for _, want := range []string{
		`id="f-project"`, `aria-label="專案過濾"`, `全部專案`,
		`id="f-owner"`, `aria-label="負責人過濾"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少 %q", want)
		}
	}
	// 專案選項由樹根生成；節點專案＝id 第一段。
	for _, want := range []string{
		"function projectOf(id)",
		`i < 0 ? s : s.slice(0, i)`,
		"state.filterProject",
		"n.id + \" — \" + n.title",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少專案過濾邏輯 %q", want)
		}
	}
	// 三個篩選同時生效：matches 內含三項判斷。
	if !strings.Contains(body, "if (state.filterProject && projectOf(n.id) !== state.filterProject) return false;") ||
		!strings.Contains(body, "if (state.filterStatus && n.status !== state.filterStatus) return false;") ||
		!strings.Contains(body, "if (state.filterOwner && n.owner !== state.filterOwner) return false;") {
		t.Error("matches 應同時套用專案／狀態／負責人三個篩選")
	}
	// 下拉變更要重繪樹。
	if !strings.Contains(body, `document.getElementById("f-project").addEventListener("change"`) {
		t.Error("缺少 f-project change 事件")
	}
}

// TestDashboardDepsRow：焦點區新增「依賴」列（列出 from → target），且不動既有「卡點」列。
func TestDashboardDepsRow(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()
	for _, want := range []string{
		`"依賴"`, `"卡點"`, // 新列存在、舊列保留（不合併）
		"state.deps",
		"x.from_id",
		"x.target",
		`getJSON("/api/deps" + projectQuery())`, // loadDeps 要抓依賴資料（跟目前專案）
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少依賴列元素 %q", want)
		}
	}
	if strings.Count(body, `"卡點"`) != 1 {
		t.Errorf("卡點列應只保留一處，got %d", strings.Count(body, `"卡點"`))
	}
}

// TestDashboardDepsFailSoft：/api/deps 失敗只讓依賴列變空；tree／stats 失敗仍整頁失敗（v0.2 裁示）。
func TestDashboardDepsFailSoft(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	// 主體載入：tree 先抓（決定預設專案），stats 跟著目前專案抓；失敗整頁失敗。
	for _, want := range []string{
		`getJSON("/api/tree")`,
		`getJSON("/api/stats" + projectQuery())`,
		`"載入失敗：" + err.message`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少主體載入邏輯 %q", want)
		}
	}
	// deps 不綁進主體；獨立 loadDeps，失敗吞掉、回空清單，且跟目前專案走。
	if strings.Contains(body, `getJSON("/api/deps")])`) {
		t.Error("deps 不應被綁進主體")
	}
	for _, want := range []string{
		"function loadDeps()",
		`getJSON("/api/deps" + projectQuery())`,
		".catch(function () { state.deps = []; })",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少 fail-soft 邏輯 %q", want)
		}
	}
}

// TestChecklistAPI：fixture 的 /api/checklist（v0.3 母表），含 project 過濾。
func TestChecklistAPI(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/checklist")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var all []fixtureChecklist
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("unmarshal checklist: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("checklist len = %d, want 3", len(all))
	}
	for _, r := range all {
		if r.Key == "" || r.ItemID == "" || r.Title == "" {
			t.Errorf("checklist 欄位不齊: %+v", r)
		}
	}

	rr = doReq(h, http.MethodGet, "/api/checklist?project=Y20260916/REQ-ARCH")
	var scoped []fixtureChecklist
	if err := json.Unmarshal(rr.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("unmarshal scoped: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Key != "B1" {
		t.Fatalf("project 過濾 = %+v", scoped)
	}

	rr = doReq(h, http.MethodGet, "/api/checklist?project=Y00000000")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("無關 project 應回 [], got %q", got)
	}
}

// TestReportAPI：fixture 的 /api/report（v0.3 週報）三塊欄位齊全。
func TestReportAPI(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/report")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var rep struct {
		WeekStart string `json:"week_start"`
		WeekEnd   string `json:"week_end"`
		Did       []struct {
			Actor  string `json:"actor"`
			Action string `json:"action"`
			NodeID string `json:"node_id"`
		} `json:"did"`
		Closed []struct {
			Action string `json:"action"`
		} `json:"closed"`
		Open []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Owner  string `json:"owner"`
		} `json:"open"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
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
		if n.ID == "" || n.Title == "" || n.Owner == "" || n.Status == "" {
			t.Errorf("open 欄位不齊: %+v", n)
		}
	}
}

// TestDashboardV03Sections：進度／母表／週報三區的渲染邏輯與資料來源。
func TestDashboardV03Sections(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	for _, want := range []string{
		// 三個容器。
		`id="progress"`, `id="checklist"`, `id="weekly"`,
		// 進度：REQ 完成度＋平均滯留天數。
		"function renderProgress()", "state.stats", "req_progress", "avg_dwell_days",
		// 母表：依 KEY 首字母分組。
		"function renderChecklist()", "state.checklist", `getJSON("/api/checklist" + projectQuery())`,
		// 週報：三塊。
		"function renderWeekly()", "state.report", `getJSON("/api/report" + projectQuery())`,
		"rep.did", "rep.closed", "rep.open",
		// 跟著專案下拉重新抓範圍。
		"function reloadProject()", "function projectQuery()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少 v0.3 元素 %q", want)
		}
	}
	// 三區都要 fail-soft（失敗只清空該區）。
	for _, want := range []string{
		".catch(function () { state.checklist = []; })",
		".catch(function () { state.report = null; })",
		"function loadExtras()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少 fail-soft 邏輯 %q", want)
		}
	}
}

// TestDashboardTabs：焦點／進度／母表／週報收斂成 header tab（預設收合、點擊展開）；
// 樹主專案預設不展開（2026-09-20 學長指示）。
func TestDashboardTabs(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	// header 分頁列與四個 tab。
	for _, want := range []string{
		`id="tabs"`, `role="tablist"`,
		`data-tab="focus"`, `data-tab="progress"`, `data-tab="checklist"`, `data-tab="weekly"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少分頁元素 %q", want)
		}
	}

	// 四個面板預設 hidden，且外層 panel 卡片預設 hidden（要看時才點開）。
	for _, want := range []string{
		`id="panel" hidden`,
		`class="focus" id="focus" role="tabpanel" hidden`,
		`id="progress" role="tabpanel" hidden`,
		`id="checklist" role="tabpanel" hidden`,
		`id="weekly" role="tabpanel" hidden`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("面板預設應為收合（hidden）：缺少 %q", want)
		}
	}

	// 切換邏輯：openTab／setActiveTab／state.activeTab；首次開 tab 才載入加值資料（lazy）。
	for _, want := range []string{
		"function setActiveTab(id)", "function openTab(id)", "state.activeTab",
		"if (!extrasLoaded) { extrasLoaded = true; loadExtras(); }",
		`document.querySelectorAll(".tab")`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少分頁切換邏輯 %q", want)
		}
	}

	// 樹預設不展開：不得再自動展開（expandDefaults 已移除），主專案預設需收合。
	if strings.Contains(body, "expandDefaults") {
		t.Error("樹不應再自動展開（expandDefaults 應移除），主專案預設需收合")
	}
}

// TestDashboardProjectRoot：母表／進度／週報以「專案為根」分組（全部模式不再混成一張）。
func TestDashboardProjectRoot(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	for _, want := range []string{
		// 共用工具。
		"function groupByProject(items, idOf)", "function projectTitle(pid)",
		// CSS 分節標題。
		".proj-title",
		// 母表：依專案分組後再依字母畫表。
		"groupByProject(rows, function (r) { return r.item_id; })",
		"function renderChecklistRows(host, rows)",
		// 進度：REQ 完成度依專案分組。
		"groupByProject(ids, function (id) { return id; })",
		// 週報：三塊依專案分節。
		"return projectOf(h.node_id) === g.key;", "return projectOf(n.id) === g.key;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少「專案為根」元素 %q", want)
		}
	}

	// 預設範圍＝第一個專案（不再預設「全部」讓各專案混在一起）。
	if !strings.Contains(body, "state.filterProject = state.tree[0].id;") {
		t.Error("應預設選定第一個專案為工作範圍")
	}
}

// TestDashboardStatusChips：狀態統計列可點（點某狀態→下面清單只留該類），且最前面有「全部」。
func TestDashboardStatusChips(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()

	for _, want := range []string{
		"function statChip(status, label, num, withDot)",
		"function setStatusFilter(status)",
		`bar.appendChild(statChip("", "全部", total, false));`,
		`chip.setAttribute("data-status", status);`,
		".stat-chip",
		`chip.addEventListener("click", function () { setStatusFilter(status); });`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少狀態 chip 元素 %q", want)
		}
	}
}

// TestDashboardGitLinks：詳情的 commit/PR 依 project repo 變成可點連結（GIT-DASH）。
func TestDashboardGitLinks(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()
	for _, want := range []string{
		"function repoURLOf(projectNode)", "function linkURL(repo, l)",
		`projectNode.links[i].kind === "repo"`, `l.kind === "commit"`, `l.kind === "pr"`,
		`base + "/commit/" + l.target`, `base + "/pull/" + l.target`,
		"a.target = \"_blank\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少 git 連結元素 %q", want)
		}
	}
}

// TestDashboardRouting：SPA deep-link——每個點選同步到網址，載入／popstate 依網址還原。
func TestDashboardRouting(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/")
	body := rr.Body.String()
	for _, want := range []string{
		"function readURL()", "function currentPath()", "function pushURL(replace)",
		"history.pushState", "history.replaceState",
		`window.addEventListener("popstate"`, "function restoreView()", "state.suppressPush",
		"pushURL();",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard.html 缺少 routing 元素 %q", want)
		}
	}
}

// TestUnknownPathSPA：非 API 的未知路徑走 SPA fallback（回同一份單頁，deep-link 用）；
// /api/ 下的未知路徑仍 404。
func TestUnknownPathSPA(t *testing.T) {
	h := newTestHandler(t)

	// deep-link 路徑要回單頁 HTML。
	for _, p := range []string{"/nope", "/Y20260916", "/Y20260916/REQ-MEMBER/ISSUE-X"} {
		rr := doReq(h, http.MethodGet, p)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d", p, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s content-type = %q", p, ct)
		}
		if !strings.Contains(rr.Body.String(), "ProjectBoard") {
			t.Errorf("%s 應回 dashboard 單頁", p)
		}
	}

	// /api/ 未知路徑不吞成 HTML，維持 404。
	rr := doReq(h, http.MethodGet, "/api/nope")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/api/nope status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not found") {
		t.Fatalf("/api/nope body = %q", rr.Body.String())
	}
}

// TestDashboardAsset：內嵌靜態資源（品牌小圖示）由 /assets/ 服務；未知檔回 404（不吞成 HTML）。
func TestDashboardAsset(t *testing.T) {
	h := newTestHandler(t)
	rr := doReq(h, http.MethodGet, "/assets/avatar.png")
	if rr.Code != http.StatusOK {
		t.Fatalf("/assets/avatar.png status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	b := rr.Body.Bytes()
	if len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Errorf("回應不是 PNG（len=%d）", len(b))
	}
	if rr := doReq(h, http.MethodGet, "/assets/nope.png"); rr.Code != http.StatusNotFound {
		t.Errorf("未知 asset status = %d, want 404", rr.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{"/", "/healthz", "/api/tree", "/api/stats", "/api/search", "/api/deps", "/api/checklist", "/api/report", "/api/node/x"} {
		rr := doReq(h, http.MethodPost, target)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", target, rr.Code)
		}
		if allow := rr.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s: Allow = %q", target, allow)
		}
	}
}

func TestTree(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/tree?project=Y20260916")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var roots []fixtureNode
	if err := json.Unmarshal(rr.Body.Bytes(), &roots); err != nil {
		t.Fatalf("unmarshal tree: %v", err)
	}
	if len(roots) != 1 || roots[0].ID != "Y20260916" {
		t.Fatalf("roots = %+v", roots)
	}
	if len(roots[0].Children) == 0 {
		t.Fatal("root has no children")
	}
}

func TestStatsShape(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/stats?project=Y20260916")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var stats map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	for _, k := range []string{"count_by_status", "count_by_owner", "req_progress", "self_verified_count", "focus"} {
		if _, ok := stats[k]; !ok {
			t.Errorf("stats 缺少 %q", k)
		}
	}
	var focus map[string]json.RawMessage
	if err := json.Unmarshal(stats["focus"], &focus); err != nil {
		t.Fatalf("focus unmarshal: %v", err)
	}
	// 焦點面板五項。
	for _, k := range []string{"blocked", "awaiting_decision", "recent", "self_verified"} {
		if _, ok := focus[k]; !ok {
			t.Errorf("focus 缺少 %q", k)
		}
	}
}

// TestDepsAPI：fixture 的 /api/deps 由 nodes.json 的 depends_on links 推導（v0.2）。
func TestDepsAPI(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/deps")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var all []fixtureDep
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("unmarshal deps: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("fixture 應有至少一筆 depends_on")
	}
	for _, d := range all {
		if d.FromID == "" || d.Target == "" {
			t.Errorf("dep 欄位不齊: %+v", d)
		}
	}

	// project 過濾：命中前綴、非命中為空。
	rr = doReq(h, http.MethodGet, "/api/deps?project=Y20260916")
	var scoped []fixtureDep
	if err := json.Unmarshal(rr.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("unmarshal scoped: %v", err)
	}
	if len(scoped) != len(all) {
		t.Errorf("project 前綴應全命中: got %d, want %d", len(scoped), len(all))
	}
	rr = doReq(h, http.MethodGet, "/api/deps?project=Y00000000")
	var none []fixtureDep
	if err := json.Unmarshal(rr.Body.Bytes(), &none); err != nil {
		t.Fatalf("unmarshal none: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("無關 project 應為空，got %+v", none)
	}
}

func TestNodeContract(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/"+sampleNode)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var node struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Owner    string `json:"owner"`
		Priority string `json:"priority"`
		Tags     string `json:"tags"`
		Body     string `json:"body"`
		Created  string `json:"created_at"`
		Updated  string `json:"updated_at"`
		Links    []struct {
			Kind   string `json:"kind"`
			Target string `json:"target"`
		} `json:"links"`
		Children []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"children"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &node); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	if node.ID != sampleNode || node.Type != "issue" || node.Status != "done" ||
		node.Owner != "xiaoxia" || node.Priority != "high" || node.Tags != "admin-bff,ut90" {
		t.Fatalf("node = %+v", node)
	}
	if node.Created == "" || node.Updated == "" || node.Body == "" {
		t.Fatalf("node 缺 body/時間欄位: %+v", node)
	}
	if len(node.Links) != 1 || node.Links[0].Kind != "commit" || node.Links[0].Target != "8094064" {
		t.Fatalf("links = %+v", node.Links)
	}
	if len(node.Children) != 1 || node.Children[0].Type != "report" {
		t.Fatalf("children = %+v", node.Children)
	}
}

func TestNodeNotFound(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/no/such/node")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestNodeMissingID(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHistory(t *testing.T) {
	h := newTestHandler(t)
	rr := doReq(h, http.MethodGet, "/api/node/"+sampleNode+"/history")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var all []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("history len = %d, want 5", len(all))
	}
	if all[4]["action"] != "verify" {
		t.Fatalf("last action = %v", all[4]["action"])
	}

	rr = doReq(h, http.MethodGet, "/api/node/"+sampleNode+"/history?limit=2")
	var limited []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &limited); err != nil {
		t.Fatalf("unmarshal limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited len = %d, want 2", len(limited))
	}

	// 非法 limit 視為 0（不截斷）。
	rr = doReq(h, http.MethodGet, "/api/node/"+sampleNode+"/history?limit=abc")
	var bad []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &bad); err != nil {
		t.Fatalf("unmarshal bad-limit: %v", err)
	}
	if len(bad) != 5 {
		t.Fatalf("bad-limit len = %d, want 5", len(bad))
	}
}

func TestHistoryNotFound(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/node/no/such/history")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHistoryMissingID(t *testing.T) {
	// ServeMux 會把 `//` 正規化並 301，因此直接呼叫 handler 驗證缺 id 的 400 分支。
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/node//history", nil)
	rr := httptest.NewRecorder()
	h.handleNode(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestSearch(t *testing.T) {
	h := newTestHandler(t)

	rr := doReq(h, http.MethodGet, "/api/search?q=admin")
	var hits []searchHit
	if err := json.Unmarshal(rr.Body.Bytes(), &hits); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("q=admin 應有命中")
	}
	found := false
	for _, h := range hits {
		if h.ID == sampleNode {
			found = true
		}
	}
	if !found {
		t.Fatalf("q=admin 未命中 %s: %+v", sampleNode, hits)
	}

	rr = doReq(h, http.MethodGet, "/api/search")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("空查詢應回 [], got %q", got)
	}

	rr = doReq(h, http.MethodGet, "/api/search?q=arch&project=Y20260916/REQ-ARCH")
	if err := json.Unmarshal(rr.Body.Bytes(), &hits); err != nil {
		t.Fatalf("unmarshal project search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("project 過濾後應仍有命中")
	}
	for _, hit := range hits {
		if !strings.HasPrefix(hit.ID, "Y20260916/REQ-ARCH") {
			t.Errorf("project 過濾失效: %s", hit.ID)
		}
	}

	rr = doReq(h, http.MethodGet, "/api/search?q=zzzznope")
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("無命中應回 [], got %q", got)
	}
}

// stubSource 用來驗證 handler 對資料來源錯誤的分流（404 vs 500）。
type stubSource struct{ err error }

func (s stubSource) Health() (json.RawMessage, error)         { return nil, s.err }
func (s stubSource) Tree(url.Values) (json.RawMessage, error) { return nil, s.err }
func (s stubSource) Node(string) (json.RawMessage, error)     { return nil, s.err }
func (s stubSource) History(string, int) (json.RawMessage, error) {
	return nil, s.err
}
func (s stubSource) Stats(url.Values) (json.RawMessage, error)  { return nil, s.err }
func (s stubSource) Search(url.Values) (json.RawMessage, error) { return nil, s.err }
func (s stubSource) Deps(url.Values) (json.RawMessage, error)   { return nil, s.err }
func (s stubSource) Checklist(url.Values) (json.RawMessage, error) {
	return nil, s.err
}
func (s stubSource) Report(url.Values) (json.RawMessage, error) { return nil, s.err }
func (s stubSource) Meta() (json.RawMessage, error)             { return nil, s.err }

func TestSourceErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"generic", errors.New("boom"), http.StatusInternalServerError},
		{"notfound", ErrNotFound, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(stubSource{err: tc.err})
			for _, target := range []string{"/healthz", "/api/tree", "/api/stats", "/api/search", "/api/deps", "/api/checklist", "/api/report", "/api/meta", "/api/node/x", "/api/node/x/history"} {
				rr := doReq(h, http.MethodGet, target)
				if rr.Code != tc.want {
					t.Errorf("%s: status = %d, want %d", target, rr.Code, tc.want)
				}
			}
		})
	}
}

func TestNewFixtureSourceBadFS(t *testing.T) {
	validTree := []byte(`[{"id":"Y1","type":"project","title":"t","status":"todo","owner":"human","priority":"medium","children":[]}]`)
	validObj := []byte(`{"k":{"x":1}}`)
	validStats := []byte(`{}`)

	cases := []struct {
		name  string
		files fstest.MapFS
	}{
		{"missing tree", fstest.MapFS{}},
		{"invalid tree json", fstest.MapFS{"fixtures/tree.json": {Data: []byte("{bad")}}},
		{"invalid stats json", fstest.MapFS{
			"fixtures/tree.json":  {Data: validTree},
			"fixtures/stats.json": {Data: []byte("{bad")},
		}},
		{"nodes not object", fstest.MapFS{
			"fixtures/tree.json":  {Data: validTree},
			"fixtures/stats.json": {Data: validStats},
			"fixtures/nodes.json": {Data: []byte("[]")},
		}},
		{"history not object", fstest.MapFS{
			"fixtures/tree.json":    {Data: validTree},
			"fixtures/stats.json":   {Data: validStats},
			"fixtures/nodes.json":   {Data: validObj},
			"fixtures/history.json": {Data: []byte("[]")},
		}},
		{"tree wrong shape", fstest.MapFS{
			"fixtures/tree.json":    {Data: []byte(`{"not":"array"}`)},
			"fixtures/stats.json":   {Data: validStats},
			"fixtures/nodes.json":   {Data: validObj},
			"fixtures/history.json": {Data: validObj},
		}},
		{"invalid report json", fstest.MapFS{
			"fixtures/tree.json":    {Data: validTree},
			"fixtures/stats.json":   {Data: validStats},
			"fixtures/nodes.json":   {Data: validObj},
			"fixtures/history.json": {Data: validObj},
			"fixtures/report.json":  {Data: []byte("{bad")},
		}},
		{"checklist not array", fstest.MapFS{
			"fixtures/tree.json":      {Data: validTree},
			"fixtures/stats.json":     {Data: validStats},
			"fixtures/nodes.json":     {Data: validObj},
			"fixtures/history.json":   {Data: validObj},
			"fixtures/report.json":    {Data: []byte("{}")},
			"fixtures/checklist.json": {Data: []byte(`{"not":"array"}`)},
		}},
		{"invalid meta json", fstest.MapFS{
			"fixtures/tree.json":      {Data: validTree},
			"fixtures/stats.json":     {Data: validStats},
			"fixtures/nodes.json":     {Data: validObj},
			"fixtures/history.json":   {Data: validObj},
			"fixtures/report.json":    {Data: []byte("{}")},
			"fixtures/checklist.json": {Data: []byte("[]")},
			"fixtures/meta.json":      {Data: []byte("{bad")},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newFixtureSource(tc.files); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestReadFixtureHelpers(t *testing.T) {
	fsys := fstest.MapFS{"a.json": {Data: []byte(`{"x":1}`)}}
	if _, err := readFixture(fsys, "a.json"); err != nil {
		t.Fatalf("readFixture: %v", err)
	}
	if _, err := readFixture(fsys, "missing.json"); err == nil {
		t.Fatal("missing file should error")
	}
	if _, err := readFixtureMap(fsys, "a.json"); err != nil {
		t.Fatalf("readFixtureMap: %v", err)
	}
	if _, err := parseTree([]byte("{bad")); err == nil {
		t.Fatal("parseTree bad json should error")
	}
	if _, err := buildIndex([]byte("{bad")); err == nil {
		t.Fatal("buildIndex bad json should error")
	}
}

func TestBuildIndexSkipsNil(t *testing.T) {
	hits, err := buildIndex([]byte(`[{"id":"a","type":"project","children":[null,{"id":"b","type":"req"}]}]`))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestHistoryUnmarshalError(t *testing.T) {
	src := &FixtureSource{history: map[string]json.RawMessage{"x": json.RawMessage(`{"not":"an array"}`)}}
	if _, err := src.History("x", 3); err == nil {
		t.Fatal("want unmarshal error")
	}
}

func TestFixtureSourceDirectMethods(t *testing.T) {
	src, err := NewFixtureSource()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Tree(nil); err != nil {
		t.Errorf("Tree: %v", err)
	}
	if _, err := src.Health(); err != nil {
		t.Errorf("Health: %v", err)
	}
	if _, err := src.Stats(nil); err != nil {
		t.Errorf("Stats: %v", err)
	}
	if _, err := src.Node(sampleNode); err != nil {
		t.Errorf("Node: %v", err)
	}
	if _, err := src.History(sampleNode, 0); err != nil {
		t.Errorf("History: %v", err)
	}
	if _, err := src.Search(nil); err != nil {
		t.Errorf("Search: %v", err)
	}
	if _, err := src.Deps(nil); err != nil {
		t.Errorf("Deps: %v", err)
	}
	if _, err := src.Checklist(nil); err != nil {
		t.Errorf("Checklist: %v", err)
	}
	if _, err := src.Report(nil); err != nil {
		t.Errorf("Report: %v", err)
	}
	if _, err := src.Meta(); err != nil {
		t.Errorf("Meta: %v", err)
	}
}

// TestMetaAPI：fixture 的 /api/meta（v0.5）；7 種 type（依 sort）＋owner 名冊。
func TestMetaAPI(t *testing.T) {
	rr := doReq(newTestHandler(t), http.MethodGet, "/api/meta")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var m struct {
		Types []struct {
			Key        string `json:"key"`
			Label      string `json:"label"`
			IDPrefix   string `json:"id_prefix"`
			ParentType string `json:"parent_type"`
			Sort       int    `json:"sort"`
		} `json:"types"`
		Owners   []string `json:"owners"`
		Statuses []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Icon  string `json:"icon"`
			Color string `json:"color"`
			Sort  int    `json:"sort"`
		} `json:"statuses"`
		Tabs []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Sort  int    `json:"sort"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Types) != 7 {
		t.Fatalf("types = %+v", m.Types)
	}
	for i, d := range m.Types {
		if d.Sort != i+1 {
			t.Fatalf("types 未依 sort 排序: %+v", m.Types)
		}
	}
	if m.Types[5].Key != "bug" || m.Types[6].Key != "plan" {
		t.Fatalf("bug/plan 缺漏: %+v", m.Types)
	}
	if len(m.Owners) != 7 {
		t.Fatalf("owners = %+v", m.Owners)
	}
	// statuses：7 筆顯示 metadata（V05-STATUS-META-API），依 sort、值與舊 var STATUS 一致。
	if len(m.Statuses) != 7 {
		t.Fatalf("statuses = %+v", m.Statuses)
	}
	for i, d := range m.Statuses {
		if d.Sort != i+1 {
			t.Fatalf("statuses 未依 sort 排序: %+v", m.Statuses)
		}
	}
	if s := m.Statuses[0]; s.Key != "todo" || s.Icon != "○" || s.Color != "#5f6368" {
		t.Fatalf("statuses[0] = %+v", s)
	}
	if s := m.Statuses[6]; s.Key != "cancel" || s.Label != "不做" {
		t.Fatalf("statuses[6] = %+v", s)
	}
	// tabs：4 個 dashboard 分頁（V05-STATUS-META-API 追加），依 sort、值與舊 var TAB_LABEL 一致。
	if len(m.Tabs) != 4 {
		t.Fatalf("tabs = %+v", m.Tabs)
	}
	for i, d := range m.Tabs {
		if d.Sort != i+1 {
			t.Fatalf("tabs 未依 sort 排序: %+v", m.Tabs)
		}
	}
	if t0 := m.Tabs[0]; t0.Key != "focus" || t0.Label != "焦點" {
		t.Fatalf("tabs[0] = %+v", t0)
	}
	if t3 := m.Tabs[3]; t3.Key != "weekly" || t3.Label != "週報" {
		t.Fatalf("tabs[3] = %+v", t3)
	}
}

// TestFixtureConsistency 確認 fixture 內部一致（樹 vs stats vs nodes vs history），
// 避免 phase 2 換真資料時才發現對不上。
func TestFixtureConsistency(t *testing.T) {
	src, err := NewFixtureSource()
	if err != nil {
		t.Fatal(err)
	}
	roots, err := parseTree(src.tree)
	if err != nil {
		t.Fatal(err)
	}

	statusCount := map[string]int{}
	openOwnerCount := map[string]int{} // §5.3：CountByOwner＝目前未結案（非 done/cancel）
	inTree := map[string]bool{}
	var walk func([]*fixtureNode)
	walk = func(nodes []*fixtureNode) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			inTree[n.ID] = true
			statusCount[n.Status]++
			if n.Status != "done" && n.Status != "cancel" {
				openOwnerCount[n.Owner]++
			}
			walk(n.Children)
		}
	}
	walk(roots)

	var stats struct {
		CountByStatus     map[string]int `json:"count_by_status"`
		CountByOwner      map[string]int `json:"count_by_owner"`
		SelfVerifiedCount int            `json:"self_verified_count"`
		Focus             struct {
			Blocked []struct {
				ID string `json:"id"`
			} `json:"blocked"`
			AwaitingDecision []struct {
				ID string `json:"id"`
			} `json:"awaiting_decision"`
			Recent []struct {
				NodeID string `json:"node_id"`
			} `json:"recent"`
			SelfVerified []struct {
				ID string `json:"id"`
			} `json:"self_verified"`
		} `json:"focus"`
	}
	if err := json.Unmarshal(src.stats, &stats); err != nil {
		t.Fatalf("stats: %v", err)
	}

	for status, n := range statusCount {
		if stats.CountByStatus[status] != n {
			t.Errorf("count_by_status[%s] = %d, tree 有 %d", status, stats.CountByStatus[status], n)
		}
	}
	for owner, n := range openOwnerCount {
		if stats.CountByOwner[owner] != n {
			t.Errorf("count_by_owner[%s] = %d, 未結案有 %d", owner, stats.CountByOwner[owner], n)
		}
	}
	if stats.SelfVerifiedCount != len(stats.Focus.SelfVerified) {
		t.Errorf("self_verified_count = %d, focus.self_verified = %d", stats.SelfVerifiedCount, len(stats.Focus.SelfVerified))
	}
	for _, x := range stats.Focus.Blocked {
		if !inTree[x.ID] {
			t.Errorf("focus.blocked 指向不存在的節點 %s", x.ID)
		}
	}
	for _, x := range stats.Focus.AwaitingDecision {
		if !inTree[x.ID] {
			t.Errorf("focus.awaiting_decision 指向不存在的節點 %s", x.ID)
		}
	}
	for _, x := range stats.Focus.Recent {
		if !inTree[x.NodeID] {
			t.Errorf("focus.recent 指向不存在的節點 %s", x.NodeID)
		}
	}
	for _, x := range stats.Focus.SelfVerified {
		if !inTree[x.ID] {
			t.Errorf("focus.self_verified 指向不存在的節點 %s", x.ID)
		}
	}

	// nodes.json 每筆都要在樹上，且欄位齊全。
	for id, raw := range src.nodes {
		if !inTree[id] {
			t.Errorf("nodes.json 的 %s 不在樹上", id)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("node %s: %v", id, err)
		}
		for _, k := range []string{"id", "type", "title", "status", "owner", "priority", "tags", "body", "created_at", "updated_at", "links", "children"} {
			if _, ok := m[k]; !ok {
				t.Errorf("node %s 缺少欄位 %q", id, k)
			}
		}
	}
}
