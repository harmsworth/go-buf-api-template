// Package errorsx 统一领域错误的传输层映射。
//
// 背景：改造前四处平行实现同一件事，且语义已经分叉——
//   - internal/todo/handler.go  writeStatusError
//   - internal/user/handler.go  writeDomainError
//   - internal/todo/server.go   gRPCError(err, internalMsg)   ← 带 internalMsg 形参
//   - internal/user/server.go   gRPCError(err)                ← 不带，连签名都不统一
//
// 分叉实例：ErrInvalidCredentials 在 HTTP 是 401、gRPC 是 Unauthenticated，
// 但在 ChangePassword 里又被 server.go 的一段特例 switch 改成 FailedPrecondition。
// 每新增一个领域错误就要记得去 4 处补 case，漏一处就会静默返回 500。
//
// 本包的核心主张：**错误自身携带映射规则**。声明处一次性定义 HTTP 状态码与 gRPC code，
// 消费方零 switch：
//
//	// 业务包内声明
//	var ErrNotFound = errorsx.New("TODO_NOT_FOUND", "todo not found",
//		http.StatusNotFound, codes.NotFound)
//
//	// Gin 侧
//	httpx.WriteMapped(c, log, err)
//
//	// gRPC 侧：由 UnaryServerInterceptor 统一转换，业务代码直接 return err
package errorsx

import (
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// defaultInternalMessage 是未知错误对外暴露的统一文案（不泄漏内部细节）。
const defaultInternalMessage = "internal server error"

// Error 是携带 HTTP 状态码与 gRPC code 的领域错误。
type Error struct {
	code    string // 稳定的机器可读错误码，可安全写入响应体 / 日志
	message string // 人类可读信息，可安全外泄
	http    int
	grpc    codes.Code
}

// New 声明一个领域错误，并在此处一次性固化它的 HTTP 状态码与 gRPC code。
//
// code 用于机器判定（建议 UPPER_SNAKE，如 USER_ALREADY_EXISTS），
// message 是给调用方看的文案，两者都不应包含内部实现细节。
func New(code, message string, httpStatus int, grpcCode codes.Code) *Error {
	return &Error{code: code, message: message, http: httpStatus, grpc: grpcCode}
}

// Error 实现 error 接口。
func (e *Error) Error() string { return e.message }

// Code 返回机器可读错误码。
func (e *Error) Code() string { return e.code }

// Spec 是一次「错误 → 传输层状态码」的解析结果。
type Spec struct {
	HTTP int
	GRPC codes.Code
}

// SpecOf 解析 err 的传输层语义：沿错误链寻找 *Error，因此支持
// fmt.Errorf("%w: invalid page_token", ErrInvalidArgument) 这类包装。
// 找不到即视为未知内部错误 → 500 / Internal。
func SpecOf(err error) Spec {
	var e *Error
	if errors.As(err, &e) {
		return Spec{HTTP: e.http, GRPC: e.grpc}
	}
	return Spec{HTTP: http.StatusInternalServerError, GRPC: codes.Internal}
}

// HTTPStatus 返回 err 对应的 HTTP 状态码。
func HTTPStatus(err error) int { return SpecOf(err).HTTP }

// Message 返回可安全外泄的错误文案：未知内部错误统一替换为固定文案。
//
// 已声明的领域错误保留 err.Error()（含 %w 包装链上的细节，例如
// "invalid argument: parse filter: ..."），因为这类细节正是调用方需要知道
// 「filter 哪里写错了」——与改造前 handler 的行为一致，不引入行为变更。
func Message(err error) string {
	if SpecOf(err).HTTP == http.StatusInternalServerError {
		return defaultInternalMessage
	}
	return err.Error()
}

// Status 把领域错误转换为 gRPC status error（未知错误不外泄原文）。
//
// 已经是 gRPC status 的错误（如 protovalidate 拦截器产生的 InvalidArgument、
// recovery 产生的 Internal）**原样透传**，否则会被本函数误判为未知错误而改写：
// 校验失败必须保持 InvalidArgument，不能变成 Internal。
func Status(err error) error {
	if s, ok := status.FromError(err); ok {
		return s.Err()
	}
	spec := SpecOf(err)
	if spec.GRPC == codes.Internal {
		return status.Error(spec.GRPC, defaultInternalMessage)
	}
	return status.Error(spec.GRPC, err.Error())
}
