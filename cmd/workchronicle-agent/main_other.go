//go:build !windows

package main

import "fmt"

func main() {
	fmt.Println("WorkChronicle Host Agent requires Windows interactive-session APIs")
}
