package store

import (
	"context"
	"path/filepath"
	"testing"

	"project_board/internal/domain"
)

// ftsFixture：project→req→issue，灌中文 body／title 與 tags（v0.2 全文搜尋用）。
func ftsFixture(t *testing.T, s *Store) (project, req, issue domain.Node) {
	t.Helper()
	project, req, issue = fixtureTree(t, s)
	reqBody := "會員中心 認證流程 說明"
	reqTags := "v0.2,ui"
	if _, err := s.Update(context.Background(), "human", req.ID,
		UpdateInput{Body: &reqBody, Tags: &reqTags}, nil); err != nil {
		t.Fatalf("Update(req): %v", err)
	}
	issueTitle := "登入查詢 admin-bff"
	issueTags := "auth,login"
	if _, err := s.Update(context.Background(), "human", issue.ID,
		UpdateInput{Title: &issueTitle, Tags: &issueTags}, nil); err != nil {
		t.Fatalf("Update(issue): %v", err)
	}
	return project, req, issue
}

func nodeIDs(ns []domain.Node) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.ID)
	}
	return out
}

func TestSearchAdvanced(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, issue := ftsFixture(t, s)
	other := mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeProject, ID: "Y20990101", Title: "別的專案 會員", Owner: "xiaoxia",
	})

	cases := []struct {
		name                       string
		query, project, tag, owner string
		want                       []string
	}{
		{"FTS 命中 body 的中文子字串", "員中心", "", "", "", []string{req.ID}},
		{"FTS 命中 body", "認證流程", "", "", "", []string{req.ID}},
		{"兩個中文字退回 LIKE 也命中", "會員中心", "", "", "", []string{req.ID}},
		{"FTS 命中 title", "admin-bff", "", "", "", []string{issue.ID}},
		{"tag 整段相符", "", "", "v0.2", "", []string{req.ID}},
		{"tag 非子字串", "", "", "v0.21", "", nil},
		{"tag 多段之一", "", "", "ui", "", []string{req.ID}},
		{"tag 命中別人的", "", "", "login", "", []string{issue.ID}},
		{"owner 全等", "", "", "", "xiaoxia", []string{other.ID}},
		{"owner 沒這個人", "員中心", "", "", "xiaoxia", nil},
		{"project 限定命中", "會員", project.ID, "", "", []string{req.ID}},
		{"project 限定排除", "會員", "Y20999999", "", "", nil},
		{"query＋tag 同時", "認證", "", "v0.2", "", []string{req.ID}},
		{"query＋tag 衝突", "認證", "", "login", "", nil},
		{"全空回全部", "", "", "", "", []string{project.ID, req.ID, issue.ID, other.ID}},
		{"LIKE 萬用字元當字面值", "%", "", "", "", nil},
		{"下引號不炸語法", `"`, "", "", "", nil},
		{"FTS 運算子字元當字面值", "admin-bff OR members", "", "", "", nil},
		{"查不到", "zzzz", "", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SearchAdvanced(bg, tc.query, tc.project, tc.tag, tc.owner)
			if err != nil {
				t.Fatalf("SearchAdvanced: %v", err)
			}
			ids := nodeIDs(got)
			if len(ids) != len(tc.want) {
				t.Fatalf("SearchAdvanced(%q,%q,%q,%q) = %v, want %v", tc.query, tc.project, tc.tag, tc.owner, ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("SearchAdvanced = %v, want %v", ids, tc.want)
				}
			}
		})
	}
}

