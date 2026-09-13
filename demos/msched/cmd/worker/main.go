package main

import (
	"fmt"
	"os"

	"github.com/balcony314/msched/internal/version"
)

func main() {
	fmt.Printf("msched-worker %s\n", version.Version)
	if len(os.Args) > 1 && os.Args[1] == "-v" {
		return
	}
	fmt.Println("worker entrypoint — see docs/DESIGN.md")
}
