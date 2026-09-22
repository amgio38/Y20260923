package main

import (
	"strings"
	"testing"
)

// 來源：Y20260920/REQ-V03-CHECKLIST —— pb checklist 的分組 markdown 與空表。

const (
	fxItemA1 = "Y20260916/REQ-ALPHA/ITEM-A1"
	fxItemA2 = "Y20260916/REQ-ALPHA/ITEM-A2"
	fxItemB1 = "Y20260916/REQ-ALPHA/ITEM-B1"
)

// addItem：cli 路徑建一列母表項目（create item ＋ 可選 depends_on）。
func (ta *testApp) addItem(t *testing.T, key, title, owner, issue string) {
	t.Helper()
	ta.mustRun(t, exitOK, "create", "--type", "item", "--title", title, "--parent", fxReq,
		"--id", fxReq+"/ITEM-"+key, "--owner", owner)
	if issue != "" {
		ta.mustRun(t, exitOK, "link", fxReq+"/ITEM-"+key, "--kind", "depends_on", "--target", issue,
			"--note", "母表索引")
	}
}

func TestChecklistCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "move", fxIssue, "in_progress")
	ta.addItem(t, "B1", "慣例與可維護性", "yilong", "")
	ta.addItem(t, "A2", "第二項", "xiaoxia", fxIssue)
	ta.addItem(t, "A10", "第十項", "human", "")

	out := ta.mustRun(t, exitOK, "checklist", "--project", fxProject)
	for _, want := range []string{
		"# 母表（" + fxProject + "）",
		"## A", "## B",
		"| 編號 | 標題 | 狀態 | Owner |",
		"| A2 | 第二項 | ⬜ todo | xiaoxia |",
		"| A10 | 第十項 | ⬜ todo | human |",
		"| B1 | 慣例與可維護性 | ⬜ todo | yilong |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("checklist 輸出缺 %q\n%s", want, out)
		}
	}
	// 分組順序：A 段整段在 B 之前；組內照自然序（A2 在 A10 前）
	if strings.Index(out, "## A") > strings.Index(out, "## B") {
		t.Errorf("分組應 A 在 B 前\n%s", out)
	}
	if strings.Index(out, "| A2 |") > strings.Index(out, "| A10 |") {
		t.Errorf("組內應自然序（A2 在 A10 前）\n%s", out)
	}

	// JSON
	rows := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "checklist", "--project", fxProject, "--json"))
	if len(rows) != 3 {
		t.Fatalf("checklist --json = %v", rows)
	}
	if rows[0]["key"] != "A2" || rows[0]["item_id"] != fxItemA2 || rows[0]["issue_id"] != fxIssue {
		t.Errorf("rows[0] = %v", rows[0])
	}
	if rows[1]["key"] != "A10" || rows[1]["issue_id"] != nil {
		t.Errorf("rows[1] = %v（沒 link 的 issue_id 應省略）", rows[1])
	}
	if rows[2]["key"] != "B1" {
		t.Errorf("rows[2] = %v", rows[2])
	}
	// 狀態跟著 item（不是 issue）
	ta.mustRun(t, exitOK, "move", fxItemA2, "hold", "--note", "上線前再評")
	rows = decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "checklist", "--project", fxProject, "--json"))
	if rows[0]["status"] != "hold" {
		t.Errorf("狀態應取 item 自己：%v", rows[0])
	}

	// 別的專案看不到
	if rows := decodeJSON[[]map[string]any](t,
		ta.mustRun(t, exitOK, "checklist", "--project", "Y20990101", "--json")); len(rows) != 0 {
		t.Errorf("別專案 = %v", rows)
	}
	// 多餘參數＝用法錯誤
	if code := ta.run("checklist", "extra"); code != exitUsage {
		t.Errorf("多餘參數 code = %d, want %d", code, exitUsage)
	}
}

// TestChecklistEmpty：沒有 item 就印空表（只有表頭），exit 0。
func TestChecklistEmpty(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	out := ta.mustRun(t, exitOK, "checklist", "--project", fxProject)
	if !strings.Contains(out, "| 編號 | 標題 | 狀態 | Owner |") {
		t.Errorf("空表應印表頭\n%s", out)
	}
	if strings.Contains(out, "| A") || strings.Contains(out, "## ") {
		t.Errorf("沒有 item 不該有分組或資料列\n%s", out)
	}
	if rows := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "checklist", "--json")); len(rows) != 0 {
		t.Errorf("空表 --json = %v（want []）", rows)
	}
}

// TestChecklistItemParentRule：item 的 parent 必須是 req（CLI 端要看到人類看得懂的錯）。
func TestChecklistItemParentRule(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	// parent 是 issue → 執行錯誤，訊息要講清楚
	if code := ta.run("create", "--type", "item", "--title", "掛錯", "--parent", fxIssue,
		"--id", fxIssue+"/ITEM-A1"); code != exitErr {
		t.Errorf("parent 是 issue code = %d, want %d（stderr=%s）", code, exitErr, ta.stderr())
	}
	if !strings.Contains(ta.stderr(), "母表項目") {
		t.Errorf("錯誤訊息應說明 item 的 parent 要是 req：%q", ta.stderr())
	}
	// KEY 形狀錯 → ID 格式錯
	if code := ta.run("create", "--type", "item", "--title", "KEY 反了", "--parent", fxReq,
		"--id", fxReq+"/ITEM-1A"); code != exitErr {
		t.Errorf("KEY 形狀錯 code = %d, want %d", code, exitErr)
	}
	// 正常一列
	out := ta.mustRun(t, exitOK, "create", "--type", "item", "--title", "架構", "--parent", fxReq, "--id", fxItemA1)
	if !strings.Contains(out, "item") {
		t.Errorf("create 輸出 = %q", out)
	}
}
