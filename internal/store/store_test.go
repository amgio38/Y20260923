package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"project_board/internal/domain"
)

// ---------- 共用 fixture ----------

// newStore：暫存 DB ＋ Migrate，測試結束自動關閉。
func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func mustCreate(t *testing.T, s *Store, actor string, in CreateInput) domain.Node {
	t.Helper()
	n, err := s.Create(context.Background(), actor, in)
	if err != nil {
		t.Fatalf("Create(%+v): %v", in, err)
	}
	return n
}

// fixtureTree：Y20260916 → REQ-ALPHA → ISSUE-ONE。
func fixtureTree(t *testing.T, s *Store) (project, req, issue domain.Node) {
	t.Helper()
	project = mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeProject, ID: "Y20260916", Title: "Y20260916", Owner: "human",
	})
	req = mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeReq, ID: project.ID + "/REQ-ALPHA", Title: "Alpha", ParentID: project.ID,
	})
	issue = mustCreate(t, s, "human", CreateInput{
		Type: domain.TypeIssue, ID: req.ID + "/ISSUE-ONE", Title: "Issue one", ParentID: req.ID,
	})
	return project, req, issue
}

func ptrTime(t time.Time) *time.Time { return &t }

// setUpdatedAt：直接改 DB 的 updated_at，讓樂觀鎖測試不依賴牆鐘精度（同秒內 nowSec 不變）。
func setUpdatedAt(t *testing.T, s *Store, id, ts string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		"UPDATE nodes SET updated_at = ? WHERE id = ?", ts, id); err != nil {
		t.Fatalf("setUpdatedAt: %v", err)
	}
}

// ---------- New / Migrate / SchemaVersion ----------

