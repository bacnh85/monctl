// config subcommand shared across platforms.
package main

import (
	"fmt"

	"github.com/bacnh85/monctl/monitor"
)

func cmdConfig() int {
	path, err := monitor.ConfigPath()
	if err != nil {
		return fail(err)
	}
	fmt.Println(path)
	return 0
}
