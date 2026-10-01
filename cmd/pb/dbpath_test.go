package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 2026-09-28 BUG-PB-DEFAULT-DB-CWD-CREATES-EMPTY-DB：預設 DB 不再只相對 CWD，
// 找不到時也不隱式建空庫。這組測試會 chdir，所以不能 t.Parallel。

// chdir：切到 dir，測完切回來。
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// fakeProject：<root>/go.mod ＋ <root>/bin/pb（空檔即可，只看路徑）。
func fakeProject(t *testing.T) (root, exe string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(root, "bin", "pb")
	for _, f := range []string{filepath.Join(root, "go.mod"), exe} {
		if err := writeFile(f, ""); err != nil {
			t.Fatal(err)
		}
	}
	return root, exe
}

// dbApp：沒有 PB_DB、executable 可注入的 app。
func dbApp(exe string) (*app, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	vars := map[string]string{"PB_ACTOR": "human"}
	a := &app{stdout: out, stderr: errOut, getenv: func(k string) string { return vars[k] }}
	a.executable = func() (string, error) { return exe, nil }
	return a, out, errOut
}

func TestResolveDefaultDBOrder(t *testing.T) {
	root, exe := fakeProject(t)
	elsewhere := t.TempDir()
	chdir(t, elsewhere)

	// PB_DB 最優先。
	a, _, _ := dbApp(exe)
	a.getenv = func(k string) string {
		if k == "PB_DB" {
			return "/x/y.db"
		}
		return ""
	}
	if p, fb := a.resolveDefaultDB(); p != "/x/y.db" || fb {
		t.Errorf("PB_DB: got %q %v", p, fb)
	}

	// CWD 沒有 var/board.db → 由 binary 推回專案根。
	a, _, _ = dbApp(exe)
	if p, fb := a.resolveDefaultDB(); p != filepath.Join(root, defaultDBPath) || fb {
		t.Errorf("project root: got %q %v", p, fb)
	}

	// symlink 指過去（PATH 上的 pb）也要解到專案根。
	link := filepath.Join(t.TempDir(), "pb")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	a, _, _ = dbApp(link)
	if p, _ := a.resolveDefaultDB(); p != filepath.Join(root, defaultDBPath) {
		t.Errorf("symlink: got %q", p)
	}

	// CWD 已有 var/board.db → 照舊用它（在 repo 裡的行為不變）。
	if err := os.MkdirAll("var", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(defaultDBPath, ""); err != nil {
		t.Fatal(err)
	}
	a, _, _ = dbApp(exe)
	if p, fb := a.resolveDefaultDB(); p != defaultDBPath || fb {
		t.Errorf("cwd existing: got %q %v", p, fb)
	}
}

func TestProjectRootRejectsNonProjectBinaries(t *testing.T) {
	root, _ := fakeProject(t)
	cases := map[string]func() (string, error){
		"not under bin/": func() (string, error) { return filepath.Join(root, "pb"), nil },
		"bin/ but no go.mod": func() (string, error) {
			return filepath.Join(t.TempDir(), "bin", "pb"), nil
		},
		"executable error": func() (string, error) { return "", os.ErrNotExist },
	}
	for name, exe := range cases {
		a, _, _ := dbApp("")
		a.executable = exe
		if got := a.projectRoot(); got != "" {
			t.Errorf("%s: projectRoot = %q, want 空", name, got)
		}
	}
	// 沒注入時走 os.Executable（go test binary 不在 bin/ 底下）。
	a := &app{getenv: func(string) string { return "" }}
	if got := a.projectRoot(); got != "" {
		t.Errorf("os.Executable: projectRoot = %q", got)
	}
}

// TestNoImplicitEmptyDBOutsideProject：repo 外、binary 也推不出專案根時，
// 讀取類／serve／mcp 一律拒絕並給提示，不在 CWD 生空庫；只有 pb init 會建。
func TestNoImplicitEmptyDBOutsideProject(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	for _, args := range [][]string{
		{"search", "--owner", "xiaoxia"},
		{"get", "Y20260916"},
		{"seed"},
		{"serve", "--addr", "127.0.0.1:0"},
		{"mcp"},
	} {
		a, _, errOut := dbApp(filepath.Join(dir, "somewhere", "pb"))
		if code := a.dispatch(args); code != exitErr {
			t.Errorf("%v: code = %d, want %d（stderr=%s）", args, code, exitErr, errOut)
		}
		if !strings.Contains(errOut.String(), "找不到 DB") || !strings.Contains(errOut.String(), "pb init") {
			t.Errorf("%v: stderr 要說明原因與解法，got %q", args, errOut)
		}
		if fileExists(defaultDBPath) {
			t.Fatalf("%v: 不該在 CWD 生出 %s", args, defaultDBPath)
		}
	}

	a, _, errOut := dbApp(filepath.Join(dir, "somewhere", "pb"))
	if code := a.dispatch([]string{"init"}); code != exitOK {
		t.Fatalf("init: code = %d（%s）", code, errOut)
	}
	if !fileExists(defaultDBPath) {
		t.Fatal("pb init 明講要建庫，應該建出來")
	}
	// 建好之後其他指令照常。
	a, _, errOut = dbApp(filepath.Join(dir, "somewhere", "pb"))
	if code := a.dispatch([]string{"search", "--owner", "xiaoxia"}); code != exitOK {
		t.Errorf("init 後 search: code = %d（%s）", code, errOut)
	}
}

// TestProjectRootDBIsCreatedOnFreshCheckout：binary 在專案 bin/ 裡時，全新 checkout
// 從別的目錄跑也照舊自己建出 <root>/var/board.db（不退化成「一定要先 init」）。
func TestProjectRootDBIsCreatedOnFreshCheckout(t *testing.T) {
	root, exe := fakeProject(t)
	chdir(t, t.TempDir())
	a, _, errOut := dbApp(exe)
	if code := a.dispatch([]string{"search", "--owner", "xiaoxia"}); code != exitOK {
		t.Fatalf("code = %d（%s）", code, errOut)
	}
	if !fileExists(filepath.Join(root, defaultDBPath)) {
		t.Error("應建在專案根的 var/board.db")
	}
	if fileExists(defaultDBPath) {
		t.Error("不該在 CWD 建")
	}
}
