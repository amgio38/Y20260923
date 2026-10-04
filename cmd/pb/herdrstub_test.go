package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// 2026-10-05：假 herdr 改成「測試執行檔自己再跑一次（子行程）」。
//
// 原本的寫法是當場寫一支 `#!/bin/sh` 腳本、chmod 755 後直接 exec。這在 Linux／macOS 沒問題，
// 但在 Windows 上 exec 一支「沒有副檔名」的腳本會失敗：
//
//	exec: "...\fake-herdr": executable file not found in %PATH%
//
// （Windows 的 CreateProcess 要靠副檔名／關聯才知道怎麼跑它。）測試要驗的是「真的 exec 一支
// 外部程式」，不是把 exec 換成假的，所以這裡改成跨平台都執行得起來的做法：herdrWaker 被餵的
// bin 指向 os.Args[0]（本測試執行檔，各平台都是貨真價實的可執行檔），TestMain 一看到
// PB_TEST_HERDR_STUB 就照規格演出、不跑任何測試後直接退出。
//
// 這樣 argv（分開傳、不經 shell）、stderr、退出碼都照實穿過 exec，測到的行為與原腳本等價。

// herdrStubSpec：假 herdr 的行為規格，序列化後放在 PB_TEST_HERDR_STUB 環境變數裡，
// 由測試行程設定、子行程繼承。
type herdrStubSpec struct {
	// Mode：
	//   record＝把 argv 逐行寫進 ArgsFile；
	//   boom＝stderr 印 boom、退出 3；
	//   panes＝`pane list` 印 ListFile、`agent prompt <w1:*>` 成功，其他 target 回 agent_not_found；
	//   bork＝`pane` 子命令退出 4，其他一律印 agent_not_found 退出 1。
	Mode     string `json:"mode"`
	ArgsFile string `json:"args_file,omitempty"`
	LogFile  string `json:"log_file,omitempty"`
	ListFile string `json:"list_file,omitempty"`
}

// stubHerdr：把假 herdr 指向測試執行檔自己，並在目前行程設好規格；回傳可執行檔路徑。
func stubHerdr(t *testing.T, spec herdrStubSpec) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PB_TEST_HERDR_STUB", string(raw))
	return os.Args[0]
}

// runHerdrStub：子行程裡扮演假 herdr；永不返回（由 TestMain 在跑測試前呼叫）。
func runHerdrStub(raw string) {
	var spec herdrStubSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		fmt.Fprintln(os.Stderr, "bad herdr stub spec:", err)
		os.Exit(90)
	}
	args := os.Args[1:]
	if spec.LogFile != "" {
		if f, err := os.OpenFile(spec.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, strings.Join(args, " "))
			_ = f.Close()
		}
	}
	switch spec.Mode {
	case "record":
		if spec.ArgsFile != "" {
			var b strings.Builder
			for _, a := range args {
				b.WriteString(a)
				b.WriteByte('\n')
			}
			_ = os.WriteFile(spec.ArgsFile, []byte(b.String()), 0o644)
		}
	case "boom":
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(3)
	case "panes":
		if len(args) >= 2 && args[0] == "pane" && args[1] == "list" {
			if b, err := os.ReadFile(spec.ListFile); err == nil {
				_, _ = os.Stdout.Write(b)
			}
			os.Exit(0)
		}
		target := ""
		if len(args) >= 3 {
			target = args[2]
		}
		if strings.HasPrefix(target, "w1:") {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "{\"error\":{\"code\":\"agent_not_found\",\"message\":\"agent target %s not found\"}}\n", target)
		os.Exit(1)
	case "bork":
		if len(args) >= 1 && args[0] == "pane" {
			os.Exit(4)
		}
		fmt.Fprintln(os.Stderr, "agent_not_found")
		os.Exit(1)
	}
	os.Exit(0)
}
