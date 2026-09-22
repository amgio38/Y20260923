package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"project_board/internal/domain"
)

// 來源：Y20260920/REQ-V03-HOOK 的 PO 裁示（2026-09-20）。
//
// 目的：ISSUE 狀態一變，就把訂了這張單的 harness 叫醒（安全網，不是免報）。
//   - 訂閱存 SQLite，重啟還在；harness 這輪只收 herdr。
//   - 改狀態的行程**不准**呼叫 herdr：本檔只負責「誰該被叫、要講什麼」，
//     真正的喚醒由長駐的 serve 做（cmd/pb 的 Waker 介面）。
//   - hook.node_id 等於異動節點或它的祖先；history.actor 等於 hook.actor 時不叫（自己改的不叫自己）。

const (
	// HarnessHerdr：這輪唯一支援的 harness。
	HarnessHerdr = "herdr"

	// hookCursorKey：喚醒游標（meta 表）——值為已處理過的 history.id。
	hookCursorKey = "hook_cursor"
)

var (
	// ErrMissingNode：寫入時少帶 node_id（hook 等）。
	ErrMissingNode = errors.New("missing node id")
	// ErrUnknownHarness：harness 不在允許清單。
	ErrUnknownHarness = errors.New("unknown harness")
	// ErrInvalidTarget：target 不符 ^[a-z][a-z0-9_-]{0,31}$。
	ErrInvalidTarget = errors.New("invalid target")
)

// hookTargetRe：target 是 herdr 的 agent 名（裁示：必須符合 ^[a-z][a-z0-9_-]{0,31}$）。
var hookTargetRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Hook：一筆訂閱（node_id 或其子孫的狀態變動時，喚醒 harness 的 target）。
type Hook struct {
	ID        int64
	NodeID    string
	Actor     string
	Harness   string
	Target    string
	CreatedAt time.Time
}

// HookInput：Hook 的輸入。
type HookInput struct {
	Actor, NodeID, Harness, Target string
}

const hookCols = "id, node_id, actor, harness, target, created_at"

func scanHook(sc interface{ Scan(...any) error }) (Hook, error) {
	var (
		h  Hook
		ts string
	)
	if err := sc.Scan(&h.ID, &h.NodeID, &h.Actor, &h.Harness, &h.Target, &ts); err != nil {
		return Hook{}, err
	}
	parsed, err := parseTime(ts)
	if err != nil {
		return Hook{}, err
	}
	h.CreatedAt = parsed
	return h, nil
}

// validateHook：actor／harness／target 的合法性（寫入前先擋，回可讀錯誤）。
func validateHook(in HookInput) error {
	if err := validateActor(in.Actor); err != nil {
		return err
	}
	if in.NodeID == "" {
		return fmt.Errorf("hook: %w", ErrMissingNode)
	}
	if in.Harness != HarnessHerdr {
		return fmt.Errorf("hook: harness %q: %w（這輪只收 %s）", in.Harness, ErrUnknownHarness, HarnessHerdr)
	}
	if !hookTargetRe.MatchString(in.Target) {
		return fmt.Errorf("hook: target %q: %w（^[a-z][a-z0-9_-]{0,31}$）", in.Target, ErrInvalidTarget)
	}
	return nil
}

