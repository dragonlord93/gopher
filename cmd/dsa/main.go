package main

import (
	"fmt"

	"github.com/dragonlord93/gopher/dsa"
)

func main() {
	res := dsa.Multiply([]int{1, 2, 3, 4, 5, 6}, []int{3, 4, 5, 1})
	fmt.Println(res)
}
