package middles

import (
	"net/http"
	"runtime/debug"
	"ttsapi/logger"

	"github.com/gin-gonic/gin"
)

func Recovery() Middle {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.Warnf(c, "panic recovered: err = %v, stack = %s\n", err, debug.Stack())
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
