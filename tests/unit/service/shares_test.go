package service_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"dashboardgo/internal/service"
)

func TestShares(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  []float64
	}{
		// Rounded on their own these were 47.04 + 2.22 + 18.94 + 31.81 = 100.01.
		{"screenshot invoices", []string{"1378.64", "65.00", "555.14", "932.26"}, []float64{47.03, 2.22, 18.94, 31.81}},
		{"three equal parts, earliest gets the spare 0.01", []string{"1", "1", "1"}, []float64{33.34, 33.33, 33.33}},
		{"largest remainder wins over position", []string{"1", "2"}, []float64{33.33, 66.67}},
		{"single part", []string{"42.50"}, []float64{100}},
		{"zero parts are 0", []string{"0", "5", "0"}, []float64{0, 100, 0}},
		{"all zero", []string{"0", "0"}, []float64{0, 0}},
		{"empty", nil, []float64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := make([]decimal.Decimal, len(tc.parts))
			for i, p := range tc.parts {
				parts[i] = decimal.RequireFromString(p)
			}
			got := service.Shares(parts)
			assert.Equal(t, tc.want, got)

			sum := decimal.Zero
			for _, g := range got {
				sum = sum.Add(decimal.NewFromFloat(g))
			}
			if !decimal.Sum(decimal.Zero, parts...).IsZero() {
				assert.True(t, sum.Equal(decimal.NewFromInt(100)), "shares sum to %s", sum)
			}
		})
	}
}
