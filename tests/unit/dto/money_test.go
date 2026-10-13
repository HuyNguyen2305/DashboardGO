package dto_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"dashboardgo/internal/dto"
)

func TestMoney(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1487.4", "1487.40"},
		{"867.26", "867.26"},
		{"0", "0.00"},
		{"2.005", "2.01"},
		{"2.004", "2.00"},
		{"-65", "-65.00"},
		{"1234567890.1", "1234567890.10"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, dto.Money(decimal.RequireFromString(tc.in)))
		})
	}
}

func TestMoneyIsExactForFloatTraps(t *testing.T) {
	// 0.1 + 0.2 is 0.30000000000000004 in float64; decimals must stay exact.
	sum := decimal.RequireFromString("0.1").Add(decimal.RequireFromString("0.2"))
	assert.Equal(t, "0.30", dto.Money(sum))
	assert.True(t, sum.Equal(decimal.RequireFromString("0.3")))
}
