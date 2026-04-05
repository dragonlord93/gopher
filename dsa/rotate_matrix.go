package dsa

// (r, c) -> (c, n-r-1)
func ClockWiseRotate(mat [][]int) {
	n := len(mat)
	m := len(mat[0])
	for i := 0; i <= n/2; i++ {
		for j := 0; j < m/2; j++ {
			temp := mat[i][j]
			mat[i][j] = mat[n-j-1][i]
			mat[n-j-1][i] = mat[n-i-1][n-j-1]
			mat[n-i-1][n-j-1] = mat[j][n-i-1]
			mat[j][n-i-1] = temp
		}
	}
}

// (r, c) -> (n-c-1, r)
func AntiClockWiseRotate(mat [][]int) {
	n := len(mat)
	m := len(mat)
	for i := 0; i < n/2; i++ {
		for j := 0; j <= m/2; j++ {
			temp := mat[i][j]
			mat[i][j] = mat[j][n-i-1]
			mat[j][n-i-1] = mat[n-i-1][n-j-1]
			mat[n-i-1][n-j-1] = mat[n-j-1][i]
			mat[n-j-1][i] = temp
		}
	}
}
