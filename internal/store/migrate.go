package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"sync"
)

// migration 機制（DATA_MODEL.md §11.7）：內建循序 SQL，`meta.schema_version` 驅動，
// 每筆 migration 一個 transaction（失敗就整筆回滾，可安全重跑）。
//
//go:embed migrations/*.sql
var migrationFS embed.FS

const schemaVersionKey = "schema_version"

type migration struct {
	version int
	name    string
	file    string
	sql     string
}

var migrationNameRe = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// loadMigrations 讀內建 migration 並檢查序號連續（0001, 0002, …），避免漏檔造成版本錯亂。
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration 檔名不合規（應為 NNNN_name.sql）: %s", e.Name())
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration 序號 %q: %w", m[1], err)
		}
		body, err := fs.ReadFile(fsys, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: m[2], file: e.Name(), sql: string(body)})
	}
	if len(out) == 0 {
		return nil, errors.New("內建 migration 為空")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration 序號不連續：第 %d 筆為 %04d_%s", i+1, m.version, m.name)
		}
	}
	return out, nil
}

var migrations = sync.OnceValues(func() ([]migration, error) { return loadMigrations(migrationFS) })

// SchemaVersion 讀 `meta.schema_version`（DB 尚未 migrate 過時回 0）。
// 契約 §2 未列，屬附加方法，供 REST `/healthz` 等處使用。
func (s *Store) SchemaVersion(ctx context.Context) (int, error) { return s.schemaVersion(ctx) }

func (s *Store) schemaVersion(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'").Scan(&n); err != nil {
		return 0, fmt.Errorf("check meta table: %w", err)
	}
	if n == 0 {
		return 0, nil // 全新 DB：meta 由 0001 建立
	}
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = ?", schemaVersionKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read meta.%s: %w", schemaVersionKey, err)
	}
	iv, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("meta.%s=%q 不是整數", schemaVersionKey, v)
	}
	return iv, nil
}

// Migrate 依 meta.schema_version 依序套用缺的 migration（API_CONTRACT.md §2／DATA_MODEL.md §11.7）。
// `pb init`／`serve`／`mcp` 啟動時都要呼叫，可重複執行。
func (s *Store) Migrate(ctx context.Context) error {
	ms, err := migrations()
	if err != nil {
		return err
	}
	cur, err := s.schemaVersion(ctx)
	if err != nil {
		return err
	}
	if cur > len(ms) {
		return fmt.Errorf("DB schema_version=%d 大於程式內建最新版 %d：請更新程式（不自行降版）", cur, len(ms))
	}
	for _, m := range ms {
		if m.version <= cur {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.file, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
			schemaVersionKey, strconv.Itoa(m.version)); err != nil {
			return fmt.Errorf("set %s: %w", schemaVersionKey, err)
		}
		if m.version == 1 {
			// meta 表由 0001 建立，順手記首次建庫時間（DATA_MODEL.md §5）。
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO meta (k, v) VALUES ('created_at', ?)`,
				formatTime(nowSec())); err != nil {
				return fmt.Errorf("set created_at: %w", err)
			}
		}
		return bumpSchemaCookie(ctx, tx)
	})
}

// bumpSchemaCookie：把 schema cookie 推一格，強制所有連線重讀 sqlite_master。
//
// 為什麼需要：0004_item 這種 migration 是「直接改 sqlite_master 的 DDL」（SQLite 不能 ALTER 掉
// 既有 CHECK，而重建表會讓 ON DELETE CASCADE 帶走資料）——改 sqlite_master 本身不會動 schema
// cookie，於是本連線（甚至其他行程）會繼續用舊的 compiled schema（實際踩過：migration 過了，
// 但 INSERT type='item' 仍被舊 CHECK 擋下）。這裡統一在每筆 migration 之後推一格；
// 對一般 migration 只是多一次 schema 重載，可重複執行、無資料副作用。
func bumpSchemaCookie(ctx context.Context, tx *sql.Tx) error {
	var cur int
	if err := tx.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&cur); err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA schema_version = "+strconv.Itoa(cur+1)); err != nil {
		return fmt.Errorf("bump schema_version: %w", err)
	}
	return nil
}
