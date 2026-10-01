package main

import (
	"context"
	"encoding/json"
	"errors"
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
	err := w.prompt(target, message)
	if err == nil || !errors.Is(err, errHerdrTargetNotFound) {
		return err
	}
	// rename 的名字隨 pane 重開就掉（2026-09-28 A3：target=pi 查無此 agent，推送
	// 靜默失效）。直接送失敗時才退一步：從 pane 清單解析出唯一的 pane_id 重送。
	pane, rerr := w.resolvePane(target)
	if rerr != nil {
		return fmt.Errorf("%w；改用 pane 清單解析也失敗：%v", err, rerr)
	}
	return w.prompt(pane, message)
}

// errHerdrTargetNotFound：herdr 認不得這個 target（agent_not_found），可退一步解析。
var errHerdrTargetNotFound = errors.New("herdr 找不到 target")

func (w herdrWaker) binary() string {
	if w.bin == "" {
		return defaultHerdrBin
	}
	return w.bin
}

// prompt：`herdr agent prompt <target> <訊息>`，參數分開傳、不經 shell。
func (w herdrWaker) prompt(target, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), wakeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, w.binary(), "agent", "prompt", target, message).CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	if strings.Contains(text, "agent_not_found") {
		return fmt.Errorf("herdr agent prompt %s: %w（%s）", target, errHerdrTargetNotFound, text)
	}
	return fmt.Errorf("herdr agent prompt %s: %w（%s）", target, err, text)
}

// resolvePane：跑 `herdr pane list` 交給 pickPane。
func (w herdrWaker) resolvePane(target string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wakeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, w.binary(), "pane", "list").Output()
	if err != nil {
		return "", fmt.Errorf("herdr pane list: %w", err)
	}
	return pickPane(out, target)
}

// herdrPane：`herdr pane list` 每筆裡喚醒要用到的欄位。
type herdrPane struct {
	PaneID string `json:"pane_id"`
	Agent  string `json:"agent"`
	Label  string `json:"label"`
	Name   string `json:"name"`
}

// pickPane：從 `herdr pane list` 的 JSON 找 target 對應的唯一 pane_id。
// 先比人取的名字（name／label），再比 agent 種類（pi、claude…）；
// 同一層多筆命中就拒絕——寧可不送，也不能送錯人。
func pickPane(listJSON []byte, target string) (string, error) {
	var doc struct {
		Result struct {
			Panes []herdrPane `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(listJSON, &doc); err != nil {
		return "", fmt.Errorf("herdr pane list 輸出不是預期的 JSON：%w", err)
	}
	byName := func(p herdrPane) bool { return p.Name == target || p.Label == target }
	byAgent := func(p herdrPane) bool { return p.Agent == target }
	for _, match := range []func(herdrPane) bool{byName, byAgent} {
		var hits []string
		for _, p := range doc.Result.Panes {
			if p.PaneID != "" && match(p) {
				hits = append(hits, p.PaneID)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], nil
		default:
			return "", fmt.Errorf("target %q 對到多個 pane（%s），不猜", target, strings.Join(hits, "、"))
		}
	}
	return "", fmt.Errorf("target %q 在 herdr pane 清單裡沒有對應的 pane", target)
}
