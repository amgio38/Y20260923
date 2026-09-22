package main

import (
	"strings"
	"testing"
)

func TestRepoSetShow(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)

	out := ta.mustRun(t, exitOK, "repo", "set", fxProject, "--url", "https://github.com/x/y", "--path", "/tmp/y", "--actor", "human")
	if !strings.Contains(out, "github.com/x/y") {
		t.Fatalf("set 輸出應含 url，got %q", out)
	}

	// idempotent：再設一次 → 只剩新的。
	ta.mustRun(t, exitOK, "repo", "set", fxProject, "--url", "https://github.com/x/z", "--actor", "human")

	show := ta.mustRun(t, exitOK, "repo", "show", fxProject)
	if !strings.Contains(show, "github.com/x/z") || strings.Contains(show, "github.com/x/y") {
		t.Fatalf("show 應只剩新 url，got %q", show)
	}

	js := ta.mustRun(t, exitOK, "repo", "show", fxProject, "--json")
	if !strings.Contains(js, `"set": true`) || !strings.Contains(js, "github.com/x/z") {
		t.Fatalf("show --json 不符，got %q", js)
	}
}

func TestRepoSetNonProject(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	if code := ta.run("repo", "set", fxReq, "--url", "u", "--actor", "human"); code != exitErr {
		t.Fatalf("對 req 設 repo code = %d, want %d", code, exitErr)
	}
	if code := ta.run("repo", "show", fxProject); code != exitOK {
		t.Fatalf("show 未設定 code = %d, want %d", code, exitOK)
	}
}

func TestRepoUsage(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	// 無子命令／未知子命令／set 缺 url／show 多參數 → 用法錯誤。
	if code := ta.run("repo"); code != exitUsage {
		t.Fatalf("repo 無子命令 code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("repo", "bogus"); code != exitUsage {
		t.Fatalf("repo 未知子命令 code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("repo", "set", fxProject, "--actor", "human"); code != exitUsage {
		t.Fatalf("set 缺 --url code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("repo", "set", fxProject, "--url", "u", "--actor", "human", "extra"); code != exitUsage {
		t.Fatalf("set 多參數 code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("repo", "show"); code != exitUsage {
		t.Fatalf("show 缺 id code = %d, want %d", code, exitUsage)
	}
}
