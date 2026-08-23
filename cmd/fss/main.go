package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"codex/platform-demo/internal/fss"
)

func main() {
	root := envOr("CLUSTERFORGE_FSS_ROOT", "/srv/clusterforge-fss")
	allowed := strings.Split(envOr("CLUSTERFORGE_FSS_WRITE_ALLOW_CIDRS", "127.0.0.1/32"), ",")
	handler, err := fss.New(root, allowed)
	if err != nil {
		log.Fatal(err)
	}
	address := envOr("CLUSTERFORGE_FSS_ADDR", "0.0.0.0:8080")
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 75 * time.Second}
	log.Printf("ClusterForge File Station listening on http://%s", address)
	log.Fatal(server.ListenAndServe())
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
