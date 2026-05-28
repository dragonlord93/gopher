package main

import "fmt"

// Event that repeats every tuesday
// Event that repeats every first Monday of a month
// Event that repeats every Monday and Wednesday

func gcd(a, b int) int {
	if b%a == 0 {
		return a
	}
	return gcd(b%a, a)
}
func main() {
	fmt.Println(gcd(9, 6))
}
