package store

import (
	"context"
	"testing"
	"time"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V03-WEEKLY —— 週界＝Asia/Taipei 週一 00:00 起、不含下週一。

func TestWeekStart(t *testing.T) {
	loc := time.FixedZone("Asia/Taipei", 8*60*60)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"週一中間", time.Date(2026, 9, 16, 13, 5, 0, 0, loc), "2026-09-14T00:00:00+08:00"},
		{"週日深夜", time.Date(2026, 9, 20, 23, 59, 59, 0, loc), "2026-09-14T00:00:00+08:00"},
		{"下週一 00:00 已換週", time.Date(2026, 9, 21, 0, 0, 0, 0, loc), "2026-09-21T00:00:00+08:00"},
		{"UTC 時刻換算回台北", time.Date(2026, 9, 20, 17, 0, 0, 0, time.UTC), "2026-09-21T00:00:00+08:00"},
		{"跨月", time.Date(2026, 10, 1, 9, 0, 0, 0, loc), "2026-09-28T00:00:00+08:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WeekStart(tc.in).In(loc).Format(time.RFC3339)
			if got != tc.want {
				t.Errorf("WeekStart(%s) = %s, want %s", tc.in.In(loc).Format(time.RFC3339), got, tc.want)
			}
			if wd := WeekStart(tc.in).In(loc).Weekday(); wd != time.Monday {
				t.Errorf("週起點應為週一，得到 %s", wd)
			}
		})
	}
}

// setHistoryTS：把某筆 history 的 ts 改到指定時間（週界測試不 sleep；history 的 ts 由程式寫入）。
// 只動「該節點該 action 的最新一筆」（同一節點可能有多筆同 action，例：todo→in_progress→review）。
func setHistoryTS(t *testing.T, s *Store, nodeID string, action domain.HistoryAction, ts time.Time) {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		"UPDATE history SET ts = ? WHERE id = (SELECT MAX(id) FROM history WHERE node_id = ? AND action = ?)",
		formatTime(ts), nodeID, string(action))
	if err != nil {
		t.Fatalf("setHistoryTS: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("setHistoryTS: 找不到 %s 的 %s 事件", nodeID, action)
	}
}

