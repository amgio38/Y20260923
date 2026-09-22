package store

import (
	"context"
	"errors"
	"testing"

	"project_board/internal/domain"
)

func TestLinkKindsAndHistory(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	l := mustLink(t, s, "xiaoxia", issue.ID, domain.LinkDependsOn, project.ID, "等專案")
	if l.ID <= 0 || l.FromID != issue.ID || l.Kind != domain.LinkDependsOn || l.Target != project.ID || l.Note != "等專案" {
		t.Fatalf("link = %+v", l)
	}
	if l.CreatedAt.IsZero() {
		t.Error("CreatedAt 應有值")
	}
	h := mustHistory(t, s, issue.ID, 1)[0]
	if h.Action != domain.ActionLink || h.Field != "depends_on" || h.ToVal != project.ID || h.Actor != "xiaoxia" {
		t.Errorf("link 事件 = %+v", h)
	}

	// 其餘 kind 指系統外字串，不驗證 target
	for _, k := range []domain.LinkKind{domain.LinkFile, domain.LinkCommit, domain.LinkPR, domain.LinkURL, domain.LinkDoc} {
		if _, err := s.Link(bg, "human", req.ID, k, "whatever-"+string(k), ""); err != nil {
			t.Fatalf("Link(%s): %v", k, err)
		}
	}
	_, links, _, err := s.Get(bg, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 5 {
		t.Fatalf("links = %d, want 5", len(links))
	}
	if links[0].Kind != domain.LinkFile {
		t.Errorf("links 應依 id 排序，第一筆 = %+v", links[0])
	}
}

func TestLinkErrors(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, issue := fixtureTree(t, s)

	if _, err := s.Link(bg, "human", issue.ID, domain.LinkDependsOn, "Y20990101/NOPE", ""); !errors.Is(err, ErrDependsOnTargetMissing) {
		t.Errorf("err = %v, want ErrDependsOnTargetMissing", err)
	}
	if _, err := s.Link(bg, "human", issue.ID, domain.LinkKind("bogus"), "x", ""); err == nil {
		t.Error("非法 kind 應回錯")
	}
	if _, err := s.Link(bg, "human", issue.ID, domain.LinkFile, "", ""); err == nil {
		t.Error("空 target 應回錯")
	}
	if _, err := s.Link(bg, "human", "Y20990101/NOPE", domain.LinkFile, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if done := len(mustHistory(t, s, req.ID, 0)); done != 1 {
		t.Errorf("失敗的 link 不應留 history: %d", done)
	}
}

func TestUnlink(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, _, issue := fixtureTree(t, s)

	l := mustLink(t, s, "xiaoxia", issue.ID, domain.LinkCommit, "8094064", "")
	if err := s.Unlink(bg, "claude", l.ID); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	_, links, _, err := s.Get(bg, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("links = %+v, want 空", links)
	}
	h := mustHistory(t, s, issue.ID, 1)[0]
	if h.Action != domain.ActionUnlink || h.Field != "commit" || h.FromVal != "8094064" || h.Actor != "claude" {
		t.Errorf("unlink 事件 = %+v", h)
	}
	if err := s.Unlink(bg, "claude", 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
