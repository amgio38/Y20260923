package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"project_board/internal/domain"
	"project_board/internal/store"

	_ "modernc.org/sqlite"
)

// ---------- 測試骨架 ----------

// testApp：把輸出與 env 注入 CLI，DB 指向暫存檔，指令可反覆驅動。
type testApp struct {
	a      *app
	out    *bytes.Buffer
	errOut *bytes.Buffer
	vars   map[string]string
	dbPath string
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	vars := map[string]string{"PB_ACTOR": "human"}
	ta := &testApp{out: out, errOut: errOut, vars: vars, dbPath: filepath.Join(t.TempDir(), "board.db")}
	vars["PB_DB"] = ta.dbPath
	ta.a = &app{
		stdout: out,
		stderr: errOut,
		getenv: func(k string) string { return vars[k] },
	}
	return ta
}

func (ta *testApp) run(args ...string) int {
	ta.out.Reset()
	ta.errOut.Reset()
	return ta.a.dispatch(args)
}

func (ta *testApp) stdout() string { return ta.out.String() }
func (ta *testApp) stderr() string { return ta.errOut.String() }

// mustRun：跑指令並要求指定結束碼（失敗時把 stdout／stderr 一起印出來）。
func (ta *testApp) mustRun(t *testing.T, wantCode int, args ...string) string {
	t.Helper()
	code := ta.run(args...)
	if code != wantCode {
		t.Fatalf("%v → 結束碼 %d，want %d\nstdout=%s\nstderr=%s", args, code, wantCode, ta.stdout(), ta.stderr())
	}
	return ta.stdout()
}

const (
	fxProject = "Y20260916"
	fxReq     = "Y20260916/REQ-ALPHA"
	fxIssue   = "Y20260916/REQ-ALPHA/ISSUE-ONE"
)

// setupTree：init＋seed＋一棵 project→req→issue。
func (ta *testApp) setupTree(t *testing.T) {
	t.Helper()
	ta.mustRun(t, exitOK, "init")
	ta.mustRun(t, exitOK, "seed")
	ta.mustRun(t, exitOK, "create", "--type", "req", "--title", "Alpha", "--parent", fxProject, "--id", fxReq)
	ta.mustRun(t, exitOK, "create", "--type", "issue", "--title", "Issue one", "--parent", fxReq, "--id", fxIssue)
}

// setUpdatedAtRaw：直接改 DB 的 updated_at，讓樂觀鎖測試不依賴牆鐘精度。
func (ta *testApp) setUpdatedAtRaw(t *testing.T, id, ts string) {
	t.Helper()
	ta.execRaw(t, "UPDATE nodes SET updated_at = ? WHERE id = ?", ts, id)
}

// setCreatedAtRaw：直接改 DB 的 created_at（平均滯留天數的測試用；不 sleep）。
func (ta *testApp) setCreatedAtRaw(t *testing.T, id string, ts time.Time) {
	t.Helper()
	ta.execRaw(t, "UPDATE nodes SET created_at = ? WHERE id = ?", ts.Format(time.RFC3339), id)
}

func (ta *testApp) execRaw(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+ta.dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", ta.dbPath, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execRaw: %v", err)
	}
}

func decodeJSON[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("JSON 解析失敗: %v\n%s", err, s)
	}
	return v
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// ---------- dispatch / usage ----------

func TestDispatchUsage(t *testing.T) {
	ta := newTestApp(t)
	if code := ta.run("help"); code != exitOK {
		t.Fatalf("help 結束碼 = %d", code)
	}
	if !strings.Contains(ta.stdout(), "pb <子命令>") {
		t.Error("help 應印用法")
	}
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		if code := ta.run(args...); code != exitOK {
			t.Errorf("%v 結束碼 = %d, want 0", args, code)
		}
	}
	// 沒有子命令 → 用法錯誤
	if code := ta.run(); code != exitUsage {
		t.Errorf("空參數結束碼 = %d, want %d", code, exitUsage)
	}
	// 未知子命令
	code := ta.run("frobnicate")
	if code != exitUsage || !strings.Contains(ta.stderr(), "未知子命令") {
		t.Errorf("未知子命令 code=%d stderr=%s", code, ta.stderr())
	}
}

