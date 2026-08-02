// game/main.go
package main

import (
	"fmt"
	"os"

	"simple_game/game/internal/gameserver"
)

func main() {
	srv := gameserver.NewServer()
	err := srv.Start()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "start failed:", err)
		os.Exit(1)
	}
}
