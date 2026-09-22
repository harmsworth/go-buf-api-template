package todo

import "github.com/gin-gonic/gin"

// RegisterRoutes 将 Todo 模块的路由注册到给定的 Gin 路由（Engine 或 RouterGroup）。
func RegisterRoutes(r gin.IRouter, h *Handler) {
	g := r.Group("/api/v1/todos")
	{
		g.POST("", h.Create)
		g.GET("", h.List)
		g.GET("/:id", h.Get)
		g.PATCH("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
}