func TestNewAndMigrateCreateSchema(t *testing.T) {
	s := newStore(t)
	bg := context.Background()

	v, err := s.SchemaVersion(bg)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 6 {
		t.Fatalf("schema_version = %d, want 6", v)
	}
	for _, tbl := range []string{"nodes", "links", "history", "meta", "nodes_fts", "hooks", "node_types"} {
		var name string
		if err := s.db.QueryRowContext(bg,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", tbl).Scan(&name); err != nil {
			t.Fatalf("表 %s 不存在: %v", tbl, err)
		}
	}
	// Open 參數（DATA_MODEL.md §10）：WAL／busy_timeout=5000／foreign_keys=ON
	var journal string
	if err := s.db.QueryRowContext(bg, "PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}
	var busy int
	if err := s.db.QueryRowContext(bg, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
	var fk int
	if err := s.db.QueryRowContext(bg, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	// meta.created_at 由 0001 套用時寫入
	var created string
	if err := s.db.QueryRowContext(bg, "SELECT v FROM meta WHERE k = 'created_at'").Scan(&created); err != nil {
		t.Errorf("meta.created_at: %v", err)
	}
	if _, err := parseTime(created); err != nil {
		t.Errorf("meta.created_at 不是 RFC3339: %v", err)
	}
}

// 0006_node_types：CHECK→FK 轉換、seed 7 筆、guard 生效、未知 type 被 FK 擋。
func TestMigrateNodeTypesAndSeed(t *testing.T) {
	s := newStore(t)
	bg := context.Background()

	// nodes.type 的 DDL 真的從 CHECK enum 換成 REFERENCES node_types(key)
	var ddl string
	if err := s.db.QueryRowContext(bg,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'nodes'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "REFERENCES node_types(key)") {
		t.Errorf("nodes.type 未換成 FK：%s", ddl)
	}
	if strings.Contains(ddl, "CHECK (type IN") {
		t.Errorf("nodes.type 仍殘留 CHECK enum：%s", ddl)
	}

	// seed 7 筆，且與 domain.BuiltinTypeDefs 一字一致（DB 與 Go 內建定義不得漂移）
	rows, err := s.db.QueryContext(bg,
		"SELECT key, id_prefix, id_shape, label, parent_type, sort FROM node_types ORDER BY sort, key")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []domain.TypeDef
	for rows.Next() {
		var (
			d                 domain.TypeDef
			key, shape, label string
			parent            string
		)
		if err := rows.Scan(&key, &d.IDPrefix, &shape, &label, &parent, &d.Sort); err != nil {
			t.Fatal(err)
		}
		d.Key = domain.NodeType(key)
		d.IDShape = domain.IDShape(shape)
		d.Label = label
		d.ParentType = domain.NodeType(parent)
		got = append(got, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 {
		t.Fatalf("node_types 筆數 = %d, want 7", len(got))
	}
	if !reflect.DeepEqual(got, domain.BuiltinTypeDefs()) {
		t.Errorf("node_types seed 與 BuiltinTypeDefs 不一致:\n got=%+v\nwant=%+v", got, domain.BuiltinTypeDefs())
	}

	// FK 生效：直接寫入未知 type 必須被拒
	if _, err := s.db.ExecContext(bg,
		`INSERT INTO nodes (id, type, parent_id, title, status, owner, priority, tags, body, sort, created_at, updated_at)
		 VALUES ('X1', 'nope', NULL, 't', 'todo', 'human', 'medium', '', '', 0, '2026-09-22T00:00:00+08:00', '2026-09-22T00:00:00+08:00')`); err == nil {
		t.Error("未知 type 應被 node_types FK 擋下")
	}
}

func TestNewBadPath(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "no-such-dir", "board.db")); err == nil {
		t.Fatal("New 對不存在的目錄應回錯")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	for i := 0; i < 2; i++ {
		if err := s.Migrate(bg); err != nil {
			t.Fatalf("第 %d 次 Migrate: %v", i+1, err)
		}
	}
	v, err := s.SchemaVersion(bg)
	if err != nil || v != 6 {
		t.Fatalf("schema_version = %d (err=%v), want 6", v, err)
	}
}

func TestSchemaVersionBeforeMigrate(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	v, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 0 {
		t.Fatalf("未 migrate 的 DB schema_version = %d, want 0", v)
	}
}

func TestMigrateRejectsFutureAndBadVersion(t *testing.T) {
	bg := context.Background()
	t.Run("DB 版本比程式新", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.db.ExecContext(bg, "UPDATE meta SET v = '9' WHERE k = 'schema_version'"); err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(bg); err == nil || !strings.Contains(err.Error(), "大於") {
			t.Fatalf("Migrate = %v, want 版本過新錯誤", err)
		}
	})
	t.Run("版本值不是整數", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.db.ExecContext(bg, "UPDATE meta SET v = 'abc' WHERE k = 'schema_version'"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SchemaVersion(bg); err == nil {
			t.Fatal("SchemaVersion 應回錯")
		}
		if err := s.Migrate(bg); err == nil {
			t.Fatal("Migrate 應回錯")
		}
	})
	t.Run("meta 表存在但沒有版本列", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.db.ExecContext(bg, "DELETE FROM meta WHERE k = 'schema_version'"); err != nil {
			t.Fatal(err)
		}
		v, err := s.SchemaVersion(bg)
		if err != nil || v != 0 {
			t.Fatalf("SchemaVersion = %d (err=%v), want 0", v, err)
		}
	})
}

func TestMigrateOnClosedDB(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("對已關閉的 DB Migrate 應回錯")
	}
}

func TestApplyMigrationRollbackAndFailure(t *testing.T) {
	bg := context.Background()
	t.Run("SQL 壞掉要整筆回滾", func(t *testing.T) {
		s, err := New(filepath.Join(t.TempDir(), "board.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		if err := s.applyMigration(bg, migration{version: 1, name: "bad", file: "0001_bad.sql", sql: "NOT SQL;"}); err == nil {
			t.Fatal("壞 SQL 應回錯")
		}
		v, err := s.SchemaVersion(bg)
		if err != nil || v != 0 {
			t.Fatalf("回滾後 schema_version = %d (err=%v), want 0", v, err)
		}
	})
	t.Run("migration 把 meta 弄掉要回錯", func(t *testing.T) {
		s, err := New(filepath.Join(t.TempDir(), "board.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		m := migration{version: 1, name: "dropmeta", file: "0001_dropmeta.sql", sql: "DROP TABLE IF EXISTS meta;"}
		if err := s.applyMigration(bg, m); err == nil {
			t.Fatal("meta 不存在時寫 schema_version 應回錯")
		}
	})
}

func TestLoadMigrations(t *testing.T) {
	t.Run("內建檔", func(t *testing.T) {
		ms, err := loadMigrations(migrationFS)
		if err != nil {
			t.Fatalf("loadMigrations: %v", err)
		}
		if len(ms) != 6 || ms[0].version != 1 || ms[0].name != "init" || ms[1].version != 2 || ms[1].name != "fts" ||
			ms[2].version != 3 || ms[2].name != "hooks" || ms[3].version != 4 || ms[3].name != "item" ||
			ms[4].version != 5 || ms[4].name != "repo_link" || ms[5].version != 6 || ms[5].name != "node_types" {
			t.Fatalf("migrations = %+v, want 0001_init ＋ 0002_fts ＋ 0003_hooks ＋ 0004_item ＋ 0005_repo_link ＋ 0006_node_types", ms)
		}
		if !strings.Contains(ms[0].sql, "CREATE TABLE nodes") {
			t.Error("0001_init.sql 內容不含 CREATE TABLE nodes")
		}
		if !strings.Contains(ms[1].sql, "CREATE VIRTUAL TABLE nodes_fts") {
			t.Error("0002_fts.sql 內容不含 CREATE VIRTUAL TABLE nodes_fts")
		}
		if !strings.Contains(ms[2].sql, "CREATE TABLE hooks") {
			t.Error("0003_hooks.sql 內容不含 CREATE TABLE hooks")
		}
		if !strings.Contains(ms[3].sql, "'item'") {
			t.Error("0004_item.sql 內容不含 item 的 CHECK")
		}
		if !strings.Contains(ms[4].sql, "'repo'") {
			t.Error("0005_repo_link.sql 內容不含 repo 的 CHECK")
		}
		if !strings.Contains(ms[5].sql, "CREATE TABLE node_types") {
			t.Error("0006_node_types.sql 內容不含 CREATE TABLE node_types")
		}
	})
	cases := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"目錄不存在", fstest.MapFS{}, "read embedded migrations"},
		{"檔名不合規", fstest.MapFS{"migrations/0001-init.sql": &fstest.MapFile{Data: []byte("SELECT 1;")}}, "檔名不合規"},
		{"序號不連續", fstest.MapFS{
			"migrations/0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			"migrations/0003_c.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		}, "序號不連續"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadMigrations(tc.fsys)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("loadMigrations = %v, want 含 %q", err, tc.want)
			}
		})
	}
	t.Run("略過目錄項", func(t *testing.T) {
		ms, err := loadMigrations(fstest.MapFS{
			"migrations/0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			"migrations/sub":        &fstest.MapFile{Mode: fs.ModeDir | 0o755},
		})
		if err != nil {
			t.Fatalf("loadMigrations: %v", err)
		}
		if len(ms) != 1 {
			t.Fatalf("len = %d, want 1（目錄項要略過）", len(ms))
		}
	})
}

