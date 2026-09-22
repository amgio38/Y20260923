package importer

import (
	"os"
	"path/filepath"
	"strings"
)

// SourceFile 是一隻待分類的 markdown。Path 原樣掛到 link，不改檔案。
type SourceFile struct {
	Name string
	Path string
	Body string
}

// LoadDir 讀 dir 底下的 *.md（不遞迴）。
// 若 dir 名是 dev_docs，順便收上一層的 MEMBER_REQ*.md（OPERATIONS.md §6 寫了這類，但它不在 dev_docs 裡）。
// 凍結目錄與 Y20260916 根目錄由呼叫端直接傳 dir 進來掃（REQ-V02-IMPORT-ALL-MD 起三處全掃）。
func LoadDir(dir string) ([]SourceFile, error) {
	list, err := readMD(dir)
	if err != nil {
		return nil, err
	}
	if filepath.Base(dir) == "dev_docs" {
		parent := filepath.Dir(dir)
		extra, err := readMD(parent)
		if err != nil {
			return nil, err
		}
		for _, f := range extra {
			if isMemberReq(f.Name) {
				list = append(list, f)
			}
		}
	}
	return list, nil
}

func readMD(dir string) ([]SourceFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []SourceFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, SourceFile{Name: name, Path: path, Body: string(b)})
	}
	return out, nil
}
