package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"project_board/internal/store"
)

// openStore：測試中直接拿一顆 Store 動 store 層（輪詢測試用；CLI 路徑仍走 a.dispatch）。
func (ta *testApp) openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(ta.dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", ta.dbPath, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// 來源：Y20260920/REQ-V03-HOOK —— pb hook／unhook／hooks 與 serve 的 2 秒輪詢。
// 喚醒一律注入假 Waker（裁示：測試不准真的 exec herdr）。

// fakeWaker：記錄被叫過的 (target, message)，可設定固定錯誤。
type fakeWaker struct {
	mu    sync.Mutex
	calls []string
	err   error
	ch    chan string
}

func (f *fakeWaker) Wake(target, message string) error {
	f.mu.Lock()
	f.calls = append(f.calls, target+"|"+message)
	ch := f.ch
	f.mu.Unlock()
	if ch != nil {
		select {
		case ch <- target + "|" + message:
		default:
		}
	}
	return f.err
}

func (f *fakeWaker) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestHookCommands(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)

	out := ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxIssue, "--target", "yilong")
	if !strings.Contains(out, "已訂閱 "+fxIssue) || !strings.Contains(out, "herdr → yilong") {
		t.Errorf("hook 輸出 = %q", out)
	}
	ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxReq, "--target", "kaimake")

	hooks := decodeJSON[[]map[string]any](t, ta.mustRun(t, exitOK, "hooks", "--db", ta.dbPath, "--json"))
	if len(hooks) != 2 {
		t.Fatalf("hooks --json = %v", hooks)
	}
	if hooks[0]["node_id"] != fxReq || hooks[0]["harness"] != "herdr" || hooks[0]["actor"] != "human" {
		t.Errorf("hooks[0] = %v", hooks[0])
	}
	// 只看某節點
	if one := decodeJSON[[]map[string]any](t,
		ta.mustRun(t, exitOK, "hooks", "--db", ta.dbPath, fxIssue, "--json")); len(one) != 1 {
		t.Errorf("hooks <node> = %v", one)
	}
	// 文字輸出
	if out := ta.mustRun(t, exitOK, "hooks", "--db", ta.dbPath); !strings.Contains(out, fxIssue) {
		t.Errorf("hooks 文字 = %q", out)
	}
	// 取消訂閱
	if out := ta.mustRun(t, exitOK, "unhook", "--db", ta.dbPath, fxIssue, "--target", "yilong"); !strings.Contains(out, "已取消訂閱") {
		t.Errorf("unhook 輸出 = %q", out)
	}
	if one := decodeJSON[[]map[string]any](t,
		ta.mustRun(t, exitOK, "hooks", "--db", ta.dbPath, fxIssue, "--json")); len(one) != 0 {
		t.Errorf("取消後 = %v", one)
	}
	// 沒有訂閱時的文字輸出
	if out := ta.mustRun(t, exitOK, "hooks", "--db", ta.dbPath, fxProject); !strings.Contains(out, "（沒有訂閱）") {
		t.Errorf("空清單輸出 = %q", out)
	}

	// hook --json
	h := decodeJSON[map[string]any](t, ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxIssue, "--target", "kaimadi", "--json"))
	if h["target"] != "kaimadi" || h["node_id"] != fxIssue {
		t.Errorf("hook --json = %v", h)
	}
}

