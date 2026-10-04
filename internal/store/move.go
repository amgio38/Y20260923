package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"project_board/internal/domain"
)

// actionMove：搬移事件（history.action）。migration 0009 放寬 CHECK 後才寫得進去；
// domain 的 HistoryAction 常數清單未收此值（比照 node.go 的 actionDelete 處理）。
const actionMove domain.HistoryAction = "move"

// ErrCannotMove：這個節點不能被搬——根節點／專案，或要搬到自己（或自己的子孫）底下。
var ErrCannotMove = errors.New("node cannot be moved")

// MoveResult：MoveNode 的結果。
type MoveResult struct {
	Node  domain.Node       // 搬移後的 root（新 id／新 parent）
	OldID string            // 搬移前 root 的 id
	NewID string            // 搬移後 root 的 id
	Moved map[string]string // 舊 id → 新 id（root ＋ 全部被搬的子孫）
}

// moveAfterWriteFault：失敗注入點（正式路徑為 nil）。讓測試能證明「transaction 中途出錯
// → 整筆回滾、資料逐筆不變」（本系統既有接縫慣例，如 app.listen／app.waker）。
var moveAfterWriteFault func() error

// MoveNode 把節點（連全部子孫）搬到另一個父節點底下，父節點可在同一專案或別的專案。
//
// 語意（Y20260920/REQ-MOVE-NODE 定案，見 ISSUE-MOVE-NODE-CORE）：
//   - **id 一律改寫**成新路徑（維持「id 錨定路徑」的不變量）；舊 id 寫入 id_aliases，
//     Get 之後仍能由舊 id 解析到新位置。
//   - 子樹每個節點的 id／parent_id 同步改寫；links(from_id／depends_on target)、
//     history.node_id、hooks.node_id 全部改指新 id；狀態／owner／sort 原樣保留。
//   - 專案／根節點不可搬；不可搬到自己或自己的子孫底下；新 parent 必須存在；
//     item／bug 的 parent_type 規則仍適用；目標已有同名 id 回 ErrIDExists（不加尾碼）。
//   - 搬到目前父節點＝no-op（不寫、不留 history）。
//   - 跨專案需附 note（理由）；本系統無驗身機制，這是紀律要求而非權限檢查。
//   - 全部在**單一 transaction**內完成（defer_foreign_keys），中途失敗整筆回滾。
func (s *Store) MoveNode(ctx context.Context, actor, id, newParentID, note string, expectedUpdatedAt *time.Time) (MoveResult, error) {
	if err := validateActor(actor); err != nil {
		return MoveResult{}, err
	}
	if strings.TrimSpace(id) == "" {
		return MoveResult{}, errors.New("move: id is required")
	}
	if strings.TrimSpace(newParentID) == "" {
		return MoveResult{}, errors.New("move: new parent id is required")
	}
	note = strings.TrimSpace(note)

	// node_types 是靜態資料，registry 在 transaction 外先取（比照 Create）：
	// 連線池只有 1 條，tx 內若再向 s.db 要連線會死鎖。
	reg, err := s.typeRegistry(ctx)
	if err != nil {
		return MoveResult{}, err
	}

	var out MoveResult
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		// 整批 id 改寫會在中途短暫破壞 parent_id／node_id 的指向，
		// 統一延到 commit 才檢查外鍵（PRAGMA foreign_keys 在交易內關不掉，改用這個）。
		if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
			return fmt.Errorf("defer foreign keys: %w", err)
		}
		byID, err := nodesByIDTx(ctx, tx)
		if err != nil {
			return err
		}
		// 舊 id（搬移後）也接受：只有「這個 id 現在沒有活節點」時才查 alias 導到新位置。
		rootID := id
		if _, ok := byID[id]; !ok {
			if rootID, err = resolveAliasQ(ctx, tx, id); err != nil {
				return err
			}
		}
		parentID := newParentID
		if _, ok := byID[newParentID]; !ok {
			if parentID, err = resolveAliasQ(ctx, tx, newParentID); err != nil {
				return err
			}
		}
		cur, ok := byID[rootID]
		if !ok {
			return fmt.Errorf("node %q: %w", id, ErrNotFound)
		}
		if cur.ParentID == "" {
			return fmt.Errorf("node %q（%s）是根節點，不可搬: %w", cur.ID, cur.Type, ErrCannotMove)
		}
		nparent, ok := byID[parentID]
		if !ok {
			return fmt.Errorf("parent %q: %w", newParentID, ErrNotFound)
		}
		if expectedUpdatedAt != nil && !cur.UpdatedAt.Equal(*expectedUpdatedAt) {
			return fmt.Errorf("node %q: %w", cur.ID, ErrConflict)
		}
		if parentID == cur.ParentID {
			out = MoveResult{Node: cur, OldID: cur.ID, NewID: cur.ID, Moved: map[string]string{}}
			return nil // no-op：已經在該父節點底下
		}
		if isSelfOrDescendant(byID, parentID, cur.ID) {
			return fmt.Errorf("不能把 %q 搬到它自己或它的子孫 %q 底下: %w", cur.ID, parentID, ErrCannotMove)
		}
		// parent 型別檢查（查 node_types.parent_type；domain 只驗 id 字串形狀）。
		def, ok := reg.Lookup(cur.Type)
		if !ok {
			return fmt.Errorf("type %q: %w", cur.Type, domain.ErrInvalidID)
		}
		if def.ParentType != "" && nparent.Type != def.ParentType {
			if cur.Type == domain.TypeItem {
				return fmt.Errorf("item 的 parent %q 是 %s: %w", nparent.ID, nparent.Type, ErrItemParentNotReq)
			}
			return fmt.Errorf("%s 的 parent %q 是 %s，需為 %s: %w",
				cur.Type, nparent.ID, nparent.Type, def.ParentType, ErrParentTypeMismatch)
		}
		if projectSegment(cur.ID) != projectSegment(parentID) && note == "" {
			return errors.New("跨專案搬移需附 --note 說明理由")
		}
		// 子樹 ＋ 新 id 對照：newID = 新 parent 前綴 ＋ 舊 id 去掉舊 parent 前綴的尾段。
		moved := map[string]string{}
		for oldID := range byID {
			if oldID == cur.ID || strings.HasPrefix(oldID, cur.ID+"/") {
				moved[oldID] = parentID + oldID[len(cur.ParentID):]
			}
		}
		if err := reg.ValidateID(cur.Type, parentID, moved[cur.ID]); err != nil {
			return err
		}
		// 撞名：目標位置已有同名 id → 直接回錯（不加尾碼，與 create 一致）。
		for _, newID := range moved {
			if _, exists := byID[newID]; exists {
				return fmt.Errorf("node %q: %w", newID, ErrIDExists)
			}
		}
		now := nowSec()

		// 1) nodes：改寫子樹每個節點的 id／parent_id（FK 已 deferred，順序無所謂）。
		for oldID, newID := range moved {
			newPID := parentID
			if oldID != cur.ID {
				newPID = moved[byID[oldID].ParentID]
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE nodes SET id = ?, parent_id = ?, updated_at = ? WHERE id = ?",
				newID, nullIfEmpty(newPID), formatTime(now), oldID); err != nil {
				return fmt.Errorf("move node %q → %q: %w", oldID, newID, err)
			}
		}
		// 2) links：出向 from_id 與 depends_on 的 target 都改指新 id。
		for oldID, newID := range moved {
			if _, err := tx.ExecContext(ctx,
				"UPDATE links SET from_id = ? WHERE from_id = ?", newID, oldID); err != nil {
				return fmt.Errorf("move links from %q: %w", oldID, err)
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE links SET target = ? WHERE kind = 'depends_on' AND target = ?", newID, oldID); err != nil {
				return fmt.Errorf("move depends_on target %q: %w", oldID, err)
			}
		}
		// 3) history：事件跟著節點走（否則 FK 斷、稽核流斷）。
		for oldID, newID := range moved {
			if _, err := tx.ExecContext(ctx,
				"UPDATE history SET node_id = ? WHERE node_id = ?", newID, oldID); err != nil {
				return fmt.Errorf("move history of %q: %w", oldID, err)
			}
		}
		// 4) hooks：訂閱跟著節點走。
		for oldID, newID := range moved {
			if _, err := tx.ExecContext(ctx,
				"UPDATE hooks SET node_id = ? WHERE node_id = ?", newID, oldID); err != nil {
				return fmt.Errorf("move hook of %q: %w", oldID, err)
			}
		}
		// 失敗注入點（測試用；正式路徑 nil）：放在所有寫入之後、收尾之前。
		if moveAfterWriteFault != nil {
			if err := moveAfterWriteFault(); err != nil {
				return err
			}
		}
		// 5) id_aliases：既有別名改指最新位置，再寫入這批舊 id。
		for oldID, newID := range moved {
			if _, err := tx.ExecContext(ctx,
				"UPDATE id_aliases SET new_id = ? WHERE new_id = ?", newID, oldID); err != nil {
				return fmt.Errorf("repoint alias of %q: %w", oldID, err)
			}
		}
		for oldID, newID := range moved {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO id_aliases (old_id, new_id, moved_at, actor) VALUES (?, ?, ?, ?)
				 ON CONFLICT (old_id) DO UPDATE SET new_id = excluded.new_id, moved_at = excluded.moved_at, actor = excluded.actor`,
				oldID, newID, formatTime(now), actor); err != nil {
				return fmt.Errorf("record alias %q: %w", oldID, err)
			}
		}
		// 6) history：搬移事件記在搬後的 root 上。
		if err := insertHistory(ctx, tx, now, historyEntry{
			nodeID: moved[cur.ID], actor: actor, action: actionMove,
			field: "parent", from: cur.ParentID, to: parentID, note: note,
		}); err != nil {
			return err
		}
		movedNode := cur
		movedNode.ID = moved[cur.ID]
		movedNode.ParentID = parentID
		movedNode.UpdatedAt = now
		out = MoveResult{Node: movedNode, OldID: cur.ID, NewID: movedNode.ID, Moved: moved}
		return nil
	})
	if err != nil {
		return MoveResult{}, err
	}
	return out, nil
}

// projectSegment：id 的第一段（專案 id，如 Y20260920）；沒有 '/' 時回整個字串。
func projectSegment(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return id
}

// resolveAliasQ：沿 id_aliases 把舊 id 換成現行 id（查不到就原樣回傳）；帶迴圈上界。
func resolveAliasQ(ctx context.Context, q queryRower, id string) (string, error) {
	cur := id
	for i := 0; i < 64; i++ {
		var next string
		err := q.QueryRowContext(ctx, "SELECT new_id FROM id_aliases WHERE old_id = ?", cur).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return cur, nil
		}
		if err != nil {
			return "", fmt.Errorf("resolve alias %q: %w", cur, err)
		}
		if next == cur || next == "" {
			return cur, nil
		}
		cur = next
	}
	return cur, nil
}

// nodesByIDTx：一次讀全部節點成 map（搬移要看整棵樹的形狀）。
func nodesByIDTx(ctx context.Context, tx *sql.Tx) (map[string]domain.Node, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+nodeCols+" FROM nodes")
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	byID := map[string]domain.Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		byID[n.ID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	return byID, nil
}
