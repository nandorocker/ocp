package main

import (
	"fmt"
	"os"
	"strings"
)

func banner(s string) string {
	return "\033[2m── " + s + " " + strings.Repeat("─", 50-len(s)) + "\033[0m"
}

func main() {
	list := []string{"overview", "router", "mixed", "lab", "apple", "notices"}
	if len(os.Args) > 1 && os.Args[1] != "all" {
		list = os.Args[1:]
	}
	for _, n := range list {
		fmt.Println(banner("ocp show — " + n))
		fmt.Print(scenario(n, true, false))
		fmt.Println()
	}
	if len(os.Args) == 1 || os.Args[1] == "all" || os.Args[1] == "plain" {
		fmt.Println(banner("plain (piped / NO_COLOR, ASCII)"))
		fmt.Print(scenario("overview", false, true))
		fmt.Println()
		fmt.Print(scenario("router", false, true))
	}
}
