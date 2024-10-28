package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Healthz(c *gin.Context) {
	c.String(http.StatusOK, "WebP Server Go up and running!🥳")
}
