// Package httpx 收敛 Gin 侧与 Proto 编解码、契约校验、响应输出相关的薄联结层。
//
// 抽取动机：改造前 internal/todo/handler.go 与 internal/user/handler.go 各自实现了一份
// 几乎逐行相同的 readProto / validateReq / writeProto / writeError / writeMappedError
// （合计约 125 行重复代码），新增业务域只能继续复制粘贴。
//
// 边界声明：本包**只做传输协议层面的搬运**，不含任何业务语义。
// 错误 → 状态码的映射由 internal/platform/errorsx 决定（错误在声明处携带映射规则）。
package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"go-buf-api-template/internal/platform/errorsx"

	"buf.build/go/protovalidate"
	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/grpc-ecosystem/grpc-gateway/v2/utilities"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// noQueryFilter 是传给 gateway query 解析器的空过滤器：不做任何字段排除。
//
// 注意不能传 nil —— DefaultQueryParser.Parse 会直接在该参数上调用
// HasCommonPrefix（grpc-gateway 生成的 *.pb.gw.go 同样传空 DoubleArray），
// 传 nil 会 panic 而非报错。空实现只读，可安全并发共享。
var noQueryFilter = &utilities.DoubleArray{}

// Validator 是 protovalidate 校验器（接口类型，便于 wire 注入与测试替身）。
type Validator = protovalidate.Validator

// NewValidator 构建契约校验器，供 wire 注入。
//
// 之所以做成 provider：改造前 Gin 侧（两个 NewHandler 各一次）与 gRPC 侧
// （provideGRPCServer 一次）共构建了三个等价实例，现在进程内共享一份。
func NewValidator() (Validator, error) { return protovalidate.New() }

// Registrar 是业务包向宿主注册 Gin 路由的统一契约，由各业务的 *Handler 实现。
//
// 有了它，宿主可以按切片聚合注册器（见 cmd/server/providers.go 的 provideHTTPRegistrars），
// 新增业务域不需要改动宿主函数的签名。
type Registrar interface {
	RegisterRoutes(gin.IRouter)
}

// ReadBody 读取请求体并用 protojson 解析（兼容 snake_case / lowerCamel 字段名与枚举名）。
//
// 只做反序列化，不执行校验——校验由调用方在补全路径参数后再触发，
// 顺序颠倒会让 id 的 uuid 规则对空值报错。失败时写出 400 并返回 false。
func ReadBody(c *gin.Context, msg proto.Message) bool {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "read request body failed")
		return false
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(body, msg); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// Validate 执行契约中声明的 buf.validate 规则；失败时写出 400 并返回 false。
func Validate(c *gin.Context, v Validator, msg proto.Message) bool {
	if err := v.Validate(msg); err != nil {
		WriteError(c, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// BindQuery 把 URL query 绑定到 proto 请求（GET 列表类接口）。
//
// 直接复用 grpc-gateway 的解析器，因此 **Gin 入口与 gateway 入口的入参语义完全一致**：
// 支持 proto 字段名与 lowerCamel JSON 名、枚举名（status=TODO_STATUS_DONE）与枚举号、
// bool 的标准写法、嵌套与 repeated 字段。
//
// 已知语义（实测）：未知参数被**静默忽略**；已知字段的非法值（非整数 page_size、
// 不存在的枚举名、非法 bool）返回错误 → 400。
//
// 改造前这里是每个 handler 手写的 DefaultQuery + strconv.Atoi + Enum() 拼装，
// 既重复又比 gateway 弱（只认枚举号、只认 "true"/"1"）。
//
// 与 Bind 同理：仅用于**无需补路径参数**的请求。
func BindQuery(c *gin.Context, msg proto.Message) bool {
	if err := runtime.PopulateQueryParameters(msg, c.Request.URL.Query(), noQueryFilter); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid query: "+err.Error())
		return false
	}
	return true
}

// Bind 是 ReadBody + Validate 的连续调用。
//
// 仅用于**无需补路径参数**的方法（如纯 body 入参的 Create）。像 Update 这类
// 需要先把路径参数写进请求消息再校验的场景，必须显式分两步调用，
// 否则 id 的 uuid 规则会对空值报错。
func Bind(c *gin.Context, v Validator, msg proto.Message) bool {
	return ReadBody(c, msg) && Validate(c, v, msg)
}

// WriteProto 以 Proto JSON 输出响应（protojson 默认不输出未填充字段，
// 与 grpc-gateway 侧关闭 EmitUnpopulated 的行为一致）。
func WriteProto(c *gin.Context, status int, msg proto.Message) {
	data, err := protojson.Marshal(msg)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, "marshal response failed")
		return
	}
	c.Data(status, "application/json", data)
}

// WriteError 输出 {"code":<status>,"message":<msg>} 形态的错误响应。
func WriteError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"code": status, "message": message})
}

// WriteMapped 写出领域错误：状态码与文案由错误在**声明处**固化的映射决定
// （见 internal/platform/errorsx），未知错误统一 500、脱敏并记录日志。
//
// 这是改造前 todo/handler.go 的 writeStatusError 与 user/handler.go 的
// writeDomainError 两处 switch 的合体，且不再需要逐 case 列举领域错误。
func WriteMapped(c *gin.Context, log *slog.Logger, err error) {
	spec := errorsx.SpecOf(err)
	if spec.HTTP == http.StatusInternalServerError {
		log.Error("unhandled service error", "error", err)
	}
	WriteError(c, spec.HTTP, errorsx.Message(err))
}

// RequestLogger 用 slog 记录访问日志。
func RequestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http request",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"latency", time.Since(start).String(),
			"client_ip", c.ClientIP(),
		)
	}
}
