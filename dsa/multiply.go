package dsa

func Multiply(a []int, b []int) []int {
	n := len(a)
	m := len(b)
	res := make([]int, n+m)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			mul := a[i] * b[j]
			res[i+j+1] += mul
			res[i+j] += res[i+j+1] / 10
			res[i+j+1] %= 10
		}
	}
	for i, val := range res {
		if val != 0 {
			return res[i:]
		}
	}
	return res
}