// Hook：訂閱一個節點（或其子孫）的狀態變動。同一 (node_id, harness, target) 重複訂閱＝更新訂閱者。
func (s *Store) Hook(ctx context.Context, in HookInput) (Hook, error) {
	if err := validateHook(in); err != nil {
		return Hook{}, err
	}
	var out Hook
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := getNodeQ(ctx, tx, in.NodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO hooks (node_id, actor, harness, target, created_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (node_id, harness, target)
			 DO UPDATE SET actor = excluded.actor, created_at = excluded.created_at`,
			in.NodeID, in.Actor, in.Harness, in.Target, formatTime(nowSec())); err != nil {
			return fmt.Errorf("hook %s → %s: %w", in.NodeID, in.Target, err)
		}
		h, err := scanHook(tx.QueryRowContext(ctx,
			"SELECT "+hookCols+" FROM hooks WHERE node_id = ? AND harness = ? AND target = ?",
			in.NodeID, in.Harness, in.Target))
		if err != nil {
			return fmt.Errorf("read hook: %w", err)
		}
		out = h
		return nil
	})
	if err != nil {
		return Hook{}, err
	}
	return out, nil
}

// Unhook：取消訂閱；不存在回 ErrNotFound（不靜默成功）。
func (s *Store) Unhook(ctx context.Context, actor, nodeID, harness, target string) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	if harness != HarnessHerdr {
		return fmt.Errorf("unhook: harness %q: %w", harness, ErrUnknownHarness)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"DELETE FROM hooks WHERE node_id = ? AND harness = ? AND target = ?", nodeID, harness, target)
		if err != nil {
			return fmt.Errorf("unhook %s: %w", nodeID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("unhook %s: %w", nodeID, err)
		}
		if n == 0 {
			return fmt.Errorf("hook %s → %s/%s: %w", nodeID, harness, target, ErrNotFound)
		}
		return nil
	})
}

// Hooks：列出訂閱（nodeID 空字串＝全部）；依 node_id、harness、target 排序。
func (s *Store) Hooks(ctx context.Context, nodeID string) ([]Hook, error) {
	q := "SELECT " + hookCols + " FROM hooks"
	args := []any{}
	if nodeID != "" {
		q += " WHERE node_id = ?"
		args = append(args, nodeID)
	}
	q += " ORDER BY node_id, harness, target"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list hooks: %w", err)
	}
	defer rows.Close()
	var out []Hook
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			return nil, fmt.Errorf("list hooks: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hooks: %w", err)
	}
	return out, nil
}

// HookCursor：喚醒游標（meta.hook_cursor）；沒跑過＝0。
func (s *Store) HookCursor(ctx context.Context) (int64, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = ?", hookCursorKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read meta.%s: %w", hookCursorKey, err)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("meta.%s=%q 不是整數", hookCursorKey, v)
	}
	return n, nil
}

// SetHookCursor：把游標推到 id（serve 處理完後呼叫）。只前進，不後退。
func (s *Store) SetHookCursor(ctx context.Context, id int64) error {
	if id < 0 {
		return fmt.Errorf("hook cursor %d 不可為負", id)
	}
	cur, err := s.HookCursor(ctx)
	if err != nil {
		return err
	}
	if id < cur {
		return nil // 不後退（重跑舊游標不會把已喚醒的事件再拉回來）
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
			hookCursorKey, strconv.FormatInt(id, 10)); err != nil {
			return fmt.Errorf("set meta.%s: %w", hookCursorKey, err)
		}
		return nil
	})
}

// HookEvent：一次「該喚醒誰、講什麼」。
type HookEvent struct {
	Event   domain.HistoryEntry // 觸發的狀態異動
	Hook    Hook                // 命中的訂閱
	Message string              // 要送給 target 的訊息（HookMessage 產生）
}

// PendingHookEvents：讀 cursor 之後的狀態異動（transition／verify），配對訂閱，
// 回傳待喚醒清單與新游標。純讀，不喚醒、不改狀態（喚醒是 serve 的事）。
//
// 命中規則（裁示）：訂閱的 node_id 等於異動節點，或異動節點在它的子樹裡（id 路徑前綴）；
// history.actor 等於訂閱者時略過（自己改的不叫自己）。
func (s *Store) PendingHookEvents(ctx context.Context, cursor int64) ([]HookEvent, int64, error) {
	var maxID int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM history").Scan(&maxID); err != nil {
		return nil, cursor, fmt.Errorf("max history id: %w", err)
	}
	hooks, err := s.Hooks(ctx, "")
	if err != nil {
		return nil, cursor, err
	}
	if len(hooks) == 0 || maxID <= cursor {
		return nil, max(cursor, maxID), nil
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+historyCols+" FROM history h WHERE h.id > ? AND h.action IN (?, ?) AND h.field = ? ORDER BY h.id",
		cursor, string(domain.ActionTransition), string(domain.ActionVerify), "status")
	if err != nil {
		return nil, cursor, fmt.Errorf("pending hook history: %w", err)
	}
	events, err := scanHistoryRows(rows, "pending hooks")
	if err != nil {
		return nil, cursor, err
	}
	var out []HookEvent
	// hooks 依 (node_id, harness, target) 排序；先比 node_id 長度再字典序，讓父節點先叫（穩定輸出）。
	sort.SliceStable(hooks, func(i, j int) bool {
		if len(hooks[i].NodeID) != len(hooks[j].NodeID) {
			return len(hooks[i].NodeID) < len(hooks[j].NodeID)
		}
		return hooks[i].NodeID < hooks[j].NodeID
	})
	for _, e := range events {
		for _, h := range hooks {
			if e.Actor == h.Actor || !hookCovers(h.NodeID, e.NodeID) {
				continue
			}
			out = append(out, HookEvent{Event: e, Hook: h, Message: HookMessage(e)})
		}
	}
	return out, maxID, nil
}

// hookCovers：訂閱的 nodeID 是否涵蓋異動節點（同一顆或它的祖先）。
func hookCovers(hookNode, eventNode string) bool {
	return eventNode == hookNode || strings.HasPrefix(eventNode, hookNode+"/")
}

// HookMessage：把一次狀態異動翻成喚醒訊息（裁示的四種寫法）。
//   - to=review：請對方看單驗收
//   - from=review 且 to=in_progress：退回修改，叫對方先讀單再改
//   - action=verify：已收成 done
//   - 其餘：from→to 與 note
func HookMessage(e domain.HistoryEntry) string {
	var b strings.Builder
	b.WriteString("[ProjectBoard] ")
	b.WriteString(e.NodeID)
	switch {
	case e.Action == domain.ActionVerify:
		fmt.Fprintf(&b, " 已驗收成 done（%s）", e.Actor)
		appendNote(&b, "驗收說明", e.Note)
		b.WriteString("。請看單。")
	case e.ToVal == string(domain.StatusReview):
		fmt.Fprintf(&b, " 進 review（%s）", e.Actor)
		appendNote(&b, "說明", e.Note)
		b.WriteString("。請看單驗收。")
	case e.FromVal == string(domain.StatusReview) && e.ToVal == string(domain.StatusInProgress):
		fmt.Fprintf(&b, " 被退回修改（%s）", e.Actor)
		appendNote(&b, "退回原因", e.Note)
		b.WriteString("。請先讀單再改。")
	default:
		fmt.Fprintf(&b, " %s → %s（%s）", e.FromVal, e.ToVal, e.Actor)
		appendNote(&b, "說明", e.Note)
		b.WriteString("。")
	}
	return b.String()
}

// appendNote：note 非空才接（節點的事件常常沒有 note）。
func appendNote(b *strings.Builder, label, note string) {
	note = strings.TrimSpace(note)
	if note == "" {
		return
	}
	b.WriteString("；")
	b.WriteString(label)
	b.WriteString("：")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(note, "\r", " "), "\n", " "))
}
