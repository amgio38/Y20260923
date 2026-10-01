package domain

import (
	"strings"
	"testing"
)

// TestParseStatusList：v0.3 的狀態多選解析——CLI／MCP／HTTP 共用同一份名冊。
func TestParseStatusList(t *testing.T) {
	ok := []struct {
		name string
		in   string
		want []Status
	}{
		{"空字串＝不過濾", "", nil},
		{"只有空白＝不過濾", "   ", nil},
		{"全是逗號＝不過濾", ",,", nil},
		{"單一", "todo", []Status{StatusTodo}},
		{"多選", "todo,in_progress,review,blocked",
			[]Status{StatusTodo, StatusInProgress, StatusReview, StatusBlocked}},
		{"容忍空白與空項", " todo , , done ,", []Status{StatusTodo, StatusDone}},
		{"重複不折疊（交由 SQL IN 處理）", "todo,todo", []Status{StatusTodo, StatusTodo}},
		{"全列", strings.Join(StatusNames(), ","), AllStatuses},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseStatusList(tc.in)
			if err != nil {
				t.Fatalf("ParseStatusList(%q) 不該出錯，實得 %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParseStatusList(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ParseStatusList(%q) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}

	// 錯誤：整串不合法、部分不合法、大小寫不符、中文——一律回「未知狀態」且訊息列可用值。
	for _, in := range []string{"nope", "todo,nope", "TODO", "進行中", "in progress"} {
		got, err := ParseStatusList(in)
		if err == nil {
			t.Fatalf("ParseStatusList(%q) 應出錯，實得 %v", in, got)
		}
		if !strings.Contains(err.Error(), "未知狀態") {
			t.Fatalf("ParseStatusList(%q) 錯誤訊息 = %q，應含「未知狀態」", in, err.Error())
		}
		for _, s := range StatusNames() {
			if !strings.Contains(err.Error(), s) {
				t.Fatalf("ParseStatusList(%q) 錯誤訊息應列出可用狀態 %q：%q", in, s, err.Error())
			}
		}
	}
}

// TestStatusNamesAndIsKnown：名冊順序＝DATA_MODEL §6，且 IsKnown 與名冊一致。
func TestStatusNamesAndIsKnown(t *testing.T) {
	names := StatusNames()
	want := []string{"todo", "in_progress", "review", "blocked", "hold", "done", "cancel", "archived"}
	if len(names) != len(want) {
		t.Fatalf("StatusNames() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("StatusNames() = %v, want %v", names, want)
		}
		if !AllStatuses[i].IsKnown() {
			t.Errorf("%q 應為已知狀態", names[i])
		}
	}
	for _, bad := range []Status{"", "Todo", "done ", "unknown"} {
		if bad.IsKnown() {
			t.Errorf("%q 不該是已知狀態", bad)
		}
	}
}
