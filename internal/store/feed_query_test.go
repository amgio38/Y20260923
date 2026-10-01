package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"project_board/internal/domain"
)

// 2026-09-28 BUG-A1-STORE-COVERAGE-BELOW-90：HistorySince／MaxHistoryID／NodesByID／
// SelfVerifiedHistory 只被 httpapi／web 間接用到，store 自己的測試一行都沒跑過。
// 這裡用真 SQLite 直接鎖行為（不用替身）。

func TestMaxHistoryIDAndHistorySince(t *testing.T) {
	bg := context.Background()
	s := newStore(t)
	if id, err := s.MaxHistoryID(bg); err != nil || id != 0 {
		t.Fatalf("空表 MaxHistoryID = %d, %v; want 0", id, err)
	}
	if got, err := s.HistorySince(bg, 0, 0); err != nil || len(got) != 0 {
		t.Fatalf("空表 HistorySince = %v, %v", got, err)
	}

	_, _, issue := fixtureTree(t, s)
	max, err := s.MaxHistoryID(bg)
	if err != nil || max < 3 {
		t.Fatalf("三筆 create 後 MaxHistoryID = %d, %v", max, err)
	}
	all, err := s.HistorySince(bg, 0, 0)
	if err != nil || int64(len(all)) != max {
		t.Fatalf("HistorySince(0, 不限) = %d 筆, %v; want %d", len(all), err, max)
	}
	for i := 1; i < len(all); i++ {
		if all[i].ID <= all[i-1].ID {
			t.Fatalf("要依 id 升冪（寫入序）：%d 在 %d 之後", all[i].ID, all[i-1].ID)
		}
	}

	if _, err := s.Transition(bg, "human", issue.ID, domain.StatusInProgress, "", nil); err != nil {
		t.Fatal(err)
	}
	newer, err := s.HistorySince(bg, max, 0)
	if err != nil || len(newer) != 1 || newer[0].NodeID != issue.ID || newer[0].ID <= max {
		t.Fatalf("HistorySince(max) 只該拿到新的那一筆：%+v, %v", newer, err)
	}
	limited, err := s.HistorySince(bg, 0, 2)
	if err != nil || len(limited) != 2 || limited[0].ID != all[0].ID {
		t.Fatalf("limit=2 要回最舊的兩筆：%+v, %v", limited, err)
	}
}

func TestNodesByID(t *testing.T) {
	bg := context.Background()
	s := newStore(t)
	project, req, _ := fixtureTree(t, s)

	empty, err := s.NodesByID(bg, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("空 ids = %v, %v", empty, err)
	}
	got, err := s.NodesByID(bg, []string{project.ID, req.ID, "Y20260916/REQ-NOPE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("不存在的不該在 map 裡：%v", got)
	}
	if got[req.ID].ParentID != project.ID || got[req.ID].Type != domain.TypeReq {
		t.Errorf("REQ 欄位不對：%+v", got[req.ID])
	}
}

func TestSelfVerifiedHistory(t *testing.T) {
	bg := context.Background()
	s := newStore(t)
	project, _, _ := fixtureTree(t, s)
	other := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20260917", Title: "other", Owner: "xiaoxia"})

	mk := func(parent, id, owner string) domain.Node {
		return mustCreate(t, s, "human", CreateInput{Type: domain.TypeIssue, ID: parent + "/" + id, Title: id, ParentID: parent, Owner: owner})
	}
	toReview := func(id, actor string) {
		t.Helper()
		for _, st := range []domain.Status{domain.StatusInProgress, domain.StatusReview} {
			if _, err := s.Transition(bg, actor, id, st, "", nil); err != nil {
				t.Fatalf("%s → %s: %v", id, st, err)
			}
		}
	}
	verify := func(id, actor, note string) {
		t.Helper()
		if _, err := s.Verify(bg, actor, id, note); err != nil {
			t.Fatalf("Verify %s: %v", id, err)
		}
	}
	req := project.ID + "/REQ-ALPHA"

	self := mk(req, "ISSUE-SELF", "xiaoxia") // 自己驗兩次（重開再驗）→ 只留最新一筆
	toReview(self.ID, "xiaoxia")
	verify(self.ID, "xiaoxia", "第一次")
	if _, err := s.Transition(bg, "xiaoxia", self.ID, domain.StatusInProgress, "重開", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(bg, "xiaoxia", self.ID, domain.StatusReview, "", nil); err != nil {
		t.Fatal(err)
	}
	verify(self.ID, "xiaoxia", "第二次")

	byOther := mk(req, "ISSUE-BY-CLAUDE", "xiaoxia") // 別人驗 → 不算
	toReview(byOther.ID, "xiaoxia")
	verify(byOther.ID, "claude", "克勞德驗")

	reopened := mk(req, "ISSUE-REOPENED", "xiaoxia") // 自己驗後又重開、目前不是 done → 不算
	toReview(reopened.ID, "xiaoxia")
	verify(reopened.ID, "xiaoxia", "驗了")
	if _, err := s.Transition(bg, "xiaoxia", reopened.ID, domain.StatusInProgress, "重開", nil); err != nil {
		t.Fatal(err)
	}

	elsewhere := mk(other.ID, "ISSUE-ELSEWHERE", "xiaoxia") // 別的 project
	toReview(elsewhere.ID, "xiaoxia")
	verify(elsewhere.ID, "xiaoxia", "別處")

	got, err := s.SelfVerifiedHistory(bg, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].NodeID != self.ID || !strings.Contains(got[0].Note, "第二次") {
		t.Fatalf("project 範圍只該有 ISSUE-SELF 的最新一筆：%+v", got)
	}
	all, err := s.SelfVerifiedHistory(bg, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("全庫 = %+v, %v; want 2（SELF＋ELSEWHERE）", all, err)
	}
	// 數字要跟 Stats 的 self_verified_count 對得上（同一條 WHERE 的用意）。
	st, err := s.Stats(bg, project.ID)
	if err != nil || st.SelfVerifiedCount != len(got) {
		t.Errorf("Stats.SelfVerifiedCount = %d, 清單 %d 筆, err=%v", st.SelfVerifiedCount, len(got), err)
	}
}

// TestFeedQueriesReportClosedDB：DB 已關時四支都回錯（不 panic、不回假的空結果）。
func TestFeedQueriesReportClosedDB(t *testing.T) {
	bg := context.Background()
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(bg); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := s.HistorySince(bg, 0, 1); err == nil {
		t.Error("HistorySince 應回錯")
	}
	if _, err := s.MaxHistoryID(bg); err == nil {
		t.Error("MaxHistoryID 應回錯")
	}
	if _, err := s.NodesByID(bg, []string{"x"}); err == nil {
		t.Error("NodesByID 應回錯")
	}
	if _, err := s.SelfVerifiedHistory(bg, "Y20260916"); err == nil {
		t.Error("SelfVerifiedHistory 應回錯")
	}
}