func TestHookCommandErrors(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"缺 node", []string{"hook", "--target", "yilong"}, exitUsage},
		{"缺 target", []string{"hook", fxIssue}, exitUsage},
		{"target 格式錯", []string{"hook", fxIssue, "--target", "Yilong"}, exitErr},
		{"harness 不支援", []string{"hook", fxIssue, "--target", "yilong", "--harness", "slack"}, exitErr},
		{"node 不存在", []string{"hook", "--target", "yilong", "Y20990101/NOPE"}, exitErr},
		{"unhook 缺 target", []string{"unhook", fxIssue}, exitUsage},
		{"unhook 不存在", []string{"unhook", fxIssue, "--target", "yilong"}, exitErr},
		{"unhook harness 不支援", []string{"unhook", fxReq, "--target", "yilong", "--harness", "slack"}, exitErr},
		{"hook 明示 herdr", []string{"hook", fxReq, "--target", "yilong", "--harness", "herdr", "--actor", "xiaoxia"}, exitOK},
		{"hooks 太多參數", []string{"hooks", "a", "b"}, exitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := ta.run(append(append([]string{}, tc.args...), "--db", ta.dbPath)...); code != tc.want {
				t.Fatalf("%v → %d, want %d（stderr=%s）", tc.args, code, tc.want, ta.stderr())
			}
		})
	}
	// 缺 actor（CLI 層用法錯誤，與其他寫入子命令一致）→ 結束碼 2
	ta2 := newTestApp(t)
	ta2.setupTree(t)
	ta2.vars["PB_ACTOR"] = ""
	if code := ta2.run("hook", "--db", ta2.dbPath, fxIssue, "--target", "yilong", "--actor", ""); code != exitUsage {
		t.Errorf("缺 actor code = %d, want %d", code, exitUsage)
	}
}

// TestPollHooks：一次輪詢＝讀游標 → 叫 → 推進游標；已叫過的不會重複叫。
func TestPollHooks(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	fake := &fakeWaker{}
	ta.a.waker = fake
	ta.a.pollInterval = time.Millisecond

	// 沒訂閱 → 不叫，但游標前進
	if err := ta.a.pollHooks(t.Context(), ta.openStore(t)); err != nil {
		t.Fatalf("pollHooks: %v", err)
	}
	if len(fake.snapshot()) != 0 {
		t.Fatalf("沒訂閱不該叫：%v", fake.snapshot())
	}

	// 訂 REQ（涵蓋旗下 ISSUE）＋ ISSUE 本身；兩個異動都由 xiaoxia 做 → 訂閱者 xiaoxia 的不叫自己
	ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxReq, "--target", "yilong", "--actor", "xiaoxia")
	ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxIssue, "--target", "kaimake", "--actor", "yilong")
	ta.mustRun(t, exitOK, "move", "--db", ta.dbPath, fxIssue, "in_progress", "--actor", "xiaoxia", "--note", "接單")
	ta.mustRun(t, exitOK, "move", "--db", ta.dbPath, fxIssue, "review", "--actor", "xiaoxia", "--note", "做完")

	st := ta.openStore(t)
	if err := ta.a.pollHooks(t.Context(), st); err != nil {
		t.Fatalf("pollHooks: %v", err)
	}
	calls := fake.snapshot()
	if len(calls) != 2 { // 2 次異動，各只叫 kaimake（xiaoxia 自己改的不叫自己）
		t.Fatalf("叫了 %d 次, want 2：%v", len(calls), calls)
	}
	if !strings.HasPrefix(calls[0], "kaimake|[ProjectBoard] "+fxIssue) {
		t.Errorf("第一則 = %q", calls[0])
	}
	if !strings.Contains(strings.Join(calls, "\n"), "請看單驗收") {
		t.Errorf("進 review 的訊息該請對方驗收：%v", calls)
	}
	// 再跑一次：游標已到頂，不再叫
	if err := ta.a.pollHooks(t.Context(), st); err != nil {
		t.Fatalf("第二次 pollHooks: %v", err)
	}
	if n := len(fake.snapshot()); n != 2 {
		t.Errorf("重複輪詢又叫人：%d 次", n)
	}
	// 游標真的寫進 meta（重開 store 也記得）
	if cur, err := ta.openStore(t).HookCursor(t.Context()); err != nil || cur == 0 {
		t.Errorf("游標 = %d (err=%v), want >0", cur, err)
	}
}

// TestPollHooksWakeFailureKeepsGoing：herdr 失敗只記 log，游標照樣前進、其他 target 照叫。
func TestPollHooksWakeFailureKeepsGoing(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	fake := &fakeWaker{err: errors.New("herdr 不在")}
	ta.a.waker = fake
	ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxIssue, "--target", "yilong", "--actor", "yilong")
	ta.mustRun(t, exitOK, "move", "--db", ta.dbPath, fxIssue, "in_progress", "--actor", "xiaoxia")

	st := ta.openStore(t)
	if err := ta.a.pollHooks(t.Context(), st); err != nil {
		t.Fatalf("喚醒失敗不該讓輪詢回錯：%v", err)
	}
	if len(fake.snapshot()) != 1 {
		t.Errorf("仍然要試著叫：%v", fake.snapshot())
	}
	if cur, _ := st.HookCursor(t.Context()); cur == 0 {
		t.Error("喚醒失敗仍要推進游標（不重試）")
	}
	if !strings.Contains(ta.stderr(), "hook 喚醒失敗") {
		t.Errorf("失敗要記 log：%q", ta.stderr())
	}
}

