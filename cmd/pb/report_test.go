package main

import (
	"strings"
	"testing"
	"time"

	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V03-WEEKLY —— pb report [--week] [--project] [--json]。

// setHistoryTSRaw：把某節點全部 history 的 ts 搬到指定時間（週報測試不 sleep）。
func (ta *testApp) setHistoryTSRaw(t *testing.T, nodeID string, ts time.Time) {
	t.Helper()
	ta.execRaw(t, "UPDATE history SET ts = ? WHERE node_id = ?", ts.Format(time.RFC3339), nodeID)
}

const fxIssueTwo = "Y20260916/REQ-ALPHA/ISSUE-TWO"

func TestReportCommand(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	// fxIssue：一路到 done（closure）；ISSUE-TWO：停在 in_progress（open）
	ta.mustRun(t, exitOK, "move", fxIssue, "in_progress", "--note", "接單")
	ta.mustRun(t, exitOK, "move", fxIssue, "review")
	ta.mustRun(t, exitOK, "verify", fxIssue, "--note", "CR 過", "--actor", "claude")
	ta.mustRun(t, exitOK, "comment", fxIssue, "順手留一句")
	ta.mustRun(t, exitOK, "create", "--type", "issue", "--title", "Two", "--parent", fxReq, "--id", fxIssueTwo,
		"--owner", "kaimake")
	ta.mustRun(t, exitOK, "move", fxIssueTwo, "in_progress", "--note", "開工")

	// 全部事件搬到 2026-09-07 那一週（fixture 的 create 也在同一週）
	inWeek := time.Date(2026, 9, 8, 10, 0, 0, 0, time.FixedZone("Asia/Taipei", 8*60*60))
	for _, id := range []string{fxProject, fxReq, fxIssue, fxIssueTwo} {
		ta.setHistoryTSRaw(t, id, inWeek)
	}

	out := ta.mustRun(t, exitOK, "report", "--week", "2026-09-10")
	for _, want := range []string{
		"週報 2026-09-07 ~ 2026-09-14（Asia/Taipei，不含下週一）",
		"誰做了什麼（10）：", "收了什麼（1）：", "還開著什麼（1）：",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report 輸出缺 %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, fxIssueTwo) || !strings.Contains(out, "claude") {
		t.Errorf("report 輸出應含 open 的 ISSUE-TWO 與收單的 claude\n%s", out)
	}

	rep := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "report", "--week", "2026-09-10", "--json"))
	if rep["week_start"] != "2026-09-07T00:00:00+08:00" || rep["week_end"] != "2026-09-14T00:00:00+08:00" {
		t.Errorf("週界 = %v ~ %v", rep["week_start"], rep["week_end"])
	}
	did, _ := rep["did"].([]any)
	closed, _ := rep["closed"].([]any)
	open, _ := rep["open"].([]any)
	if len(did) != 10 || len(closed) != 1 || len(open) != 1 {
		t.Fatalf("did=%d closed=%d open=%d（want 10／1／1）", len(did), len(closed), len(open))
	}
	if c := closed[0].(map[string]any); c["action"] != "verify" || c["node_id"] != fxIssue {
		t.Errorf("closed[0] = %v", closed[0])
	}
	if o := open[0].(map[string]any); o["id"] != fxIssueTwo || o["status"] != "in_progress" {
		t.Errorf("open[0] = %v", open[0])
	}

	// --project 收斂（Y20260916 內；別的專案沒有東西）
	if rep := decodeJSON[map[string]any](t,
		ta.mustRun(t, exitOK, "report", "--week", "2026-09-10", "--project", fxProject, "--json")); len(rep["open"].([]any)) != 1 {
		t.Errorf("--project 報告 = %v", rep)
	}
	if rep := decodeJSON[map[string]any](t,
		ta.mustRun(t, exitOK, "report", "--week", "2026-09-10", "--project", "Y20990101", "--json")); len(rep["did"].([]any)) != 0 {
		t.Errorf("別專案報告 = %v", rep)
	}

	// 不帶 --week＝本週（事件已被搬到過去，本週只剩 open 的現況）
	now := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "report", "--json"))
	wantStart := store.WeekStart(time.Now())
	if now["week_start"] != wantStart.Format(time.RFC3339) {
		t.Errorf("本週週起 = %v, want %s", now["week_start"], wantStart.Format(time.RFC3339))
	}

	// 用法錯誤
	if code := ta.run("report", "extra"); code != exitUsage {
		t.Errorf("多餘位置參數 code = %d, want %d", code, exitUsage)
	}
	if code := ta.run("report", "--week", "2026/09/10"); code != exitUsage {
		t.Errorf("--week 格式錯 code = %d, want %d", code, exitUsage)
	}
}

func TestReportEmptyDB(t *testing.T) {
	ta := newTestApp(t)
	ta.mustRun(t, exitOK, "init")
	out := ta.mustRun(t, exitOK, "report")
	for _, want := range []string{"誰做了什麼（0）：", "收了什麼（0）：", "還開著什麼（0）：", "（無）"} {
		if !strings.Contains(out, want) {
			t.Errorf("空庫週報缺 %q\n%s", want, out)
		}
	}
}

// oneLine：note 的換行在週報裡要被壓成空白（一行一筆）。
func TestReportNoteOneLine(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	ta.mustRun(t, exitOK, "comment", fxIssue, "第一行\n第二行\t有 tab")
	ta.setHistoryTSRaw(t, fxIssue, time.Date(2026, 9, 8, 10, 0, 0, 0, time.FixedZone("Asia/Taipei", 8*60*60)))
	out := ta.mustRun(t, exitOK, "report", "--week", "2026-09-10")
	if !strings.Contains(out, "第一行 第二行 有 tab") {
		t.Errorf("note 應壓成一行\n%s", out)
	}
	if strings.Contains(out, "第一行\n第二行") {
		t.Errorf("note 不該帶原始換行\n%s", out)
	}
}
