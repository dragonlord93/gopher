package dsa

import (
	"fmt"
	"testing"
)

func TestRotate(t *testing.T) {
	t.Run("test nxn clockwise rotations", func(t *testing.T) {
		mat := [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}
		ClockWiseRotate(mat)
		fmt.Println(mat)
	})

	t.Run("test nxn clockwise rotations", func(t *testing.T) {
		mat := [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}
		AntiClockWiseRotate(mat)
		fmt.Println(mat)
	})
}

/* Clockwise
1 2 3
4 5 6
7 8 9

7 4 1
8 5 2
9 6 3
*/

/* Anti-Clockwise
1 2 3
4 5 6
7 8 9

3 6 9
2 5 8
1 4 7
*/

//===============

/*
1 2 3
4 5 6

4 1
5 2
6 3
*/

func Transpose(mat [][]int) [][]int {
	n := len(mat)
	m := len(mat[0])
	result := make([][]int, m)
	for i := range result {
		result[i] = make([]int, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < m; j++ {
			result[j][i] = mat[i][j]
		}
	}
	return result
}