// TestHookLoop：真的跑 ticker（10ms），收到喚醒就取消 —— 驗證 serve 的輪詢會被啟動。
func TestHookLoop(t *testing.T) {
	ta := newTestApp(t)
	ta.setupTree(t)
	fake := &fakeWaker{ch: make(chan string, 4)}
	ta.a.waker = fake
	ta.a.pollInterval = 10 * time.Millisecond
	ta.mustRun(t, exitOK, "hook", "--db", ta.dbPath, fxIssue, "--target", "yilong", "--actor", "yilong")

	st := ta.openStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go ta.a.hookLoop(ctx, st)

	// 訂閱後才異動（游標 0 起算，第一輪就會看到）
	ta.mustRun(t, exitOK, "move", "--db", ta.dbPath, fxIssue, "in_progress", "--actor", "xiaoxia", "--note", "接單")

	select {
	case got := <-fake.ch:
		if !strings.HasPrefix(got, "yilong|[ProjectBoard] "+fxIssue) {
			t.Errorf("喚醒訊息 = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("3 秒內沒收到喚醒（輪詢沒跑？）")
	}
	cancel()
	// 取消後不該再叫
	before := len(fake.snapshot())
	time.Sleep(50 * time.Millisecond)
	if after := len(fake.snapshot()); after != before {
		t.Errorf("取消後還在叫：%d → %d", before, after)
	}
}

// TestHerdrWakerCommandShape：真 Waker 的參數形狀＝`<bin> agent prompt <target> <訊息>`（分開傳、不經 shell）。
func TestHerdrWakerCommandShape(t *testing.T) {
	dir := t.TempDir()
	argsFile := dir + "/args.txt"
	script := dir + "/fake-herdr"
	if err := writeFile(script, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsFile+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	w := herdrWaker{bin: script}
	if err := w.Wake("yilong", "訊息 帶空白; rm -rf /"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	got := readFile(t, argsFile)
	want := "agent\nprompt\nyilong\n訊息 帶空白; rm -rf /\n"
	if got != want {
		t.Errorf("argv = %q, want %q（分開傳、不經 shell）", got, want)
	}
}

// TestHerdrWakerFailure：herdr 不在（回非零）→ 回錯，訊息帶得出 stderr。
func TestHerdrWakerFailure(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/bad-herdr"
	if err := writeFile(script, "#!/bin/sh\necho 'boom' >&2\nexit 3\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	w := herdrWaker{bin: script}
	err := w.Wake("yilong", "x")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want 帶 boom", err)
	}
	// 找不到執行檔也要回錯（不 panic）
	missing := herdrWaker{bin: dir + "/nope"}
	if err := missing.Wake("yilong", "x"); err == nil {
		t.Error("執行檔不存在應回錯")
	}
}

// TestWireWakerDefaults：不注入時＝真 herdr ＋ 2 秒。
func TestWireWakerDefaults(t *testing.T) {
	a := newApp(&strings.Builder{}, &strings.Builder{}, func(string) string { return "" })
	if a.waker == nil || a.pollInterval != defaultHookPollInterval {
		t.Fatalf("預設 = %#v / %s", a.waker, a.pollInterval)
	}
	if _, ok := a.waker.(herdrWaker); !ok {
		t.Errorf("預設 waker = %T, want herdrWaker", a.waker)
	}
	// 已注入的不要被蓋掉
	fake := &fakeWaker{}
	a.waker = fake
	a.pollInterval = time.Second
	a.wireWaker()
	if a.waker != Waker(fake) || a.pollInterval != time.Second {
		t.Error("已注入的 waker／interval 不該被預設值蓋掉")
	}
}
