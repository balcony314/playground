package probe

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HandleReadyProbe 就绪探针HTTP handler：已就绪返回200，未就绪返回503
func HandleReadyProbe(c *gin.Context) {
	if ready {
		c.String(http.StatusOK, "server is ready")
	} else {
		c.String(http.StatusServiceUnavailable, "server is not ready")
	}
}

// HandleHealthyProbe 健康探针HTTP handler：健康返回200，不健康返回503
func HandleHealthyProbe(c *gin.Context) {
	if healthy {
		c.String(http.StatusOK, "server is healthy")
	} else {
		c.String(http.StatusServiceUnavailable, "server is not healthy")
	}
}
