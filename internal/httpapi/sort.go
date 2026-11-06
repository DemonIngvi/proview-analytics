package httpapi

import (
	"cmp"
	"slices"
	"strconv"
)

// sortOptions orders numeric values numerically and the rest alphabetically.
func sortOptions(opts []Option) {
	slices.SortFunc(opts, func(a, b Option) int {
		ai, aerr := strconv.ParseUint(a.Value, 10, 64)
		bi, berr := strconv.ParseUint(b.Value, 10, 64)
		if aerr == nil && berr == nil {
			return cmp.Compare(ai, bi)
		}
		return cmp.Compare(a.Value, b.Value)
	})
}
