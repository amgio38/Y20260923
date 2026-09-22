package store

import (
	"context"
	"errors"
	"testing"

	"project_board/internal/domain"
)

func TestSearch(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	body, tags := "member core 認證流程", "auth,login"
	if _, err := s.Update(bg, "human", req.ID, UpdateInput{Body: &body}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(bg, "human", issue.ID, UpdateInput{Tags: &tags}, nil); err != nil {
		t.Fatal(err)
	}
	// 別的專案也要有同樣關鍵字，確認 project 過濾有效
	other := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "other"})
	mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: other.ID + "/REQ-MEMBER", Title: "member 別的專案", ParentID: other.ID,
	})

	ids := func(ns []domain.Node) []string {
		out := make([]string, 0, len(ns))
		for _, n := range ns {
			out = append(out, n.ID)
		}
		return out
	}

	cases := []struct {
		name    string
		query   string
		project string
		want    int
	}{
		{"title 命中", "Issue one", "", 1},
		{"body 命中", "認證流程", "", 1},
		{"tags 命中", "auth", "", 1},
		{"跨專案命中", "member", "", 2},
		{"限定專案", "member", project.ID, 1},
		{"萬用字元當字面值", "%", "", 0},
		{"空查詢回全部", "", "", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Search(bg, tc.query, tc.project)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("Search(%q, %q) = %v (%d 筆), want %d", tc.query, tc.project, ids(got), len(got), tc.want)
			}
		})
	}
}

func TestHistoryLimitAndNotFound(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)
	mustAssign(t, s, "human", issue.ID, "xiaoxia")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")

	all := mustHistory(t, s, issue.ID, 0)
	if len(all) != 3 { // create + assign + transition
		t.Fatalf("history = %d, want 3", len(all))
	}
	if all[0].Action != domain.ActionTransition || all[2].Action != domain.ActionCreate {
		t.Errorf("應為最新優先: %+v", all)
	}
	if two := mustHistory(t, s, issue.ID, 2); len(two) != 2 || two[1].Action != domain.ActionAssign {
		t.Errorf("limit=2 = %+v", two)
	}
	if _, err := s.History(bg, "Y20990101/NOPE", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStats(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	req2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-BETA", Title: "Beta", ParentID: project.ID,
	})
	issue2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-TWO", Title: "Two", ParentID: req.ID,
	})
	issue3 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-THREE", Title: "Three", ParentID: req.ID, Owner: "xiaoxia",
	})

	// ISSUE-ONE：claude 驗收（非自我驗收）
	mustAssign(t, s, "human", issue.ID, "xiaoxia")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusReview, "")
	if _, err := s.Verify(bg, "claude", issue.ID, "CR 通過"); err != nil {
		t.Fatal(err)
	}
	// ISSUE-TWO：cancel（不計入完成度分母）
	mustTransition(t, s, "human", issue2.ID, domain.StatusCancel, "")
	// ISSUE-THREE：xiaoxia 自我驗收
	mustTransition(t, s, "xiaoxia", issue3.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "xiaoxia", issue3.ID, domain.StatusReview, "")
	if _, err := s.Verify(bg, "xiaoxia", issue3.ID, "自測 92%"); err != nil {
		t.Fatal(err)
	}
	// 別的專案：不應計入
	other := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "other"})

	st, err := s.Stats(bg, project.ID)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	wantStatus := map[domain.Status]int{
		domain.StatusTodo:   3, // project, REQ-ALPHA, REQ-BETA
		domain.StatusDone:   2, // ISSUE-ONE, ISSUE-THREE
		domain.StatusCancel: 1, // ISSUE-TWO
	}
	if len(st.CountByStatus) != len(wantStatus) {
		t.Fatalf("CountByStatus = %+v, want %+v", st.CountByStatus, wantStatus)
	}
	for k, v := range wantStatus {
		if st.CountByStatus[k] != v {
			t.Errorf("CountByStatus[%s] = %d, want %d", k, st.CountByStatus[k], v)
		}
	}
	wantOwner := map[string]int{"human": 1, "unassigned": 2}
	for k, v := range wantOwner {
		if st.CountByOwner[k] != v {
			t.Errorf("CountByOwner[%s] = %d, want %d", k, st.CountByOwner[k], v)
		}
	}
	// 手上張數只算未結案（done／cancel 不算，§5-3）
	if _, ok := st.CountByOwner["xiaoxia"]; ok {
		t.Errorf("done／cancel 不應計入手上張數: %+v", st.CountByOwner)
	}
	if got := st.ReqProgress[req.ID]; got != 1.0 {
		t.Errorf("ReqProgress[%s] = %v, want 1（cancel 不計入分母）", req.ID, got)
	}
	if got, ok := st.ReqProgress[req2.ID]; !ok || got != 0 {
		t.Errorf("ReqProgress[%s] = %v (存在=%v), want 0", req2.ID, got, ok)
	}
	if st.SelfVerifiedCount != 1 {
		t.Errorf("SelfVerifiedCount = %d, want 1", st.SelfVerifiedCount)
	}

	// 全庫：多一顆 project
	all, err := s.Stats(bg, "")
	if err != nil {
		t.Fatal(err)
	}
	if all.CountByOwner["unassigned"] != 3 || all.CountByStatus[domain.StatusTodo] != 4 {
		t.Errorf("全庫統計 = %+v", all)
	}
	if _, ok := all.ReqProgress[other.ID+"/REQ-X"]; ok {
		t.Error("不應有別的 REQ 進度")
	}
}