// ---------- init / seed ----------

func TestInitAndSeed(t *testing.T) {
	ta := newTestApp(t)
	out := ta.mustRun(t, exitOK, "init")
	if !strings.Contains(out, "schema v6") { // 版本號跟著 migration 數量走（v0.5 加 0006_node_types 後為 v6）
		t.Errorf("init 輸出 = %q, want 含 schema v6", out)
	}
	if _, err := filepath.Glob(ta.dbPath); err != nil {
		t.Fatal(err)
	}
	// 可重複跑
	ta.mustRun(t, exitOK, "init")
	ta.mustRun(t, exitOK, "seed")
	out = ta.mustRun(t, exitOK, "seed")
	if !strings.Contains(out, "種子專案 "+fxProject) || !strings.Contains(out, "in_progress") {
		t.Errorf("seed 輸出 = %q", out)
	}
}

func TestInitCreatesParentDir(t *testing.T) {
	ta := newTestApp(t)
	nested := filepath.Join(filepath.Dir(ta.dbPath), "var", "board.db")
	ta.vars["PB_DB"] = nested
	ta.mustRun(t, exitOK, "init")
	if _, err := filepath.Glob(nested); err != nil {
		t.Fatal(err)
	}
}

func TestInitBadPath(t *testing.T) {
	ta := newTestApp(t)
	blocker := filepath.Join(filepath.Dir(ta.dbPath), "blocker")
	if err := writeFile(blocker, "x"); err != nil {
		t.Fatal(err)
	}
	ta.vars["PB_DB"] = filepath.Join(blocker, "board.db")
	if code := ta.run("init"); code != exitErr {
		t.Fatalf("code = %d, want %d（%s）", code, exitErr, ta.stderr())
	}
	if !strings.Contains(ta.stderr(), "建立目錄") {
		t.Errorf("stderr = %q", ta.stderr())
	}
}

func TestSeedNeedsActor(t *testing.T) {
	ta := newTestApp(t)
	delete(ta.vars, "PB_ACTOR")
	code := ta.run("seed")
	if code != exitUsage {
		t.Fatalf("code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(ta.stderr(), "PB_ACTOR") {
		t.Errorf("stderr = %q, want 提示 PB_ACTOR", ta.stderr())
	}
	// --actor 優先於 env
	ta.vars["PB_ACTOR"] = "nobody"
	ta.mustRun(t, exitOK, "init")
	if code := ta.run("seed", "--actor", "claude"); code != exitOK {
		t.Fatalf("--actor 應蓋過 env: %d %s", code, ta.stderr())
	}
}

func TestWriteNeedsActorEverywhere(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	delete(ta.vars, "PB_ACTOR")
	writes := [][]string{
		{"seed"},
		{"create", "--type", "req", "--title", "t", "--parent", fxProject},
		{"update", fxIssue, "--title", "x"},
		{"move", fxIssue, "in_progress"},
		{"assign", fxIssue, "xiaoxia"},
		{"link", fxIssue, "--kind", "commit", "--target", "abc"},
		{"verify", fxIssue, "--note", "n"},
		{"comment", fxIssue, "hi"},
	}
	for _, args := range writes {
		code := ta.run(args...)
		if code != exitUsage {
			t.Errorf("%v → code=%d, want %d（%s）", args, code, exitUsage, ta.stderr())
		}
		if !strings.Contains(ta.stderr(), "缺少 actor") {
			t.Errorf("%v stderr=%q", args, ta.stderr())
		}
	}
}

// ---------- 讀取類 ----------

func TestTreeCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)

	out := ta.mustRun(t, exitOK, "tree")
	if !strings.Contains(out, fxProject) || !strings.Contains(out, fxIssue) || !strings.Contains(out, "└─") {
		t.Errorf("tree 輸出 = %q", out)
	}
	// 過濾：status=todo 帶上祖先
	if out := ta.mustRun(t, exitOK, "tree", "--status", "todo"); !strings.Contains(out, fxProject) {
		t.Errorf("tree --status 應帶祖先: %q", out)
	}
	// 無命中
	if out := ta.mustRun(t, exitOK, "tree", "--owner", "yilong"); !strings.Contains(out, "沒有符合的節點") {
		t.Errorf("tree --owner 無命中輸出 = %q", out)
	}
	// 未知狀態 → 用法錯誤
	if code := ta.run("tree", "--status", "bogus"); code != exitUsage {
		t.Errorf("code = %d, want %d", code, exitUsage)
	}
	// 不存在的專案 → 執行錯誤
	if code := ta.run("tree", "--project", "Y20990101"); code != exitErr {
		t.Errorf("code = %d, want %d", code, exitErr)
	}
	// JSON
	nodes := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "tree", "--json"))
	if len(nodes) != 3 { // seed 的 project ＋ REQ ＋ ISSUE
		t.Fatalf("tree --json = %d 顆, want 3", len(nodes))
	}
	if nodes[0]["id"] != fxProject {
		t.Errorf("第一顆 = %v, want %s", nodes[0]["id"], fxProject)
	}
}

func TestGetCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "link", fxIssue, "--kind", "commit", "--target", "8094064")
	out := ta.mustRun(t, exitOK, "get", fxIssue)
	for _, want := range []string{"ID       : " + fxIssue, "Type     : issue", "commit → 8094064", statusIcon[domain.StatusTodo]} {
		if !strings.Contains(out, want) {
			t.Errorf("get 輸出缺 %q\n%s", want, out)
		}
	}
	detail := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "get", fxIssue, "--json"))
	if detail["id"] != fxIssue || detail["status"] != "todo" {
		t.Errorf("get --json = %v", detail)
	}
	if links, ok := detail["links"].([]any); !ok || len(links) != 1 {
		t.Errorf("links = %v", detail["links"])
	}
	if code := ta.run("get"); code != exitUsage {
		t.Errorf("缺 id code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("get", "Y20990101/NOPE"); code != exitErr {
		t.Errorf("不存在 code = %d, want %d", code, exitErr)
	}
	if !strings.Contains(ta.stderr(), "找不到節點") {
		t.Errorf("stderr = %q", ta.stderr())
	}
}

func TestSearchCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "search", "Issue")
	if !strings.Contains(out, fxIssue) {
		t.Errorf("search 輸出 = %q", out)
	}
	if out := ta.mustRun(t, exitOK, "search", "zzz-nothing"); !strings.Contains(out, "沒有命中") {
		t.Errorf("無命中輸出 = %q", out)
	}
	got := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "search", "Issue", "--json"))
	if len(got) != 1 {
		t.Fatalf("search --json = %v", got)
	}
	if code := ta.run("search"); code != exitUsage {
		t.Errorf("缺 query code = %d", code)
	}
}

// TestSearchCommandTagOwner：v0.2 的 --tag（整段相符）與 --owner（全等）；query 可省略。
func TestSearchCommandTagOwner(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "update", fxIssue, "--tags", "v0.2,ui")
	ta.mustRun(t, exitOK, "assign", fxIssue, "xiaoxia")

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"tag 整段相符", []string{"--tag", "v0.2"}, true},
		{"tag 非子字串", []string{"--tag", "v0.21"}, false},
		{"tag 多段之一", []string{"--tag", "ui"}, true},
		{"owner 全等", []string{"--owner", "xiaoxia"}, true},
		{"owner 沒這個人", []string{"--owner", "kaimadi"}, false},
		{"query＋tag", []string{"Issue", "--tag", "v0.2"}, true},
		{"query＋tag 衝突", []string{"Issue", "--tag", "login"}, false},
		{"query＋owner", []string{"Issue", "--owner", "xiaoxia"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ta.mustRun(t, exitOK, append([]string{"search"}, tc.args...)...)
			if hit := strings.Contains(out, fxIssue); hit != tc.want {
				t.Errorf("search %v = %q, want 命中=%v", tc.args, out, tc.want)
			}
		})
	}

	// query 空但有 tag／owner 也要能查（REQ：query／tag／owner／project 至少一個）
	rows := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "search", "--tag", "ui", "--json"))
	if len(rows) != 1 || rows[0]["id"] != fxIssue {
		t.Fatalf("search --tag --json = %v", rows)
	}
	// 四個條件全空＝用法錯誤
	if code := ta.run("search", "--project", ""); code != exitUsage {
		t.Errorf("全空 code = %d, want %d", code, exitUsage)
	}
	// 兩個位置參數＝用法錯誤
	if code := ta.run("search", "a", "b"); code != exitUsage {
		t.Errorf("多餘位置參數 code = %d, want %d", code, exitUsage)
	}
}

func TestDepsCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "link", fxIssue, "--kind", "depends_on", "--target", fxProject, "--note", "等專案")
	ta.mustRun(t, exitOK, "link", fxIssue, "--kind", "commit", "--target", "8094064")

	out := ta.mustRun(t, exitOK, "deps")
	if !strings.Contains(out, fxIssue+" → "+fxProject) || !strings.Contains(out, "等專案") {
		t.Errorf("deps 輸出 = %q", out)
	}
	if strings.Contains(out, "8094064") {
		t.Errorf("deps 只該列 depends_on：%q", out)
	}
	deps := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "deps", "--json"))
	if len(deps) != 1 || deps[0]["from_id"] != fxIssue || deps[0]["target"] != fxProject {
		t.Fatalf("deps --json = %v", deps)
	}
	if out := ta.mustRun(t, exitOK, "deps", "--project", "Y20999999"); !strings.Contains(out, "沒有依賴") {
		t.Errorf("別專案 deps = %q", out)
	}
	if out := ta.mustRun(t, exitOK, "deps", "--project", fxProject); !strings.Contains(out, fxIssue) {
		t.Errorf("本專案 deps = %q", out)
	}
	if code := ta.run("deps", "extra"); code != exitUsage {
		t.Errorf("多餘參數 code = %d, want %d", code, exitUsage)
	}
}

func TestHistoryCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "comment", fxIssue, "先做 UT90")
	out := ta.mustRun(t, exitOK, "history", fxIssue)
	if !strings.Contains(out, "comment") || !strings.Contains(out, "先做 UT90") {
		t.Errorf("history 輸出 = %q", out)
	}
	hs := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "history", fxIssue, "--json", "--limit", "1"))
	if len(hs) != 1 || hs[0]["action"] != "comment" {
		t.Fatalf("history --json = %v", hs)
	}
	if code := ta.run("history", "Y20990101/NOPE"); code != exitErr {
		t.Errorf("不存在 code = %d", code)
	}
	if code := ta.run("history"); code != exitUsage {
		t.Errorf("缺 id code = %d", code)
	}
}

func TestStatsCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "stats")
	for _, want := range []string{"狀態計數：", "每人手上張數（未結案）：", "REQ 完成度：", "自我驗收張數："} {
		if !strings.Contains(out, want) {
			t.Errorf("stats 輸出缺 %q\n%s", want, out)
		}
	}
	st := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "stats", "--json"))
	if _, ok := st["count_by_status"]; !ok {
		t.Errorf("stats --json = %v", st)
	}
	if st["self_verified_count"].(float64) != 0 {
		t.Errorf("self_verified_count = %v, want 0", st["self_verified_count"])
	}
	// 有 REQ 進度（fixture 的 REQ 之下有 1 顆 issue）
	prog, ok := st["req_progress"].(map[string]any)
	if !ok || len(prog) != 1 {
		t.Fatalf("req_progress = %v", st["req_progress"])
	}
}

