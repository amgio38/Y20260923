package store

import (
	"context"
	"testing"

	"project_board/internal/domain"
)

// TestSearchFilteredStatuses：v0.3 的 status 多選。
//
// 「我的未結單」＝Owner＋Statuses{todo,in_progress,review,blocked}；關鍵是**不補祖先**——
// 這正是它與 Tree（畫樹會帶出父層）的差異。
func TestSearchFilteredStatuses(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := ftsFixture(t, s)

	// req：xiaoxia／in_progress；issue：xiaoxia／todo；project：human／in_progress。
	if _, err := s.Assign(bg, "human", req.ID, "xiaoxia"); err != nil {
		t.Fatalf("Assign(req): %v", err)
	}
	if _, err := s.Transition(bg, "human", req.ID, domain.StatusInProgress, "", nil); err != nil {
		t.Fatalf("Transition(req): %v", err)
	}
	if _, err := s.Assign(bg, "human", issue.ID, "xiaoxia"); err != nil {
		t.Fatalf("Assign(issue): %v", err)
	}

	open := []domain.Status{domain.StatusTodo, domain.StatusInProgress, domain.StatusReview, domain.StatusBlocked}
	cases := []struct {
		name string
		f    SearchFilter
		want []string
	}{
		{"status 多選＝我的未結單", SearchFilter{Owner: "xiaoxia", Statuses: open}, []string{req.ID, issue.ID}},
		{"status 單選", SearchFilter{Owner: "xiaoxia", Statuses: []domain.Status{domain.StatusTodo}}, []string{issue.ID}},
		{"集合外即排除", SearchFilter{Owner: "xiaoxia", Statuses: []domain.Status{domain.StatusDone}}, nil},
		{"status 空＝不過濾（同 owner）", SearchFilter{Owner: "xiaoxia"}, []string{req.ID, issue.ID}},
		{"project＋owner＋status 交集", SearchFilter{Project: project.ID, Owner: "xiaoxia",
			Statuses: []domain.Status{domain.StatusInProgress}}, []string{req.ID}},
		{"只給 status（不帶 owner，別人也算）", SearchFilter{Statuses: []domain.Status{domain.StatusInProgress}},
			[]string{req.ID}},
		{"不補祖先：子孫命中不帶出 project", SearchFilter{Owner: "xiaoxia",
			Statuses: []domain.Status{domain.StatusTodo}}, []string{issue.ID}},
		{"owner 沒這個人＋status", SearchFilter{Owner: "kaimadi", Statuses: open}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SearchFiltered(bg, tc.f)
			if err != nil {
				t.Fatalf("SearchFiltered: %v", err)
			}
			ids := nodeIDs(got)
			if len(ids) != len(tc.want) {
				t.Fatalf("SearchFiltered(%+v) = %v, want %v", tc.f, ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("SearchFiltered(%+v) = %v, want %v", tc.f, ids, tc.want)
				}
			}
		})
	}
}

// TestSearchAdvancedMatchesFiltered：SearchAdvanced（v0.2 四參數）＝SearchFiltered 留空 Statuses，
// 保住舊呼叫端的語意（回歸護欄）。
func TestSearchAdvancedMatchesFiltered(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := ftsFixture(t, s)

	old, err := s.SearchAdvanced(bg, "認證", "", "v0.2", "")
	if err != nil {
		t.Fatalf("SearchAdvanced: %v", err)
	}
	newer, err := s.SearchFiltered(bg, SearchFilter{Query: "認證", Tag: "v0.2"})
	if err != nil {
		t.Fatalf("SearchFiltered: %v", err)
	}
	if got, want := nodeIDs(newer), nodeIDs(old); len(got) != len(want) || (len(got) == 1 && got[0] != want[0]) {
		t.Fatalf("SearchFiltered = %v, SearchAdvanced = %v，兩者應一致", got, want)
	}
	if len(old) != 1 || old[0].ID != req.ID {
		t.Fatalf("fixture 前置壞了：SearchAdvanced = %v", nodeIDs(old))
	}
}
