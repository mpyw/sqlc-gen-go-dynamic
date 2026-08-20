// Command sqlc-gen-go-dynamic is a sqlc process plugin.
//
// Configure it with format: json, which is what this speaks:
//
//	plugins:
//	  - name: dynamic
//	    process:
//	      cmd: sqlc-gen-go-dynamic
//	      format: json
package main

import (
	"fmt"
	"os"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/plugin"
)

func main() {
	if err := plugin.Run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
