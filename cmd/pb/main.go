// Command pb：ProjectBoard CLI（權威：docs/INTERFACE.md §1；任務：dev_docs/ISSUE-XIAOXIA.md §PB03）。
//
// 單一 binary、單一 DB 檔（`var/board.db`）、零外部服務。
//   - 每個子命令都先 `store.New` ＋ `Migrate`（migration 可重複跑，DATA_MODEL.md §11.7）。
//   - 寫入類子命令一律要 actor：`--actor` → env `PB_ACTOR` → 拒絕（DATA_MODEL.md §11.1）。
//   - 錯誤一律用 `errors.Is` 分辨種類，印人類看得懂的訊息，不吐 Go 原始錯誤字串。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"project_board/internal/store"
)

// 結束碼：0 成功、1 執行錯誤（store／domain 回的錯）、2 用法錯誤（參數缺漏／未知子命令）。
const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2

	// defaultDBPath：真相源 DB（README／OPERATIONS.md §2）；可用 --db 或 env PB_DB 覆寫。
	defaultDBPath = "var/board.db"

	// seedProjectID：`pb seed` 建的樹根（INTERFACE.md §1）；與 internal/store 的種子常數一致。
	seedProjectID = "Y20260916"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run 是 main 的可測版本：外部依賴（輸出、env）全部注入。
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return newApp(stdout, stderr, getenv).dispatch(args)
}

// app 收斂外部依賴，讓每個子命令都能在測試中被驅動（含常駐服務的接縫）。
type app struct {
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
	mcpRun func(context.Context, *store.Store) error // 預設 mcp.RunStdio
	listen func(*http.Server) error                  // 預設 srv.ListenAndServe
	// hook 喚醒（v0.3）：serve 用；測試換成假 Waker，不真的 exec herdr（REQ-V03-HOOK 裁示）。
	waker        Waker
	pollInterval time.Duration // 預設 defaultHookPollInterval（2s）
}

func newApp(stdout, stderr io.Writer, getenv func(string) string) *app {
	a := &app{stdout: stdout, stderr: stderr, getenv: getenv}
	a.wire()
	return a
}

// logf：啟動訊息（走 stderr，不污染 stdout 的資料輸出）。
func (a *app) logf(format string, args ...any) {
	fmt.Fprintf(a.stderr, format, args...)
}

// dispatch 把子命令名對到實作（INTERFACE.md §1 的命令集）。
func (a *app) dispatch(args []string) int {
	if len(args) == 0 {
		a.usage()
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return a.cmdInit(rest)
	case "seed":
		return a.cmdSeed(rest)
	case "serve":
		return a.cmdServe(rest)
	case "mcp":
		return a.cmdMCP(rest)
	case "tree":
		return a.cmdTree(rest)
	case "get":
		return a.cmdGet(rest)
	case "create":
		return a.cmdCreate(rest)
	case "update":
		return a.cmdUpdate(rest)
	case "move":
		return a.cmdMove(rest)
	case "assign":
		return a.cmdAssign(rest)
	case "link":
		return a.cmdLink(rest)
	case "verify":
		return a.cmdVerify(rest)
	case "comment":
		return a.cmdComment(rest)
	case "search":
		return a.cmdSearch(rest)
	case "deps":
		return a.cmdDeps(rest)
	case "export":
		return a.cmdExport(rest)
	case "history":
		return a.cmdHistory(rest)
	case "stats":
		return a.cmdStats(rest)
	case "report":
		return a.cmdReport(rest)
	case "hook":
		return a.cmdHook(rest)
	case "unhook":
		return a.cmdUnhook(rest)
	case "hooks":
		return a.cmdHooks(rest)
	case "checklist":
		return a.cmdChecklist(rest)
	case "import":
		return a.cmdImport(rest)
	case "commit":
		return a.cmdCommit(rest)
	case "repo":
		return a.cmdRepo(rest)
	case "help", "-h", "--help":
		a.usage()
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "錯誤：未知子命令 %q\n", cmd)
		a.usage()
		return exitUsage
	}
}

