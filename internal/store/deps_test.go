package store

import (
	"context"
	"testing"

	"project_board/internal/domain"
)

func TestListDependsOn(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, _, issue := fixtureTree(t, s)

	// 第二個專案（驗 project 過濾）
	other := mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "別的專案"})
	oreq := mustCreate(t, s, "human", CreateInput{Type: domain.TypeReq, ID: other.ID + "/REQ-X", Title: "X", ParentID: other.ID})
	oissue := mustCreate(t, s, "human", CreateInput{Type: domain.TypeIssue, ID: oreq.ID + "/ISSUE-Y", Title: "Y", ParentID: oreq.ID})

	mustLink(t, s, "human", issue.ID, domain.LinkDependsOn, oissue.ID, "等對方")
	mustLink(t, s, "human", oissue.ID, domain.LinkDependsOn, issue.ID, "")
	mustLink(t, s, "human", issue.ID, domain.LinkFile, "dev_docs/x.md", "非依賴") // 別的 kind 不該出現
	mustLink(t, s, "human", oreq.ID, domain.LinkCommit, "abc1234", "非依賴")      // 同上

	all, err := s.ListDependsOn(bg, "")
	if err != nil {
		t.Fatalf("ListDependsOn: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("全庫 = %d 筆, want 2（只回 depends_on）", len(all))
	}
	// 依 link id 排序，且欄位是 Links 的那三個
	if all[0].FromID != issue.ID || all[0].Target != oissue.ID || all[0].Note != "等對方" {
		t.Errorf("第一筆 = %+v", all[0])
	}
	if all[0].Kind != domain.LinkDependsOn || all[0].ID <= 0 || all[0].CreatedAt.IsZero() {
		t.Errorf("第一筆欄位不全 = %+v", all[0])
	}

	got, err := s.ListDependsOn(bg, project.ID)
	if err != nil || len(got) != 1 || got[0].FromID != issue.ID {
		t.Fatalf("限定專案 = %+v (err=%v), want 只有 %s 出發的那筆", got, err, issue.ID)
	}
	got, err = s.ListDependsOn(bg, other.ID)
	if err != nil || len(got) != 1 || got[0].FromID != oissue.ID {
		t.Fatalf("限定另一專案 = %+v (err=%v), want 只有 %s 出發的那筆", got, err, oissue.ID)
	}
	// 子樹比對是 id 前綴：REQ 也吃得到自己樹下的依賴
	got, err = s.ListDependsOn(bg, oreq.ID)
	if err != nil || len(got) != 1 || got[0].FromID != oissue.ID {
		t.Fatalf("限定 REQ 子樹 = %+v (err=%v)", got, err)
	}
	if got, err = s.ListDependsOn(bg, "Y20999999"); err != nil || len(got) != 0 {
		t.Fatalf("查無此專案 = %+v (err=%v), want 0 筆", got, err)
	}
}

func TestListDependsOnEmpty(t *testing.T) {
	s := newStore(t)
	fixtureTree(t, s)
	got, err := s.ListDependsOn(context.Background(), "")
	if err != nil {
		t.Fatalf("ListDependsOn: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("沒有任何依賴 = %d 筆, want 0", len(got))
	}
}

// TestListDependsOnPrefixNotSubstring：project 比對是「id 本身或 id + "/" 前綴」，
// Y20260916/REQ-ALPHA 不該吃到同層的 Y20260916/REQ-ALPHA2。
func TestListDependsOnPrefixNotSubstring(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, _, issue := fixtureTree(t, s)
	req2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-ALPHA2", Title: "Alpha2", ParentID: project.ID,
	})
	issue2 := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req2.ID + "/ISSUE-TWO", Title: "Issue two", ParentID: req2.ID,
	})
	mustLink(t, s, "human", issue2.ID, domain.LinkDependsOn, issue.ID, "")

	got, err := s.ListDependsOn(bg, req2.ID[:len(req2.ID)-1]) // "…/REQ-ALPH"：只差最後一字，不算前綴
	if err != nil {
		t.Fatalf("ListDependsOn: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("= %+v, want 0 筆（REQ-ALPHA2 不在 REQ-ALPH 底下）", got)
	}
	if got, err = s.ListDependsOn(bg, req2.ID); err != nil || len(got) != 1 || got[0].FromID != issue2.ID {
		t.Fatalf("= %+v (err=%v), want 1 筆 %s", got, err, issue2.ID)
	}
	if got, err = s.ListDependsOn(bg, project.ID); err != nil || len(got) != 1 {
		t.Fatalf("= %+v (err=%v), want 1 筆（專案子樹含 REQ-ALPHA2）", got, err)
	}
}
