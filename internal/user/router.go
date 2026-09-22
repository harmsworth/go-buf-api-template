package user

import "github.com/gin-gonic/gin"

// RegisterRoutes 将 User 模块的路由注册到给定的 Gin 路由（Engine 或 RouterGroup）。
//
// 路径与 api/user/v1/user.proto 中的 google.api.http 注解保持一致；
// 例外：proto 的自定义方法路径 /v1/users/{user_id}:changePassword 含段内冒号，
// Gin 路由解析器不支持（一个路径段只能有一个通配符），故改为 /change-password 后缀形式，
// 原生 :changePassword 形态由 grpc-gateway 提供（见 server.go）。
func RegisterRoutes(r gin.IRouter, h *Handler) {
	// 认证
	r.POST("/v1/auth/login", h.Login)

	// 用户资源
	g := r.Group("/v1/users")
	{
		g.POST("", h.CreateUser)
		g.GET("", h.ListUsers)
		g.GET("/:user_id", h.GetUser)
		g.PATCH("/:user_id", h.UpdateUser)
		g.DELETE("/:user_id", h.DeleteUser)

		// 自定义方法（AIP-136）
		g.POST("/:user_id/change-password", h.ChangePassword)
		g.POST("/:user_id/reset-password", h.ResetPassword)
	}
}
