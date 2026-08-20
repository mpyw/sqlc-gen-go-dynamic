package main

import (
	"github.com/sqlc-dev/plugin-sdk-go/codegen"

	golang "github.com/mpyw/sqlc-gen-go-dynamic/internal"
)

func main() {
	codegen.Run(golang.Generate)
}