// TestStatsAvgDwellDaysCommand：pb stats 要印／吐 avg_dwell_days（REQ-V03-PROGRESS）。
func TestStatsAvgDwellDaysCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.setCreatedAtRaw(t, fxIssue, time.Now().Add(-48*time.Hour))
	ta.setCreatedAtRaw(t, fxReq, time.Now().Add(-48*time.Hour))

	out := ta.mustRun(t, exitOK, "stats")
	if !strings.Contains(out, "平均滯留天數（未結案）：") {
		t.Errorf("stats 輸出缺平均滯留天數區塊\n%s", out)
	}
	if !strings.Contains(out, "unassigned   2.0 天") {
		t.Errorf("平均滯留天數 = %q, want unassigned 2.0 天\n%s", lineWith(out, "unassigned"), out)
	}
	st := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "stats", "--json"))
	dwell, ok := st["avg_dwell_days"].(map[string]any)
	if !ok {
		t.Fatalf("stats --json 缺 avg_dwell_days：%v", st)
	}
	if got, ok := dwell["unassigned"].(float64); !ok || got < 1.9 || got > 2.1 {
		t.Errorf("avg_dwell_days[unassigned] = %v, want ~2.0", dwell["unassigned"])
	}
	if _, ok := dwell["kaimake"]; ok {
		t.Errorf("沒有未結案單的 owner 不該出現：%v", dwell)
	}
}

// lineWith：把含 substr 的那一行抓出來（錯誤訊息用）。
func lineWith(s, substr string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, substr) {
			return l
		}
	}
	return "(無)"
}

// ---------- 寫入類 ----------

func TestCreateCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.mustRun(t, exitOK, "init")
	ta.mustRun(t, exitOK, "seed")
	out := ta.mustRun(t, exitOK, "create", "--type", "req", "--title", "Member Core", "--parent", fxProject,
		"--owner", "xiaoxia", "--priority", "high", "--tags", "a,b", "--body", "正文")
	if !strings.Contains(out, fxProject+"/REQ-MEMBER-CORE") || !strings.Contains(out, "owner=xiaoxia") {
		t.Errorf("create 輸出 = %q", out)
	}
	// --json
	n := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "create", "--type", "req", "--title", "Other",
		"--parent", fxProject, "--json"))
	if n["type"] != "req" || n["status"] != "todo" || n["priority"] != "medium" {
		t.Errorf("create --json = %v", n)
	}
	// 撞名（不自動加尾碼）
	code := ta.run("create", "--type", "req", "--title", "Member Core", "--parent", fxProject)
	if code != exitErr || !strings.Contains(ta.stderr(), "ID 已存在") {
		t.Errorf("撞名 code=%d stderr=%q", code, ta.stderr())
	}
	// 缺必填
	for _, args := range [][]string{
		{"create", "--title", "x"},
		{"create", "--type", "req"},
	} {
		if code := ta.run(args...); code != exitUsage {
			t.Errorf("%v code = %d, want %d", args, code, exitUsage)
		}
	}
	// ID 格式不合
	if code := ta.run("create", "--type", "project", "--id", "BAD", "--title", "t"); code != exitErr {
		t.Errorf("壞 id code = %d", code)
	}
	if !strings.Contains(ta.stderr(), "ID 格式不合") {
		t.Errorf("stderr = %q", ta.stderr())
	}
}

func TestUpdateCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "update", fxIssue, "--title", "改標題", "--body", "b", "--priority", "high",
		"--owner", "kaimake", "--tags", "x", "--sort", "3")
	if !strings.Contains(out, "已更新 "+fxIssue) {
		t.Errorf("update 輸出 = %q", out)
	}
	n := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "get", fxIssue, "--json"))
	if n["title"] != "改標題" || n["owner"] != "kaimake" || n["priority"] != "high" || n["sort"].(float64) != 3 {
		t.Errorf("更新後 = %v", n)
	}
	// 沒帶任何欄位
	if code := ta.run("update", fxIssue); code != exitUsage {
		t.Errorf("沒欄位 code = %d, want %d", code, exitUsage)
	}
	// 缺 id
	if code := ta.run("update", "--title", "x"); code != exitUsage {
		t.Errorf("缺 id code = %d", code)
	}
	// 壞時間格式
	if code := ta.run("update", fxIssue, "--title", "x", "--if-unmodified-since", "昨天"); code != exitUsage {
		t.Errorf("壞 ts code = %d, want %d（%s）", code, exitUsage, ta.stderr())
	}
	if !strings.Contains(ta.stderr(), "RFC3339") {
		t.Errorf("stderr = %q", ta.stderr())
	}
	// 樂觀鎖：舊版本 → 衝突
	old := "2020-01-01T00:00:00+08:00"
	ta.setUpdatedAtRaw(t, fxIssue, old)
	code := ta.run("update", fxIssue, "--title", "x2", "--if-unmodified-since", old)
	if code != exitOK {
		t.Fatalf("帶正確版本應通過: %d %s", code, ta.stderr())
	}
	code = ta.run("update", fxIssue, "--title", "x3", "--if-unmodified-since", old)
	if code != exitErr || !strings.Contains(ta.stderr(), "已被異動") {
		t.Errorf("衝突 code=%d stderr=%q", code, ta.stderr())
	}
}

func TestMoveCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "move", fxIssue, "in_progress")
	if !strings.Contains(out, "已流轉 "+fxIssue+" → "+statusIcon[domain.StatusInProgress]) {
		t.Errorf("move 輸出 = %q", out)
	}
	// 非法轉移
	code := ta.run("move", fxIssue, "done")
	if code != exitErr || !strings.Contains(ta.stderr(), "狀態機不允許") {
		t.Errorf("非法轉移 code=%d stderr=%q", code, ta.stderr())
	}
	// blocked 要理由
	ta.mustRun(t, exitOK, "move", fxIssue, "blocked", "--note", "等 CONC01")
	// blocked 沒理由（先做一顆）
	ta.mustRun(t, exitOK, "create", "--type", "issue", "--title", "Two", "--parent", fxReq, "--id", fxReq+"/ISSUE-TWO")
	code = ta.run("move", fxReq+"/ISSUE-TWO", "blocked")
	if code != exitErr || !strings.Contains(ta.stderr(), "blocked 要附理由") {
		t.Errorf("blocked 無理由 code=%d stderr=%q", code, ta.stderr())
	}
	// 未知狀態名
	if code := ta.run("move", fxIssue, "bogus"); code != exitUsage {
		t.Errorf("未知狀態 code = %d", code)
	}
	// 參數個數
	if code := ta.run("move", fxIssue); code != exitUsage {
		t.Errorf("缺狀態 code = %d", code)
	}
	// JSON
	n := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "move", fxIssue, "in_progress", "--json"))
	if n["status"] != "in_progress" {
		t.Errorf("move --json = %v", n)
	}
	// 樂觀鎖衝突
	ta.setUpdatedAtRaw(t, fxIssue, "2020-01-01T00:00:00+08:00")
	code = ta.run("move", fxIssue, "review", "--if-unmodified-since", "2021-01-01T00:00:00+08:00")
	if code != exitErr || !strings.Contains(ta.stderr(), "已被異動") {
		t.Errorf("move 衝突 code=%d stderr=%q", code, ta.stderr())
	}
	// reopen 要 note：先推到 done
	old := "2020-01-01T00:00:00+08:00"
	ta.mustRun(t, exitOK, "move", fxIssue, "review", "--if-unmodified-since", old)
	ta.mustRun(t, exitOK, "verify", fxIssue, "--note", "證據")
	code = ta.run("move", fxIssue, "in_progress")
	if code != exitErr || !strings.Contains(ta.stderr(), "reopen") {
		t.Errorf("reopen 無 note code=%d stderr=%q", code, ta.stderr())
	}
	ta.mustRun(t, exitOK, "move", fxIssue, "in_progress", "--note", "補測")
}

func TestAssignCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "assign", fxIssue, "xiaoxia")
	if !strings.Contains(out, "owner=xiaoxia") {
		t.Errorf("assign 輸出 = %q", out)
	}
	code := ta.run("assign", fxIssue, "nobody")
	if code != exitErr || !strings.Contains(ta.stderr(), "不在名冊內") {
		t.Errorf("不在名冊 code=%d stderr=%q", code, ta.stderr())
	}
	if code := ta.run("assign", fxIssue); code != exitUsage {
		t.Errorf("缺 owner code = %d", code)
	}
	if code := ta.run("assign", "Y20990101/NOPE", "xiaoxia"); code != exitErr {
		t.Errorf("不存在 code = %d", code)
	}
}

func TestLinkCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "link", fxIssue, "--kind", "commit", "--target", "8094064", "--note", "收單")
	if !strings.Contains(out, "已關聯 "+fxIssue) || !strings.Contains(out, "commit → 8094064") {
		t.Errorf("link 輸出 = %q", out)
	}
	// depends_on 指到不存在的節點
	code := ta.run("link", fxIssue, "--kind", "depends_on", "--target", "Y20990101/NOPE")
	if code != exitErr || !strings.Contains(ta.stderr(), "目標節點不存在") {
		t.Errorf("depends_on 壞目標 code=%d stderr=%q", code, ta.stderr())
	}
	// depends_on 指到存在節點
	ta.mustRun(t, exitOK, "link", fxIssue, "--kind", "depends_on", "--target", fxReq)
	// 缺 --kind／--target
	for _, args := range [][]string{
		{"link", fxIssue, "--target", "x"},
		{"link", fxIssue, "--kind", "commit"},
	} {
		if code := ta.run(args...); code != exitUsage {
			t.Errorf("%v code = %d", args, code)
		}
	}
	// 非法 kind
	if code := ta.run("link", fxIssue, "--kind", "bogus", "--target", "x"); code != exitErr {
		t.Errorf("非法 kind code = %d", code)
	}
	if code := ta.run("link"); code != exitUsage {
		t.Errorf("缺 id code = %d", code)
	}
	// JSON
	l := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "link", fxIssue, "--kind", "url", "--target", "https://x", "--json"))
	if l["kind"] != "url" || l["target"] != "https://x" {
		t.Errorf("link --json = %v", l)
	}
}

func TestVerifyCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	// 非 review → 拒
	code := ta.run("verify", fxIssue, "--note", "n")
	if code != exitErr || !strings.Contains(ta.stderr(), "只有 status=review") {
		t.Errorf("非 review code=%d stderr=%q", code, ta.stderr())
	}
	ta.mustRun(t, exitOK, "assign", fxIssue, "xiaoxia")
	ta.mustRun(t, exitOK, "move", fxIssue, "in_progress")
	ta.mustRun(t, exitOK, "move", fxIssue, "review")
	out := ta.mustRun(t, exitOK, "verify", fxIssue, "--note", "覆蓋率 91%", "--actor", "xiaoxia")
	if !strings.Contains(out, "已驗收 "+fxIssue) || !strings.Contains(out, statusIcon[domain.StatusDone]) {
		t.Errorf("verify 輸出 = %q", out)
	}
	// self-verified 標記有進 history
	h := ta.mustRun(t, exitOK, "history", fxIssue, "--json", "--limit", "1")
	if !strings.Contains(h, "[self-verified]") {
		t.Errorf("history 應含 [self-verified]: %s", h)
	}
	// 缺 --note
	if code := ta.run("verify", fxIssue); code != exitUsage {
		t.Errorf("缺 note code = %d", code)
	}
	if code := ta.run("verify"); code != exitUsage {
		t.Errorf("缺 id code = %d", code)
	}
}

func TestCommentCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "comment", fxIssue, "先做 UT90")
	if !strings.Contains(out, "已留言於 "+fxIssue) {
		t.Errorf("comment 輸出 = %q", out)
	}
	for _, args := range [][]string{
		{"comment", fxIssue},
		{"comment"},
	} {
		if code := ta.run(args...); code != exitUsage {
			t.Errorf("%v code = %d", args, code)
		}
	}
	if code := ta.run("comment", "Y20990101/NOPE", "hi"); code != exitErr {
		t.Errorf("不存在 code = %d", code)
	}
}

// ---------- DB 路徑與錯誤訊息 ----------

func TestDBFlagOverridesEnv(t *testing.T) {
	ta := newTestApp(t)
	other := filepath.Join(filepath.Dir(ta.dbPath), "other.db")
	ta.mustRun(t, exitOK, "init", "--db", other)
	ta.mustRun(t, exitOK, "init") // env 的預設路徑也建起來
	// seed 只寫進 --db 指定的那顆
	ta.mustRun(t, exitOK, "seed", "--db", other)
	if code := ta.run("get", fxProject); code != exitErr {
		t.Errorf("env DB 不該有 seed 的資料: code=%d out=%s", code, ta.stdout())
	}
	if code := ta.run("get", fxProject, "--db", other); code != exitOK {
		t.Errorf("--db 指定的 DB 應有種子: code=%d %s", code, ta.stderr())
	}
}

