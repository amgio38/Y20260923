package main

import (
	"strings"
	"testing"
)

// TestReparentCommand：CLI `pb reparent`（搬單；Y20260920/REQ-MOVE-NODE）。
func TestReparentCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "create", "--type", "project", "--title", "P2", "--id", "Y20261002")

	// 跨專案搬移（需 --note）。
	out := ta.mustRun(t, exitOK, "reparent", fxReq, "--parent", "Y20261002", "--note", "掛錯專案")
	if !strings.Contains(out, "已搬移 "+fxReq+" → Y20261002/REQ-ALPHA") {
		t.Errorf("reparent 輸出 = %q", out)
	}
	// 舊 id 仍可由 get 解析到新位置。
	if got := ta.mustRun(t, exitOK, "get", fxReq); !strings.Contains(got, "Y20261002/REQ-ALPHA") {
		t.Errorf("get 舊 id = %q", got)
	}
	// 缺 --parent → 用法錯誤。
	if code := ta.run("reparent", fxIssue); code != exitUsage {
		t.Errorf("缺 --parent code = %d，want %d", code, exitUsage)
	}
	// 根節點（專案）不可搬。
	if code := ta.run("reparent", fxProject, "--parent", "Y20261002"); code != exitErr || !strings.Contains(ta.stderr(), "不能搬") {
		t.Errorf("搬根節點 code=%d stderr=%q", code, ta.stderr())
	}
	// 搬回去（同專案）＋ JSON 輸出。
	n := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK,
		"reparent", "Y20261002/REQ-ALPHA", "--parent", fxProject, "--note", "搬回", "--json"))
	if n["new_id"] != fxReq {
		t.Errorf("reparent --json = %v", n)
	}
}
