package scheduler

import "math/big"

func gcd(m, n int) int {
	x, y, z := new(big.Int), new(big.Int), new(big.Int)
	a := new(big.Int).SetUint64(uint64(m))
	b := new(big.Int).SetUint64(uint64(n))
	return int(z.GCD(x, y, a, b).Int64())
}