func TestWeekly(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	loc := time.FixedZone("Asia/Taipei", 8*60*60)

	// 本週（2026-09-14 週一起）：review（週一）＋ verify（週三）
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")
	// 本週（2026-09-07 週一起）的事件：review（週二）＋ verify（週三）
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusReview, "做完")
	setHistoryTS(t, s, issue.ID, domain.ActionTransition, time.Date(2026, 9, 8, 10, 0, 0, 0, loc))
	if _, err := s.Verify(bg, "claude", issue.ID, "CR 過"); err != nil {
		t.Fatal(err)
	}
	setHistoryTS(t, s, issue.ID, domain.ActionVerify, time.Date(2026, 9, 9, 9, 0, 0, 0, loc))
	// 再上一週的事件（不該進本週）
	other := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-OLD", Title: "Old", ParentID: req.ID, Owner: "kaimake",
	})
	mustTransition(t, s, "kaimake", other.ID, domain.StatusInProgress, "上週開工")
	setHistoryTS(t, s, other.ID, domain.ActionTransition, time.Date(2026, 9, 1, 10, 0, 0, 0, loc))

	// 下週一 00:00 的事件（不含）
	next := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-NEXT", Title: "Next", ParentID: req.ID, Owner: "kaimadi",
	})
	mustTransition(t, s, "kaimadi", next.ID, domain.StatusInProgress, "下週")
	setHistoryTS(t, s, next.ID, domain.ActionTransition, time.Date(2026, 9, 14, 0, 0, 0, 0, loc))

	// 還開著什麼：issue 且 in_progress／review
	mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: req.ID + "/REQ-B", Title: "B", ParentID: req.ID, Owner: "yilong",
	})
	done := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-DONE", Title: "Done", ParentID: req.ID, Owner: "yilong",
	})
	mustTransition(t, s, "yilong", done.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "yilong", done.ID, domain.StatusReview, "")
	mustTransition(t, s, "yilong", done.ID, domain.StatusDone, "收")
	hold := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-HOLD", Title: "Hold", ParentID: req.ID, Owner: "yilong",
	})
	mustTransition(t, s, "yilong", hold.ID, domain.StatusHold, "")

	// 別的專案（不該進本專案週報）
	otherProj := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "other"})
	otherReq := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: otherProj.ID + "/REQ-X", Title: "X", ParentID: otherProj.ID,
	})
	otherIssue := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: otherReq.ID + "/ISSUE-X", Title: "X1", ParentID: otherReq.ID, Owner: "claude",
	})
	mustTransition(t, s, "claude", otherIssue.ID, domain.StatusInProgress, "別專案")
	setHistoryTS(t, s, otherIssue.ID, domain.ActionTransition, time.Date(2026, 9, 9, 12, 0, 0, 0, loc))

	week := time.Date(2026, 9, 10, 12, 0, 0, 0, loc) // 該週任一時刻（歷史週：fixture 的 create 事件在現在，不會干擾）
	rep, err := s.Weekly(bg, project.ID, week)
	if err != nil {
		t.Fatalf("Weekly: %v", err)
	}
	if got := rep.WeekStart.In(loc).Format(time.RFC3339); got != "2026-09-07T00:00:00+08:00" {
		t.Errorf("WeekStart = %s", got)
	}
	if got := rep.WeekEnd.In(loc).Format(time.RFC3339); got != "2026-09-14T00:00:00+08:00" {
		t.Errorf("WeekEnd = %s", got)
	}
	// 誰做了什麼：本週 2 筆（transition＋verify）；上上週與下週各 1 筆不算
	if len(rep.Did) != 2 {
		t.Fatalf("Did = %+v, want 2 筆", rep.Did)
	}
	if rep.Did[0].Action != domain.ActionTransition || rep.Did[1].Action != domain.ActionVerify {
		t.Errorf("Did 應依時間序（transition → verify）：%+v", rep.Did)
	}
	// 收了什麼：只有 verify
	if len(rep.Closed) != 1 || rep.Closed[0].Action != domain.ActionVerify || rep.Closed[0].Actor != "claude" {
		t.Errorf("Closed = %+v, want 1 筆 claude 的 verify", rep.Closed)
	}
	// 還開著什麼：ISSUE-OLD（in_progress）、ISSUE-NEXT（in_progress）；done／hold 不算
	var openIDs []string
	for _, n := range rep.Open {
		openIDs = append(openIDs, n.ID)
	}
	want := []string{req.ID + "/ISSUE-NEXT", req.ID + "/ISSUE-OLD"}
	if len(openIDs) != len(want) {
		t.Fatalf("Open = %v, want %v", openIDs, want)
	}
	for i := range want {
		if openIDs[i] != want[i] {
			t.Errorf("Open[%d] = %s, want %s", i, openIDs[i], want[i])
		}
	}

	// 全庫：多了別專案那筆
	all, err := s.Weekly(bg, "", week)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Did) != 3 {
		t.Errorf("全庫 Did = %d 筆, want 3", len(all.Did))
	}
	if len(all.Open) != 3 {
		t.Errorf("全庫 Open = %d 筆, want 3", len(all.Open))
	}

	// 空的一週：三塊都空（不報錯）
	empty, err := s.Weekly(bg, project.ID, time.Date(2026, 1, 7, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Did) != 0 || len(empty.Closed) != 0 {
		t.Errorf("空白週 = %+v", empty)
	}
	// 空週的 Open 仍是「現在的」開著什麼（不是那一週的快照）
	if len(empty.Open) != 2 {
		t.Errorf("空週 Open = %d, want 2（Open 是現況）", len(empty.Open))
	}
}

func TestWeeklyEmptyDB(t *testing.T) {
	s := newStore(t)
	rep, err := s.Weekly(context.Background(), "", time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Weekly: %v", err)
	}
	if len(rep.Did) != 0 || len(rep.Closed) != 0 || len(rep.Open) != 0 {
		t.Errorf("空庫週報 = %+v", rep)
	}
}

// commentAt：在指定時間點對節點留言（驗證 Did 收得到 comment，Closed 只收 verify）。
func TestWeeklyIncludesComment(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, _, issue := fixtureTree(t, s)
	loc := time.FixedZone("Asia/Taipei", 8*60*60)
	if err := s.Comment(bg, "human", issue.ID, "進度備註"); err != nil {
		t.Fatal(err)
	}
	setHistoryTS(t, s, issue.ID, domain.ActionComment, time.Date(2026, 9, 8, 10, 0, 0, 0, loc))

	rep, err := s.Weekly(bg, project.ID, time.Date(2026, 9, 10, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Did) != 1 || rep.Did[0].Note != "進度備註" {
		t.Errorf("Did = %+v, want comment 一筆", rep.Did)
	}
	if len(rep.Closed) != 0 {
		t.Errorf("Closed 只收 verify，得到 %+v", rep.Closed)
	}
}
