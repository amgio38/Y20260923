.PHONY: build linux windows clean fmt vet test

BIN_DIR := bin
PKG     := ./cmd/pb

# 純 Go 的 modernc.org/sqlite（無 cgo），跨平台編譯不需要 C 交叉編譯工具鏈，
# 固定關掉 cgo 避免主機環境不同導致行為不一致。
export CGO_ENABLED=0

# 預設：編給目前這台機器用（開發機常用）。
build:
	go build -o $(BIN_DIR)/pb $(PKG)

# Linux amd64（伺服器常見架構）。
linux:
	GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/pb-linux-amd64 $(PKG)

# Windows amd64（.exe 副檔名，Windows 執行檔要求）。
windows:
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/pb-windows-amd64.exe $(PKG)

fmt:
	gofmt -l .

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
