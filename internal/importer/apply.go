package importer

import (
	"context"
	"errors"
	"fmt"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// Result 是一次 dry-run 或落庫的計數。Warnings 含略過與撞名，不算失敗。
type Result struct {
	Created  int
	Skipped  int
	Warnings []string
}

// Apply 依 Plan 的順序寫入。dryRun 為 true 時不開寫入。
// 狀態不直接改欄位：沿狀態機走（done 必經 verify），history 才對得上。
// 撞名只警告，不更新既有節點（OPERATIONS.md §6）。
func Apply(ctx context.Context, st *store.Store, actor, projectID string, items []Item, dryRun bool) (Result, error) {
	var res Result
	if !dryRun {
		if _, _, _, err := st.Get(ctx, projectID); err != nil {
			return res, fmt.Errorf("匯入目標 %s 不存在，先 pb seed: %w", projectID, err)
		}
	}
	for _, it := range items {
		if it.Action != "create" {
			res.Skipped++
			res.Warnings = append(res.Warnings, "略過 "+it.Source+"："+it.Reason)
			continue
		}
		if dryRun {
			res.Created++
			continue
		}
		_, err := st.Create(ctx, actor, store.CreateInput{
			Type:     it.Type,
			ID:       it.ID,
			ParentID: it.ParentID,
			Title:    it.Title,
			Owner:    it.Owner,
			Body:     it.Body,
			Tags:     "imported",
		})
		if err != nil {
			if errors.Is(err, store.ErrIDExists) {
				res.Skipped++
				res.Warnings = append(res.Warnings, "撞名不覆蓋 "+it.ID)
				continue
			}
			return res, fmt.Errorf("建立 %s: %w", it.ID, err)
		}
		if it.Source != "" {
			if _, err := st.Link(ctx, actor, it.ID, domain.LinkFile, it.Source, "匯入來源"); err != nil {
				return res, fmt.Errorf("掛來源 %s: %w", it.ID, err)
			}
		}
		if err := reach(ctx, st, actor, it.ID, it.Status, "匯入自 "+it.Source); err != nil {
			return res, fmt.Errorf("狀態 %s → %s: %w", it.ID, it.Status, err)
		}
		res.Created++
	}
	return res, nil
}

// reach 從 todo 走到目標狀態。done 用 verify，證據是匯入來源。
func reach(ctx context.Context, st *store.Store, actor, id string, to domain.Status, note string) error {
	if to == "" || to == domain.StatusTodo {
		return nil
	}
	step := func(s domain.Status, n string) error {
		_, err := st.Transition(ctx, actor, id, s, n, nil)
		return err
	}
	switch to {
	case domain.StatusInProgress:
		return step(domain.StatusInProgress, note)
	case domain.StatusReview:
		if err := step(domain.StatusInProgress, note); err != nil {
			return err
		}
		return step(domain.StatusReview, note)
	case domain.StatusDone:
		if err := step(domain.StatusInProgress, note); err != nil {
			return err
		}
		if err := step(domain.StatusReview, note); err != nil {
			return err
		}
		_, err := st.Verify(ctx, actor, id, note)
		return err
	case domain.StatusBlocked, domain.StatusHold, domain.StatusCancel:
		return step(to, note)
	default:
		return fmt.Errorf("無法匯入的狀態 %q", to)
	}
}