func TestHumanizeCoversAllSentinels(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errMissingActor, "缺少 actor"},
		{store.ErrMissingActor, "actor 是空字串"},
		{domain.ErrInvalidOwner, "不在名冊內"},
		{store.ErrNotFound, "找不到節點或關聯"},
		{store.ErrIDExists, "不自動加尾碼"},
		{store.ErrConflict, "重新讀取後再試"},
		{store.ErrDependsOnTargetMissing, "目標節點不存在"},
		{store.ErrCannotDelete, "不能刪除"},
		{store.ErrNotInReview, "只有 status=review"},
		{domain.ErrIllegalTransition, "狀態機不允許"},
		{domain.ErrMissingBlockReason, "要附理由"},
		{domain.ErrInvalidID, "ID 格式不合"},
		{errNotWired, "尚未接線"},
		{errors.New("原始錯誤"), "原始錯誤"},
	}
	for _, tc := range cases {
		if got := humanize(tc.err); !strings.Contains(got, tc.want) {
			t.Errorf("humanize(%v) = %q, want 含 %q", tc.err, got, tc.want)
		}
	}
}

func TestUsageErrExitCode(t *testing.T) {
	ta := newTestApp(t)
	if code := ta.run("tree", "--nope"); code != exitUsage {
		t.Errorf("壞 flag code = %d, want %d", code, exitUsage)
	}
}

// ---------- DB 路徑預設值 / migration 失敗 / 種子錯誤 ----------

// PB_DB 未設時走 var/board.db；t.Chdir 讓預設路徑落在暫存目錄（不污染專案）。
func TestDBPathDefaults(t *testing.T) {
	ta := newTestApp(t)
	delete(ta.vars, "PB_DB")
	t.Chdir(t.TempDir())
	ta.mustRun(t, exitOK, "init")
	if _, err := os.Stat(filepath.Join("var", "board.db")); err != nil {
		t.Fatalf("預設路徑 var/board.db 沒建立: %v", err)
	}
	// 相對路徑沒有目錄名（mkdirFor 的 no-op 分支）
	ta.vars["PB_DB"] = "board.db"
	ta.mustRun(t, exitOK, "init")
	if _, err := os.Stat("board.db"); err != nil {
		t.Fatalf("board.db 沒建立: %v", err)
	}
}

// DB 已存在但 schema 壞掉 → Migrate 失敗要回執行錯誤（openStore 的錯誤分支）。
func TestMigrateFailureIsReported(t *testing.T) {
	ta := newTestApp(t)
	if code := ta.run("init"); code != exitOK {
		t.Fatalf("init: %d %s", code, ta.stderr())
	}
	db, err := sql.Open("sqlite", "file:"+ta.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE meta"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE meta (k TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta (k) VALUES ('schema_version')"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	// meta.v NOT NULL 違反 → schemaVersion 讀值失敗
	if code := ta.run("tree"); code != exitErr {
		t.Errorf("壞 schema code = %d, want %d（%s）", code, exitErr, ta.stderr())
	}
}

func TestSeedRejectsUnknownActor(t *testing.T) {
	ta := newTestApp(t)
	ta.mustRun(t, exitOK, "init")
	code := ta.run("seed", "--actor", "nobody")
	if code != exitErr || !strings.Contains(ta.stderr(), "不在名冊內") {
		t.Errorf("code=%d stderr=%q", code, ta.stderr())
	}
}

func TestStatsOnEmptyDB(t *testing.T) {
	ta := newTestApp(t)
	ta.mustRun(t, exitOK, "init")
	out := ta.mustRun(t, exitOK, "stats")
	if !strings.Contains(out, "（無）") || !strings.Contains(out, "自我驗收張數：0") {
		t.Errorf("空庫 stats 輸出 = %q", out)
	}
}
