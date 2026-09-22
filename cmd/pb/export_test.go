package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V02-EXPORT。匯出＝該 id 自己＋全部子孫，一節點一檔，檔頭四行。

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("讀 %s: %v", path, err)
	}
	return string(b)
}

func TestExportCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "comment", fxIssue, "先做 UT90") // body 之外的東西不進匯出

	dir := t.TempDir()
	out := ta.mustRun(t, exitOK, "export", fxReq, "--out", dir)
	if !strings.Contains(out, "已匯出 2 個節點") {
		t.Errorf("export 輸出 = %q", out)
	}

	// 路徑照 id 分段、最後一段加 .md；project 不在 fxReq 子樹裡，不該被匯出
	reqFile := filepath.Join(dir, "Y20260916", "REQ-ALPHA.md")
	issueFile := filepath.Join(dir, "Y20260916", "REQ-ALPHA", "ISSUE-ONE.md")
	want := "id: " + fxReq + "\ntype: req\nstatus: todo\nowner: unassigned\n\n"
	if got := readFile(t, reqFile); got != want {
		t.Errorf("REQ 檔內容 = %q, want %q", got, want)
	}
	if got := readFile(t, issueFile); !strings.HasPrefix(got, "id: "+fxIssue+"\ntype: issue\nstatus: todo\nowner: unassigned\n\n") {
		t.Errorf("ISSUE 檔內容 = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "Y20260916.md")); err == nil {
		t.Error("不該匯出 fxReq 以外的節點（project 自己）")
	}

	// 同路徑可覆蓋（var/export 是我們產生的）
	ta.mustRun(t, exitOK, "update", fxReq, "--body", "改過的 body")
	ta.mustRun(t, exitOK, "export", fxReq, "--out", dir)
	if got := readFile(t, reqFile); !strings.Contains(got, "改過的 body") {
		t.Errorf("覆蓋後內容 = %q", got)
	}

	// 一次匯出整棵專案（自己＋全部子孫）
	dir2 := t.TempDir()
	if out := ta.mustRun(t, exitOK, "export", fxProject, "--out", dir2, "--json"); !strings.Contains(out, `"files"`) {
		t.Errorf("export --json = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir2, "Y20260916.md")); err != nil {
		t.Errorf("project 自己也要一檔: %v", err)
	}

	// id 不存在 → exit 1；缺 id → 用法錯誤
	if code := ta.run("export", "Y20990101/NOPE", "--out", dir); code != exitErr {
		t.Errorf("不存在 id code = %d, want %d", code, exitErr)
	}
	if code := ta.run("export", fxReq, "--out", dir, "extra"); code != exitUsage {
		t.Errorf("多餘參數 code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("export"); code != exitUsage {
		t.Errorf("缺 id code = %d, want %d", code, exitUsage)
	}
}

// TestExportDefaultDir：沒帶 --out 時寫到 var/export（相對目前工作目錄）。
func TestExportDefaultDir(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	work := t.TempDir()
	t.Chdir(work)

	ta.mustRun(t, exitOK, "export", fxIssue)
	p := filepath.Join(work, "var", "export", "Y20260916", "REQ-ALPHA", "ISSUE-ONE.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("預設輸出 %s: %v", p, err)
	}
}

// TestExportWriteFailure：--out 底下的目錄建不起來（被檔案佔住）→ 執行錯誤（exit 1），不是用法錯誤。
func TestExportWriteFailure(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ta.mustRun(t, exitOK, "export", fxIssue, "--out", t.TempDir()) // 先確認正常路徑可用
	if code := ta.run("export", fxIssue, "--out", blocked); code != exitErr {
		t.Errorf("寫不進去 code = %d, want %d（stdout=%s stderr=%s）", code, exitErr, ta.stdout(), ta.stderr())
	}

	// 目標檔名被同名的目錄佔住 → WriteFile 失敗
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Y20260916", "REQ-ALPHA", "ISSUE-ONE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := ta.run("export", fxIssue, "--out", dir); code != exitErr {
		t.Errorf("檔名被目錄佔住 code = %d, want %d（stderr=%s）", code, exitErr, ta.stderr())
	}

	// --out 少帶值 → 用法錯誤
	if code := ta.run("export", fxIssue, "--out"); code != exitUsage {
		t.Errorf("--out 缺值 code = %d, want %d", code, exitUsage)
	}
}

func TestExportPathRejectsUnsafeIDs(t *testing.T) {
	cases := []string{"../evil", "a/../../b", "a//b", "", "a/./b", `a\b`}
	for _, id := range cases {
		if p, err := exportPath("out", id); err == nil {
			t.Errorf("exportPath(%q) = %q, want 錯誤", id, p)
		}
	}
	p, err := exportPath("out", "Y20260916/REQ-ALPHA")
	if err != nil || p != filepath.Join("out", "Y20260916", "REQ-ALPHA")+".md" {
		t.Errorf("exportPath = %q (err=%v)", p, err)
	}
}

func TestExportMarkdown(t *testing.T) {
	n := domain.Node{ID: "A/B", Type: domain.TypeIssue, Status: domain.StatusDone, Owner: "xiaoxia", Body: "本文"}
	want := "id: A/B\ntype: issue\nstatus: done\nowner: xiaoxia\n\n本文\n"
	if got := exportMarkdown(n); got != want {
		t.Errorf("exportMarkdown = %q, want %q", got, want)
	}
	n.Body = "本文\n"
	if got := exportMarkdown(n); got != want {
		t.Errorf("body 已帶換行時 = %q, want %q", got, want)
	}
	n.Body = ""
	if got := exportMarkdown(n); got != "id: A/B\ntype: issue\nstatus: done\nowner: xiaoxia\n\n" {
		t.Errorf("空 body = %q", got)
	}
}
