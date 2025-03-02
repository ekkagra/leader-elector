package utils

import (
	"slices"

	"golang.org/x/exp/constraints"
)

type Number interface {
	constraints.Integer | constraints.Float
}

func Avg[T Number](s []T, excludePct float32) float64 {
	slices.Sort(s)
	exclude := int(float32(len(s)) * excludePct)

	var sum T
	for _, val := range s[exclude : len(s)-exclude] {
		sum = sum + val
	}
	return float64(sum) / float64(len(s)-exclude-exclude)
}

func MapSlice[T any, U any](s []T, m func(T) (U, bool)) []U {
	out := make([]U, 0, len(s))

	for _, ele := range s {
		if val, add := m(ele); add {
			out = append(out, val)
		}
	}

	return out
}

func AllMapSlice[T any, U any](s []T, m func(T) U) []U {
	return MapSlice(s, func(t T) (U, bool) {
		return m(t), true
	})
}
