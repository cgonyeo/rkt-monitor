package main

func main() {
	for {
		var x uint64
		for i := 0; i < 1000000000; i++ {
			x += uint64(i)
		}
	}
}
