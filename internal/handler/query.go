package handler

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// allowQuery rejects requests carrying query parameters other than keys with 400.
// Unknown parameters would otherwise be ignored silently: a typo like staff_id=1, or the
// staff_ids[]=1 form some clients send, would quietly return the unfiltered widget.
func allowQuery(keys ...string) gin.HandlerFunc {
	allowed := "none"
	if len(keys) > 0 {
		allowed = strings.Join(keys, ", ")
	}
	return func(c *gin.Context) {
		var unknown []string
		for k := range c.Request.URL.Query() {
			if !slices.Contains(keys, k) {
				unknown = append(unknown, fmt.Sprintf("%q", k))
			}
		}
		if len(unknown) > 0 {
			slices.Sort(unknown)
			WriteError(c, fmt.Errorf("%w: unknown query parameter %s; allowed: %s",
				ErrInvalidQuery, strings.Join(unknown, ", "), allowed))
			return
		}
		c.Next()
	}
}
