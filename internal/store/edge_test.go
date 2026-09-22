package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"project_board/internal/domain"
)

// 這一檔補「防禦性分支」：壞資料、已關閉的連線、部分初始化的 DB、孤兒節點。
// 這些情境真實世界不該發生，但程式不能 panic 或默默吞掉。

func TestWritesAfterClose(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	body := "x"
	if _, err := s.Update(bg, "human", req.ID, UpdateInput{Body: &body}, nil); err == nil {
		t.Error("關閉後 Update 應回錯")
	}
	if _, err := s.Create(bg, "human", CreateInput{Type: domain.TypeProject, ID: "Y20990101", Title: "t"}); err == nil {
		t.Error("關閉後 Create 應回錯")
	}
	if _, _, _, err := s.Get(bg, req.ID); err == nil {
		t.Error("關閉後 Get 應回錯")
	}
}

func TestMigrateFailsOnPartiallyInitializedDB(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	bg := context.Background()
	if _, err := s.db.ExecContext(bg, "CREATE TABLE nodes (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(bg); err == nil {
		t.Fatal("已有 nodes 表時套 0001 應失敗（且整筆回滾）")
	}
}

func TestInsertHistoryRejectsUnknownNode(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	err := s.inTx(bg, func(tx *sql.Tx) error {
		return insertHistory(bg, tx, nowSec(), historyEntry{
			nodeID: "Y20990101/NOPE", actor: "human", action: domain.ActionComment,
		})
	})
	if err == nil {
		t.Fatal("FK 應擋下不存在節點的 history")
	}
	var n int
	if err := s.db.QueryRowContext(bg, "SELECT COUNT(*) FROM history").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("不應留下任何 history，實際 %d 筆", n)
	}
}

func TestCorruptTimeFields(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, issue := fixtureTree(t, s)
	mustLink(t, s, "human", issue.ID, domain.LinkFile, "f", "")

	if _, err := s.db.ExecContext(bg, "UPDATE nodes SET created_at = 'x' WHERE id = ?", req.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get(bg, req.ID); err == nil {
		t.Error("壞掉的 created_at 應讓 Get 回錯")
	}

	if _, err := s.db.ExecContext(bg, "UPDATE links SET created_at = 'x' WHERE from_id = ?", issue.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get(bg, issue.ID); err == nil {
		t.Error("壞掉的 link created_at 應讓 Get 回錯")
	}
}

// 孤兒節點（parent 已被直接刪掉）：Tree／Stats 必須容忍，不可 panic。
func TestOrphanNodeTolerated(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := fixtureTree(t, s)

	// 繞過 FK 直接刪中層，做出真實世界不該出現的孤兒
	if _, err := s.db.ExecContext(bg, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(bg, "DELETE FROM nodes WHERE id = ?", req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(bg, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	tree, err := s.Tree(bg, TreeFilter{Project: project.ID})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree) != 1 || tree[0].ID != project.ID {
		t.Fatalf("Tree = %v, want 只有 project（孤兒不屬子樹）", tree)
	}
	if _, err := s.Tree(bg, TreeFilter{Status: string(domain.StatusTodo)}); err != nil {
		t.Fatalf("Tree(status): %v", err)
	}
	if _, err := s.Stats(bg, ""); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if n, _, _, err := s.Get(bg, issue.ID); err != nil || n.ParentID != req.ID {
		t.Fatalf("孤兒節點仍應可讀: %v", err)
	}
}
