package main

import (
	"time"
)

func main() {
	var counter uint64
	var numbers []uint64
	for {
		numbers = append(numbers, counter)
		counter = counter + 1
		time.Sleep(time.Nanosecond)
	}
}
