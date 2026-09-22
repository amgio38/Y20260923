// Package store：ProjectBoard 的 SQLite 儲存層。
// 簽名權威：dev_docs/API_CONTRACT.md §2；語意權威：docs/DATA_MODEL.md（含 §10 併發、§11 CTO 補充）。
//
// 寫入紀律（DATA_MODEL.md §10）：每個寫入函式＝一個 transaction，且 nodes 變更與 history
// 同一個 transaction 寫入（保證「狀態變了必有事件」）。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"project_board/internal/domain"

	_ "modernc.org/sqlite" // 純 Go driver：不引入 cgo、不依賴系統 libsqlite
)

// sentinel error（API_CONTRACT.md §2）：CLI／MCP／httpapi 一律用 errors.Is 判斷，不比對字串。
var (
	ErrNotFound               = errors.New("not found")
	ErrIDExists               = errors.New("id already exists")
	ErrMissingActor           = errors.New("missing actor")
	ErrConflict               = errors.New("conflict: node modified since read")
	ErrDependsOnTargetMissing = errors.New("depends_on target does not exist")
	ErrCannotDelete           = errors.New("node has children or links, cannot delete")
	ErrNotInReview            = errors.New("verify requires status=review")
	// ErrParentTypeMismatch：節點的 parent 型別不符 node_types.parent_type（item 另有 ErrItemParentNotReq 以保留既有訊息）。
	ErrParentTypeMismatch = errors.New("parent type mismatch")
)

// Store 持有一個 SQLite 連線池。
type Store struct {
	db *sql.DB
}

// TreeFilter：Tree 的過濾條件（API_CONTRACT.md §2 ＋ §5-1）。
type TreeFilter struct {
	Project, Status, Owner string
	Tag                    string // 空字串＝不過濾；非空＝tags 欄位含此字串（子字串比對，§5-1）
	Type                   domain.NodeType
	Depth                  int // 0 = 不限
}

// CreateInput：Create 的輸入（API_CONTRACT.md §2）；ID 空字串 → 用 domain.GenerateID。
type CreateInput struct {
	Type                        domain.NodeType
	Title, ParentID, ID         string
	Owner, Priority, Tags, Body string
}

// UpdateInput：Update 的部分更新輸入（API_CONTRACT.md §2）；nil 代表不改該欄位。
type UpdateInput struct {
	Title, Body, Owner, Priority, Tags *string
	Sort                               *int
}

// Stats：Stats 的回傳（API_CONTRACT.md §2；CountByOwner 語意見 §5-3）。
type Stats struct {
	CountByStatus map[domain.Status]int
	// CountByOwner：目前**未結案**張數（status 不是 done 也不是 cancel），對應 dashboard「每人手上張數」（§5-3）。
	CountByOwner map[string]int
	ReqProgress  map[string]float64 // req id -> 完成度 0~1
	// AvgDwellDays：每人**未結案**節點的平均滯留天數（now - created_at，一位小數）。
	// 該 owner 沒有未結案節點時**不出現**在 map 裡（不除以零）；時鐘由呼叫端給（StatsAt）。
	AvgDwellDays      map[string]float64
	SelfVerifiedCount int // §11.2
}

// taipei：台北固定 +08:00。台灣自 1979 年起無日光節約，用 FixedZone 即正確，
// 也免掉容器內沒 tzdata 時 time.LoadLocation 失敗的問題。
var taipei = time.FixedZone("Asia/Taipei", 8*60*60)

// timeFormat：INTERFACE.md §3 範例格式（2026-09-20T01:03:00+08:00）。
const timeFormat = time.RFC3339

func formatTime(t time.Time) string { return t.In(taipei).Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t, nil
}

// nowSec：時間統一由程式寫入，且存成秒級（RFC3339 無小數）。
// 若不截秒，回給呼叫端的 UpdatedAt（含奈米）會與 DB 值不等，害樂觀鎖誤判衝突。
func nowSec() time.Time { return time.Now().Truncate(time.Second) }

// round1：四捨五入到小數 1 位（平均滯留天數的精度；REQ-V03-PROGRESS 裁示「一位小數」）。
func round1(f float64) float64 { return math.Round(f*10) / 10 }

// New 開檔＋設定 WAL／busy_timeout=5000／foreign_keys=ON，不跑 migration（API_CONTRACT.md §2）。
// pragma 走 DSN，確保連線池中每個新連線都套用同一組設定。
func New(dbPath string) (*Store, error) {
	dsn := "file:" + dbPath + "?" + strings.Join([]string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
	}, "&")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	// 單機內用、單一 writer：把連線數收成 1，最單純地序列化存取（免同 process 內的鎖競爭）。
	// 多 process 共用同一顆 DB 仍靠 WAL ＋ busy_timeout 容許（OPERATIONS.md §4）。
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", dbPath, err)
	}
	return &Store{db: db}, nil
}

// Close 關閉底層連線（CLI／server 收工用）。契約 §2 未列，屬附加方法，不動既有簽名。
func (s *Store) Close() error { return s.db.Close() }

// validateActor：契約 §2 規定所有寫入函式的第一件事。
func validateActor(actor string) error {
	if actor == "" {
		return ErrMissingActor
	}
	if !domain.IsValidOwner(actor) {
		return domain.ErrInvalidOwner
	}
	return nil
}

// validPriority：DB 也有 CHECK，這裡先擋一次以回可讀錯誤。
func validPriority(p string) bool {
	switch domain.Priority(p) {
	case domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow:
		return true
	}
	return false
}

// inTx 把 fn 包進單一 transaction；fn 回錯即 rollback。
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