func (a *app) usage() {
	fmt.Fprint(a.stdout, `ProjectBoard（pb）—— 專案／需求／單 的單一真相源

用法：pb <子命令> [參數]

  init                                       建 DB 與 schema（可重複跑）
  seed                                       建 Y20260916 專案節點（可重複跑）
  serve [--addr 127.0.0.1:8787]              常駐：REST ＋ dashboard ＋ MCP(HTTP)
  mcp                                         MCP over stdio（外部 harness 用）

  tree   [--project X] [--status s] [--owner o] [--tag t] [--type t] [--depth n]
  get    <id>
  create --type <t> --title <s> [--parent <id>] [--id <id>] [--owner <o>] [--priority p]
         [--tags s] [--body s]
  update <id> [--title s] [--body s] [--owner o] [--priority p] [--tags s] [--sort n]
         [--if-unmodified-since <ts>]
  move   <id> <status> [--note s] [--if-unmodified-since <ts>]
  assign <id> <owner>
  link   <id> --kind <k> --target <s> [--note s]
  verify <id> --note <evidence>
  comment <id> <text>
  search [query] [--project X] [--tag t] [--owner o]
  deps   [--project X]
  history <id> [--limit n]
  stats  [--project X]
  report [--week YYYY-MM-DD] [--project X]
  hook   <node_id> --target <agent> [--harness herdr]
  unhook <node_id> --target <agent> [--harness herdr]
  hooks  [<node_id>]
  checklist [--project X]
  import <dir> [--project Y20260916] [--dry-run]
  commit attach [--sha <sha>] [--message-file <path>] [--dry-run]
  repo   set <project-id> --url <url> [--path <p>]
  repo   show <project-id> [--json]
  export <id> [--out dir]

共同參數：
  --actor <name>   寫入類子命令必填；未帶時退回 env PB_ACTOR；兩者皆無即拒絕
                   （名冊見 DATA_MODEL.md §8）
  --db <path>      DB 路徑（預設 env PB_DB → var/board.db）
  --json           讀取類子命令（tree／get／search／deps／history／stats／report／hooks／checklist／export）輸出 JSON

結束碼：0 成功／1 執行錯誤／2 用法錯誤
`)
}

// reorderArgs：把旗標集中到前面、位置參數往後排。
//
// 為什麼需要：Go 的 flag 套件遇到第一個非旗標就停止解析，而 INTERFACE.md §1 的寫法
// 是 `pb move <id> <status> --note s`（位置參數在前）——不重排就會把 --note 當成位置參數。
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { // 明確分隔：之後一律當位置參數
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(arg) > 1 && arg[0] == '-' {
			name := strings.TrimLeft(arg, "-")
			hasValue := false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name, hasValue = name[:eq], true
			}
			flags = append(flags, arg)
			if !hasValue && i+1 < len(args) {
				if f := fs.Lookup(name); f != nil && !isBoolFlag(f.Value) {
					i++
					flags = append(flags, args[i])
				}
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

// isBoolFlag：判斷某個 flag 是否為布林旗標（布林旗標不吃後面的值）。
func isBoolFlag(v flag.Value) bool {
	if bf, ok := v.(interface{ IsBoolFlag() bool }); ok {
		return bf.IsBoolFlag()
	}
	return false
}

// newFlagSet：每個子命令自己的 FlagSet，錯誤訊息往 stderr。
func (a *app) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	return fs
}

// dbFlag：--db，預設 env PB_DB → var/board.db。
func (a *app) dbFlag(fs *flag.FlagSet) *string {
	def := a.getenv("PB_DB")
	if def == "" {
		def = defaultDBPath
	}
	return fs.String("db", def, "SQLite 檔路徑（預設 $PB_DB → "+defaultDBPath+"）")
}

// actorFlag：--actor（寫入類子命令）。
func (a *app) actorFlag(fs *flag.FlagSet) *string {
	return fs.String("actor", "", "操作者（名冊見 DATA_MODEL.md §8；未帶時退回 env PB_ACTOR）")
}

// resolveActor：--actor → env PB_ACTOR → 拒絕（INTERFACE.md §1、DATA_MODEL.md §11.1）。
func (a *app) resolveActor(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := a.getenv("PB_ACTOR"); v != "" {
		return v, nil
	}
	return "", errMissingActor
}

// openStore：New ＋ Migrate（migration 可重複跑，DATA_MODEL.md §11.7）。
func openStore(dbPath string) (*store.Store, error) {
	st, err := store.New(dbPath)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// withStore：開 DB 跑 fn，收工一定關檔。
func (a *app) withStore(dbPath string, fn func(ctx context.Context, st *store.Store) error) int {
	st, err := openStore(dbPath)
	if err != nil {
		return a.fail(err)
	}
	defer func() { _ = st.Close() }()
	if err := fn(context.Background(), st); err != nil {
		return a.fail(err)
	}
	return exitOK
}

// usageErr 印用法錯誤並回 2。
func (a *app) usageErr(format string, args ...any) int {
	fmt.Fprintf(a.stderr, "錯誤："+format+"\n", args...)
	return exitUsage
}

// fail 印人類可讀錯誤；缺 actor 視為用法錯誤（2），其餘執行錯誤（1）。
func (a *app) fail(err error) int {
	fmt.Fprintln(a.stderr, humanize(err))
	if isUsageErr(err) {
		return exitUsage
	}
	return exitErr
}

// mkdirFor：建 DB 所在目錄（var/ 之類），目錄已存在時 no-op。
func mkdirFor(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建立目錄 %q: %w", dir, err)
	}
	return nil
}
