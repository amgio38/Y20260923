package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"project_board/internal/domain"
)

func TestSeed(t *testing.T) {
	s := newStore(t)
	bg := context.Background()

	if err := s.Seed(bg, "human"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	n, links, children, err := s.Get(bg, "Y20260916")
	if err != nil {
		t.Fatalf("Get 種子: %v", err)
	}
	if n.Type != domain.TypeProject || n.Status != domain.StatusInProgress || n.Owner != "human" {
		t.Errorf("種子節點 = %+v", n)
	}
	if n.Priority != domain.PriorityMedium || n.ParentID != "" {
		t.Errorf("種子屬性 = %+v", n)
	}
	if len(links) != 0 || len(children) != 0 {
		t.Errorf("種子應無 link／子節點: %v %v", links, children)
	}
	h := mustHistory(t, s, "Y20260916", 0)
	if len(h) != 2 || h[0].Action != domain.ActionTransition || h[0].FromVal != "todo" || h[0].ToVal != "in_progress" {
		t.Fatalf("種子 history = %+v", h)
	}
	if h[1].Action != domain.ActionCreate || h[1].Actor != "human" {
		t.Errorf("種子 create 事件 = %+v", h[1])
	}

	// 重複跑＝no-op（`pb init && pb seed` 可重複執行）
	if err := s.Seed(bg, "human"); err != nil {
		t.Fatalf("第二次 Seed: %v", err)
	}
	if got := len(mustHistory(t, s, "Y20260916", 0)); got != 2 {
		t.Errorf("重複 Seed 不應再寫 history: %d", got)
	}

	// actor 檢查
	if err := s.Seed(bg, ""); !errors.Is(err, ErrMissingActor) {
		t.Errorf("err = %v, want ErrMissingActor", err)
	}
	if err := s.Seed(bg, "nobody"); !errors.Is(err, domain.ErrInvalidOwner) {
		t.Errorf("err = %v, want domain.ErrInvalidOwner", err)
	}
}

func TestSeedWithoutMigrate(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Seed(context.Background(), "human"); err == nil {
		t.Fatal("尚未 migrate 的 DB 應回錯")
	}
}