// ---------- 時間格式與壞資料 ----------

func TestTimeFormatting(t *testing.T) {
	ts := mustParseTime(t, "2026-09-20T01:03:00+08:00")
	if got := formatTime(ts); got != "2026-09-20T01:03:00+08:00" {
		t.Errorf("formatTime = %q", got)
	}
	// UTC 輸入要轉成台北 (+08:00)
	utc := mustParseTime(t, "2026-09-19T17:03:00Z")
	if got := formatTime(utc); got != "2026-09-20T01:03:00+08:00" {
		t.Errorf("formatTime(UTC) = %q", got)
	}
	if _, err := parseTime("not-a-time"); err == nil {
		t.Error("parseTime 應回錯")
	}
	if nowSec().Nanosecond() != 0 {
		t.Error("nowSec 應截到秒")
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := parseTime(s)
	if err != nil {
		t.Fatalf("parseTime(%q): %v", s, err)
	}
	return ts
}

func TestCorruptRowReturnsError(t *testing.T) {
	s := newStore(t)
	bg := context.Background()
	_, req, _ := fixtureTree(t, s)
	if _, err := s.db.ExecContext(bg, "UPDATE nodes SET updated_at = 'oops' WHERE id = ?", req.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Get(bg, req.ID); err == nil {
		t.Fatal("壞掉的 updated_at 應讓 Get 回錯")
	}
	if _, err := s.Tree(bg, TreeFilter{}); err == nil {
		t.Fatal("壞掉的 updated_at 應讓 Tree 回錯")
	}
	if _, err := s.History(bg, req.ID, 0); err == nil {
		t.Fatal("壞掉的 updated_at 應讓 History 回錯")
	}
}

func TestCloseTwiceSafe(t *testing.T) {
	s := newStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
}
