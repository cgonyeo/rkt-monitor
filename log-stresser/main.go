package main

import (
	"fmt"
	"time"
)

func main() {
	for {
		fmt.Printf("%s\n", time.Now().Format("Mon Jan 2 15:04:05 -0700 MST 2006"))
	}
}
