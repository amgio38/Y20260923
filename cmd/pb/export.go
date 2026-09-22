package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"project_board/internal/domain"
	"project_board/internal/store"
)

// 來源：Y20260920/REQ-V02-EXPORT 的 CTO 裁示（2026-09-20）。
//
//	pb export <id> [--out dir]
//	- 預設輸出目錄 var/export／（與 defaultDBPath 同基準），只寫這裡，來源 md 一律不碰。
//	- 匯出該 id 自己與全部子孫，一節點一檔，路徑照 id 分段、檔名最後一段加 .md。
//	- 檔頭四行 id／type／status／owner，空一行接 body。
const defaultExportDir = "var/export"

func (a *app) cmdExport(args []string) int {
	fs := a.newFlagSet("export")
	db := a.dbFlag(fs)
	out := fs.String("out", defaultExportDir, "輸出目錄（預設 var/export）")
	asJSON := fs.Bool("json", false, "輸出 JSON（寫了哪些檔）")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return a.usageErr("用法：pb export <id> [--out dir]")
	}
	id := fs.Arg(0)
	return a.withStore(*db, func(ctx context.Context, st *store.Store) error {
		nodes, err := collectSubtree(ctx, st, id)
		if err != nil {
			return err
		}
		paths, err := writeExport(*out, nodes)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.writeJSON(map[string]any{"out": *out, "files": paths})
		}
		fmt.Fprintf(a.stdout, "已匯出 %d 個節點到 %s\n", len(paths), *out)
		return nil
	})
}

// collectSubtree：BFS 收該 id 自己與全部子孫（讀 store 既有 Get；id 不存在回 ErrNotFound）。
func collectSubtree(ctx context.Context, st *store.Store, id string) ([]domain.Node, error) {
	var out []domain.Node
	seen := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		n, _, children, err := st.Get(ctx, cur)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
		for _, c := range children {
			queue = append(queue, c.ID)
		}
	}
	return out, nil
}

// exportPath：id 分段成目錄、最後一段加 .md。id 的每一段都必須是安全的檔名片段，
// 免得有人用 "../x" 之類的 id 把檔案寫出 --out 之外。
func exportPath(dir, id string) (string, error) {
	segs := strings.Split(id, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `\/`) {
			return "", fmt.Errorf("id %q 含不安全的片段 %q，拒絕匯出路徑", id, s)
		}
	}
	parts := append([]string{dir}, segs...)
	p := filepath.Join(parts...)
	return p + ".md", nil
}

// exportMarkdown：檔頭四行 ＋ 空行 ＋ body。
func exportMarkdown(n domain.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\n", n.ID)
	fmt.Fprintf(&b, "type: %s\n", n.Type)
	fmt.Fprintf(&b, "status: %s\n", n.Status)
	fmt.Fprintf(&b, "owner: %s\n", n.Owner)
	b.WriteString("\n")
	body := n.Body
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	b.WriteString(body)
	return b.String()
}

// writeExport：逐節點寫檔（var/export 內同路徑可覆蓋，那是我們自己產生的）。
func writeExport(dir string, nodes []domain.Node) ([]string, error) {
	paths := make([]string, 0, len(nodes))
	for _, n := range nodes {
		p, err := exportPath(dir, n.ID)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, fmt.Errorf("建立目錄 %q: %w", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(exportMarkdown(n)), 0o644); err != nil {
			return nil, fmt.Errorf("寫入 %q: %w", p, err)
		}
		paths = append(paths, p)
	}
	return paths, nil
}
