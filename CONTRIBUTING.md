# 貢獻指南

謝謝你願意幫忙。ProjectBoard 是純 Go 的單一執行檔（SQLite，無 cgo）。

## 開發環境

```bash
git clone https://github.com/amgio38/Y20260923
cd Y20260923
make build      # → bin/pb
make test       # go test ./...（改完程式碼，commit 前先跑）
make vet        # go vet ./...
gofmt -l .      # 不應該列出任何檔案
```

需要 Go（版本見 `go.mod`）。不需要 C 編譯器。

## 專案規矩

- **測試跟著功能走**：新功能要有測試；修 bug 要有一個「沒修會紅」的測試。
- **資料庫 migration 只增不改**：新的結構變更加一個新的 `internal/store/migrations/00NN_*.sql`，不要改舊的。
- **程式裡不寫死任何團隊或人名**：owner 名冊走 `owners.txt` / `PB_OWNERS`（見 README「設定你自己團隊的 owner 名冊」）。
- **使用者看得到的改變要升版號**：`internal/version/version.go` 的 `Version`，格式 `V0.YYYYMMDD.NNN`。打標籤時標籤要跟它一致（`v0.YYYYMMDD.NNN`），release 流程會檢查。
- 不要把 `var/`、`owners.txt`、任何真實資料或個人路徑放進 commit。

## 送 Pull Request

1. 開一個分支，改，跑上面的 `make test`、`make vet`、`gofmt -l .`。
2. 說清楚「改了什麼、為什麼」，附上你怎麼驗證的。
3. CI 會自動跑（格式、vet、測試、各平台編譯、安裝腳本測試）；紅燈請先修。

## 行為準則與安全問題

參與就代表你同意遵守 [行為準則](CODE_OF_CONDUCT.md)。安全問題**不要**開公開 issue，見 [SECURITY.md](SECURITY.md)。
