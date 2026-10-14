package main

import (
	"log"
	"os"

	"codex/platform-demo/internal/mcpcatalog"
)

func main() {
	data, err := mcpcatalog.Generate(".")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("internal/api/mcp_catalog_generated.go", data, 0644); err != nil {
		log.Fatal(err)
	}
	doc, err := mcpcatalog.Markdown(data)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("docs/mcp-tools.md", doc, 0644); err != nil {
		log.Fatal(err)
	}
}
