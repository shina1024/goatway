package scheduler

import "math/big"

func gcd(m, n int) int {
	x, y, z := new(big.Int), new(big.Int), new(big.Int)
	a := new(big.Int).SetUint64(uint64(m)) //nolint:gosec // scheduler weights are validated as non-negative before gcd calculation
	b := new(big.Int).SetUint64(uint64(n)) //nolint:gosec // scheduler weights are validated as non-negative before gcd calculation
	return int(z.GCD(x, y, a, b).Int64())
}
