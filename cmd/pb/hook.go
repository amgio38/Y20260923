package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V03-HOOK 的 PO 裁示（2026-09-20）。
//
//	pb hook   <node_id> [--harness herdr] --target <agent> [--actor a]
//	pb unhook <node_id> [--harness herdr] --target <agent> [--actor a]
//	pb hooks  [<node_id>]
//	- 接單的人 hook 自己的 ISSUE；派工的 CTO hook 那張 REQ（旗下 ISSUE 的變動都算）。
//   - 喚醒本體在長駐 serve（2 秒輪詢），CLI 只寫／讀訂閱。

// cmdHook：訂閱節點（或它的子孫）的狀態變動。
func (a *app) cmdHook(args []string) int {
	fs := a.newFlagSet("hook")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	harness := fs.String("harness", store.HarnessHerdr, "harness（這輪只收 "+store.HarnessHerdr+"）")
	target := fs.String("target", "", "要喚醒的 agent 名（^[a-z][a-z0-9_-]{0,31}$）")
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb hook <node_id> --target <agent> [--harness herdr] [--actor a]")
	}
	node := fs.Arg(0)
	if *target == "" {
		return a.usageErr("--target 是必填（要喚醒的 agent 名）")
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		h, err := st.Hook(ctx, store.HookInput{Actor: who, NodeID: node, Harness: *harness, Target: *target})
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toHookJSON(h))
		}
		fmt.Fprintf(a.stdout, "已訂閱 %s（%s → %s，actor=%s）\n", h.NodeID, h.Harness, h.Target, h.Actor)
		return nil
	})
}

// cmdUnhook：取消訂閱（不存在回執行錯誤，不靜默成功）。
func (a *app) cmdUnhook(args []string) int {
	fs := a.newFlagSet("unhook")
	db := a.dbFlag(fs)
	actor := a.actorFlag(fs)
	harness := fs.String("harness", store.HarnessHerdr, "harness（這輪只收 "+store.HarnessHerdr+"）")
	target := fs.String("target", "", "要取消的 agent 名")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb unhook <node_id> --target <agent> [--harness herdr] [--actor a]")
	}
	node := fs.Arg(0)
	if *target == "" {
		return a.usageErr("--target 是必填")
	}
	who, err := a.resolveActor(*actor)
	if err != nil {
		return a.fail(err)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		if err := st.Unhook(ctx, who, node, *harness, *target); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "已取消訂閱 %s（%s → %s）\n", node, *harness, *target)
		return nil
	})
}

// cmdHooks：列出訂閱（可只列某節點）。
func (a *app) cmdHooks(args []string) int {
	fs := a.newFlagSet("hooks")
	db := a.dbFlag(fs)
	asJSON := fs.Bool("json", false, "輸出 JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() > 1 {
		return a.usageErr("用法：pb hooks [<node_id>] [--json]")
	}
	node := ""
	if fs.NArg() == 1 {
		node = fs.Arg(0)
	}
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		hs, err := st.Hooks(ctx, node)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(toHooksJSON(hs))
		}
		a.printHooks(hs)
		return nil
	})
}

// ---------- 喚醒（只有長駐 serve 會用到）----------

// Waker：喚醒介面（裁示：測試注入假實作，不准真的 exec herdr）。
type Waker interface {
	Wake(target, message string) error
}

// herdrWaker：真的去叫 herdr，固定 `herdr agent prompt <target> <訊息>`，
// 參數分開傳、**不經 shell**（裁示）。
type herdrWaker struct {
	bin string
}

// defaultHerdrBin：herdr 執行檔名（PATH 內）。
const defaultHerdrBin = "herdr"

// wakeTimeout：單次喚醒的上界（herdr 不在／卡住都不會拖垮 serve；失敗只記 log）。
const wakeTimeout = 5 * time.Second

func (w herdrWaker) Wake(target, message string) error {
	bin := w.bin
	if bin == "" {
		bin = defaultHerdrBin
	}
	ctx, cancel := context.WithTimeout(context.Background(), wakeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "agent", "prompt", target, message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr agent prompt %s: %w（%s）", target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