// 回歸：無子單（total==0）的 REQ，完成度退回自身狀態——done→1.0、其餘→0。
// 先前一律 0%，導致已 done 的 childless REQ 在看板上誤報 0%。
func TestStatsReqProgressChildless(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, _, _ := fixtureTree(t, s)

	doneReq := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-DONE-NOKIDS", Title: "done no kids", ParentID: project.ID,
	})
	mustTransition(t, s, "human", doneReq.ID, domain.StatusInProgress, "")
	mustTransition(t, s, "human", doneReq.ID, domain.StatusReview, "")
	if _, err := s.Verify(bg, "claude", doneReq.ID, "無子單直接驗收"); err != nil {
		t.Fatal(err)
	}
	todoReq := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-TODO-NOKIDS", Title: "todo no kids", ParentID: project.ID,
	})

	st, err := s.Stats(bg, project.ID)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if got := st.ReqProgress[doneReq.ID]; got != 1.0 {
		t.Errorf("無子單且已 done 的 REQ 完成度 = %v, want 1", got)
	}
	if got, ok := st.ReqProgress[todoReq.ID]; !ok || got != 0 {
		t.Errorf("無子單且 todo 的 REQ 完成度 = %v (存在=%v), want 0", got, ok)
	}
}

func TestRecentHistory(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)
	mustAssign(t, s, "human", issue.ID, "xiaoxia")
	mustTransition(t, s, "xiaoxia", issue.ID, domain.StatusInProgress, "")
	if err := s.Comment(bg, "xiaoxia", req.ID, "hi"); err != nil {
		t.Fatal(err)
	}

	all, err := s.RecentHistory(bg, "", 0)
	if err != nil {
		t.Fatalf("RecentHistory: %v", err)
	}
	if len(all) != 6 { // 3 建節點 + assign + transition + comment
		t.Fatalf("全庫 = %d 筆，want 6", len(all))
	}
	// 最新優先（ts 同秒時以 id desc 收尾）
	if all[0].Action != domain.ActionComment || all[0].NodeID != req.ID {
		t.Errorf("最新一筆 = %+v, want comment@req", all[0])
	}
	if all[len(all)-1].Action != domain.ActionCreate {
		t.Errorf("最舊一筆 = %+v, want 最早的 create", all[len(all)-1])
	}

	two, err := s.RecentHistory(bg, "", 2)
	if err != nil || len(two) != 2 || two[0].ID != all[0].ID {
		t.Fatalf("limit=2 = %+v (err=%v)", two, err)
	}

	// 別的專案的事件不應出現在 project 範圍內
	other := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "other"})
	scoped, err := s.RecentHistory(bg, project.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range scoped {
		if h.NodeID == other.ID {
			t.Errorf("project 過濾失效，出現 %s 的事件", other.ID)
		}
	}
	if len(scoped) != 6 {
		t.Errorf("project 範圍 = %d 筆，want 6", len(scoped))
	}
}

func TestStatsEmptyDB(t *testing.T) {
	s := newStore(t)
	st, err := s.Stats(context.Background(), "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(st.CountByStatus) != 0 || len(st.ReqProgress) != 0 || st.SelfVerifiedCount != 0 {
		t.Errorf("空庫統計 = %+v", st)
	}
}

func TestDescendants(t *testing.T) {
	children := map[string][]string{"a": {"b", "c"}, "b": {"d"}}
	got := descendants(children, "a")
	if len(got) != 3 {
		t.Fatalf("descendants = %v, want 3 筆（b c d）", got)
	}
	if len(descendants(children, "d")) != 0 {
		t.Error("葉節點應無子孫")
	}
}
