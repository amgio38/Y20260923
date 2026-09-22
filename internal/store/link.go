package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"project_board/internal/domain"
)

const linkCols = "id, from_id, kind, target, note, created_at"

func scanLink(sc rowScanner) (domain.Link, error) {
	var (
		l        domain.Link
		kind, ts string
	)
	if err := sc.Scan(&l.ID, &l.FromID, &kind, &l.Target, &l.Note, &ts); err != nil {
		return domain.Link{}, err
	}
	l.Kind = domain.LinkKind(kind)
	var err error
	if l.CreatedAt, err = parseTime(ts); err != nil {
		return domain.Link{}, err
	}
	return l, nil
}

func validLinkKind(k domain.LinkKind) bool {
	switch k {
	case domain.LinkDependsOn, domain.LinkFile, domain.LinkCommit, domain.LinkPR, domain.LinkURL, domain.LinkDoc, domain.LinkRepo:
		return true
	}
	return false
}

func (s *Store) linksFrom(ctx context.Context, id string) ([]domain.Link, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+linkCols+" FROM links WHERE from_id = ? ORDER BY id", id)
	if err != nil {
		return nil, fmt.Errorf("links from %q: %w", id, err)
	}
	defer rows.Close()
	var out []domain.Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("links from %q: %w", id, err)
	}
	return out, nil
}

// ListDependsOn：列出所有 kind=depends_on 的關聯（v0.2 依賴清單；Y20260920/REQ-V02-DEPS 裁示）。
// project 空字串＝全庫；非空＝只回 from_id 屬於該專案子樹（id 等於 project，或以前綴 project/ 開頭）的關聯。
// 沿用 domain.Link（FromID／Target／Note），依 link id 排序。
func (s *Store) ListDependsOn(ctx context.Context, project string) ([]domain.Link, error) {
	q := "SELECT " + linkCols + " FROM links WHERE kind = 'depends_on'"
	args := []any{}
	if project != "" {
		q += " AND (from_id = ? OR from_id LIKE ? ESCAPE '\\')"
		args = append(args, project, likeEscape(project)+"/%")
	}
	q += " ORDER BY id"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list depends_on: %w", err)
	}
	defer rows.Close()
	var out []domain.Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list depends_on: %w", err)
	}
	return out, nil
}

// hasDependsOnLink：轉入 blocked 時的理由檢查用（§11.3）。
func hasDependsOnLink(ctx context.Context, q queryRower, id string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx,
		"SELECT 1 FROM links WHERE from_id = ? AND kind = 'depends_on' LIMIT 1", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("depends_on links of %q: %w", id, err)
	}
	return true, nil
}

// Link 建關聯；kind=depends_on 時驗證 target 節點存在（不存在回 ErrDependsOnTargetMissing，§11.4）。
// 其餘 kind 指向系統外的字串，不驗證。
func (s *Store) Link(ctx context.Context, actor, fromID string, kind domain.LinkKind, target, note string) (domain.Link, error) {
	if err := validateActor(actor); err != nil {
		return domain.Link{}, err
	}
	if !validLinkKind(kind) {
		return domain.Link{}, fmt.Errorf("invalid link kind %q", kind)
	}
	if target == "" {
		return domain.Link{}, errors.New("link: target is required")
	}
	var out domain.Link
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := getNodeQ(ctx, tx, fromID); err != nil {
			return err
		}
		if kind == domain.LinkDependsOn {
			ok, err := nodeExists(ctx, tx, target)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("depends_on target %q: %w", target, ErrDependsOnTargetMissing)
			}
		}
		now := nowSec()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO links (from_id, kind, target, note, created_at) VALUES (?, ?, ?, ?, ?)`,
			fromID, string(kind), target, note, formatTime(now))
		if err != nil {
			return fmt.Errorf("insert link %s→%s: %w", fromID, target, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("link id: %w", err)
		}
		if err := insertHistory(ctx, tx, now, historyEntry{
			nodeID: fromID, actor: actor, action: domain.ActionLink,
			field: string(kind), to: target, note: note,
		}); err != nil {
			return err
		}
		out = domain.Link{ID: id, FromID: fromID, Kind: kind, Target: target, Note: note, CreatedAt: now}
		return nil
	})
	if err != nil {
		return domain.Link{}, err
	}
	return out, nil
}

// Unlink 移除關聯（依 id），並在被關聯的節點留 unlink history。
func (s *Store) Unlink(ctx context.Context, actor string, linkID int64) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		l, err := scanLink(tx.QueryRowContext(ctx, "SELECT "+linkCols+" FROM links WHERE id = ?", linkID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("link %d: %w", linkID, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("get link %d: %w", linkID, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM links WHERE id = ?", linkID); err != nil {
			return fmt.Errorf("delete link %d: %w", linkID, err)
		}
		return insertHistory(ctx, tx, nowSec(), historyEntry{
			nodeID: l.FromID, actor: actor, action: domain.ActionUnlink,
			field: string(l.Kind), from: l.Target,
		})
	})
}
