// Command pbweb：僅供 PB05-A 實測用的獨立小 server，服務 fixture dashboard。
// 正式入口是 pb serve（小蝦 cmd/pb，phase 2）；本檔不影響它。
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"project_board/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address")
	flag.Parse()

	src, err := web.NewFixtureSource()
	if err != nil {
		log.Fatalf("fixture source: %v", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           web.NewHandler(src),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("ProjectBoard dashboard (fixture) on http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
