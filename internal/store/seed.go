package store

import (
	"context"
	"fmt"

	"project_board/internal/domain"
)

// 種子：v0.1 只建樹根 `Y20260916`（INTERFACE.md §1／OPERATIONS.md §1：`pb seed` 建 Y20260916 project 節點），
// 屬性比照 DATA_MODEL.md §9 範例（project、owner=human、in_progress）。
const (
	seedProjectID    = "Y20260916"
	seedProjectTitle = "Y20260916"
	seedProjectOwner = "human"
)

// Seed 建立種子專案節點；已存在時視為 no-op（讓 `pb init && pb seed` 可重複跑，OPERATIONS.md §7）。
func (s *Store) Seed(ctx context.Context, actor string) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	ok, err := nodeExists(ctx, s.db, seedProjectID)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if _, err := s.Create(ctx, actor, CreateInput{
		Type:     domain.TypeProject,
		ID:       seedProjectID,
		Title:    seedProjectTitle,
		Owner:    seedProjectOwner,
		Priority: string(domain.PriorityMedium),
	}); err != nil {
		return fmt.Errorf("seed project: %w", err)
	}
	if _, err := s.Transition(ctx, actor, seedProjectID, domain.StatusInProgress, "seed", nil); err != nil {
		return fmt.Errorf("seed project status: %w", err)
	}
	return nil
}