// TestSearchLegacyDelegates：舊簽名 Search 只丟 query／project，行為要與 SearchAdvanced 一致。
func TestSearchLegacyDelegates(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	project, req, _ := ftsFixture(t, s)

	got, err := s.Search(bg, "員中心", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if ids := nodeIDs(got); len(ids) != 1 || ids[0] != req.ID {
		t.Fatalf("Search = %v, want [%s]", ids, req.ID)
	}
	if got, err = s.Search(bg, "員中心", "Y20999999"); err != nil || len(got) != 0 {
		t.Fatalf("Search 限定別專案 = %v (err=%v), want 0 筆", nodeIDs(got), err)
	}
	if got, err = s.Search(bg, "員中心", project.ID); err != nil || len(got) != 1 {
		t.Fatalf("Search 限定本專案 = %v (err=%v), want 1 筆", nodeIDs(got), err)
	}
}

// TestSearchIndexFollowsWrites：索引要跟著 Update／Delete 走（trigger 有沒有生效）。
func TestSearchIndexFollowsWrites(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, issue := ftsFixture(t, s)

	// Update：舊關鍵字查不到、新關鍵字查得到
	newBody := "改成 付款流程"
	if _, err := s.Update(bg, "human", req.ID, UpdateInput{Body: &newBody}, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SearchAdvanced(bg, "認證流程", "", "", ""); err != nil || len(got) != 0 {
		t.Fatalf("改 body 後舊關鍵字 = %v (err=%v), want 0", nodeIDs(got), err)
	}
	if got, err := s.SearchAdvanced(bg, "付款流程", "", "", ""); err != nil || len(got) != 1 {
		t.Fatalf("改 body 後新關鍵字 = %v (err=%v), want 1", nodeIDs(got), err)
	}

	// Delete：葉節點刪掉後不該再命中
	if err := s.Delete(bg, "human", issue.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, err := s.SearchAdvanced(bg, "admin-bff", "", "", ""); err != nil || len(got) != 0 {
		t.Fatalf("刪節點後 = %v (err=%v), want 0", nodeIDs(got), err)
	}
	if got, err := s.SearchAdvanced(bg, "", "", "login", ""); err != nil || len(got) != 0 {
		t.Fatalf("刪節點後 tag = %v (err=%v), want 0", nodeIDs(got), err)
	}
}

// TestFTSMigrationBackfillsExistingNodes：v1 既有 DB 升上 v2 時，舊節點要進得了索引
// （0002_fts.sql 的 INSERT…SELECT 補建；沒有這段，舊單一律搜不到）。
func TestFTSMigrationBackfillsExistingNodes(t *testing.T) {
	bg := context.Background()
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	// 只套 v1（沒有 FTS 表），模擬升級前的既有 DB。
	if err := s.applyMigration(bg, ms[0]); err != nil {
		t.Fatalf("套 0001: %v", err)
	}
	if _, err := s.db.ExecContext(bg,
		`INSERT INTO nodes (id, type, parent_id, title, status, owner, priority, tags, body, sort, created_at, updated_at)
		 VALUES ('Y20260916', 'project', NULL, '會員中心', 'in_progress', 'human', 'medium', '', '', 0, ?, ?)`,
		"2026-09-20T01:00:00+08:00", "2026-09-20T01:00:00+08:00"); err != nil {
		t.Fatal(err)
	}

	// 升級到最新
	if err := s.Migrate(bg); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := s.SchemaVersion(bg)
	if err != nil || v != 6 {
		t.Fatalf("schema_version = %d (err=%v), want 6", v, err)
	}
	got, err := s.SearchAdvanced(bg, "員中心", "", "", "")
	if err != nil {
		t.Fatalf("SearchAdvanced: %v", err)
	}
	if ids := nodeIDs(got); len(ids) != 1 || ids[0] != "Y20260916" {
		t.Fatalf("升級後既有節點 = %v, want ['Y20260916']", ids)
	}

	// 升級後新寫入也要被索引（trigger 有沒有建起來）
	mustCreate(t, s, "human", CreateInput{Type: domain.TypeProject, ID: "Y20260917", Title: "升級後新增 付款"})
	if got, err := s.SearchAdvanced(bg, "付款", "", "", ""); err != nil || len(got) != 1 {
		t.Fatalf("升級後新增 = %v (err=%v), want 1", nodeIDs(got), err)
	}
}
