package main

import (
	"context"
	"time"

	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V03-HOOK 的 PO 裁示（2026-09-20）。
//
//   - 長駐的 pb serve 每 2 秒看新的 history（transition、verify），游標放 meta。
//   - 改狀態的那個行程不准呼叫 herdr：喚醒只發生在這裡（serve）。
//   - 喚醒失敗只記 log，不讓服務掛掉、也不影響任何寫入。

// defaultHookPollInterval：裁示的 2 秒輪詢間隔。
const defaultHookPollInterval = 2 * time.Second

// hookLoop：定時輪詢（ctx 結束即停）。
func (a *app) hookLoop(ctx context.Context, st *store.Store) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.pollHooks(ctx, st); err != nil {
				a.logf("hook 輪詢失敗：%v\n", err)
			}
		}
	}
}

// pollHooks：一次輪詢——讀游標、算待喚醒、叫、推進游標。
// 呼叫者（hookLoop）只把錯誤記 log；這裡的錯誤都是讀寫 DB 的問題。
func (a *app) pollHooks(ctx context.Context, st *store.Store) error {
	cur, err := st.HookCursor(ctx)
	if err != nil {
		return err
	}
	events, next, err := st.PendingHookEvents(ctx, cur)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if err := a.waker.Wake(ev.Hook.Target, ev.Message); err != nil {
			// herdr 不在或失敗只記 log（裁示）；不重試、不卡游標。
			a.logf("hook 喚醒失敗（%s → %s）：%v\n", ev.Event.NodeID, ev.Hook.Target, err)
			continue
		}
		a.logf("hook 已喚醒 %s（%s %s→%s）\n", ev.Hook.Target, ev.Event.NodeID, ev.Event.FromVal, ev.Event.ToVal)
	}
	if next > cur {
		return st.SetHookCursor(ctx, next)
	}
	return nil
}
