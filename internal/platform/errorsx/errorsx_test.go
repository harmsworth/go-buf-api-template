package errorsx

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errNotFound = New("THING_NOT_FOUND", "thing not found", http.StatusNotFound, codes.NotFound)

// TestSpecOf_DeclaredError 直接命中声明好的领域错误。
func TestSpecOf_DeclaredError(t *testing.T) {
	spec := SpecOf(errNotFound)
	if spec.HTTP != http.StatusNotFound {
		t.Fatalf("HTTP = %d, want %d", spec.HTTP, http.StatusNotFound)
	}
	if spec.GRPC != codes.NotFound {
		t.Fatalf("GRPC = %v, want %v", spec.GRPC, codes.NotFound)
	}
}

// TestSpecOf_WrappedError 包装后的错误仍能解析出声明处的语义。
func TestSpecOf_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("%w: invalid page_token", New("X", "invalid argument", http.StatusBadRequest, codes.InvalidArgument))

	spec := SpecOf(wrapped)
	if spec.HTTP != http.StatusBadRequest {
		t.Fatalf("HTTP = %d, want %d", spec.HTTP, http.StatusBadRequest)
	}
	if spec.GRPC != codes.InvalidArgument {
		t.Fatalf("GRPC = %v, want %v", spec.GRPC, codes.InvalidArgument)
	}

	// Message 保留包装链上的细节（调用方需要知道 filter 哪里写错了）。
	if got := Message(wrapped); !strings.Contains(got, "invalid page_token") {
		t.Fatalf("Message() = %q, 应保留包装细节", got)
	}
}

// TestSpecOf_UnknownError 未声明的错误一律视为内部错误。
func TestSpecOf_UnknownError(t *testing.T) {
	unknown := errors.New("some driver failure")

	spec := SpecOf(unknown)
	if spec.HTTP != http.StatusInternalServerError {
		t.Fatalf("HTTP = %d, want 500", spec.HTTP)
	}
	if spec.GRPC != codes.Internal {
		t.Fatalf("GRPC = %v, want Internal", spec.GRPC)
	}
}

// TestMessage_RedactsInternalError 内部错误的原文不得外泄。
func TestMessage_RedactsInternalError(t *testing.T) {
	leaky := errors.New("pq: password authentication failed for user root")

	if got := Message(leaky); got != defaultInternalMessage {
		t.Fatalf("Message() = %q, 应脱敏为 %q", got, defaultInternalMessage)
	}
	if got := Message(errNotFound); got != "thing not found" {
		t.Fatalf("Message() = %q, 已声明的错误应原样返回", got)
	}
}

// TestStatus_DeclaredError 领域错误转成对应的 gRPC code。
func TestStatus_DeclaredError(t *testing.T) {
	err := Status(errNotFound)

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Status() 未返回 gRPC status: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("Code = %v, want NotFound", st.Code())
	}
	if st.Message() != "thing not found" {
		t.Fatalf("Message = %q", st.Message())
	}
}

// TestStatus_PassesThroughExistingStatus 已是 gRPC status 的错误必须原样透传，
// 否则 protovalidate 的 InvalidArgument 会被误判成 Internal。
func TestStatus_PassesThroughExistingStatus(t *testing.T) {
	original := status.Error(codes.InvalidArgument, "title: value length must be at least 1")

	got := Status(original)
	st, ok := status.FromError(got)
	if !ok {
		t.Fatalf("Status() 未返回 gRPC status: %v", got)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("Code = %v, want InvalidArgument（不得被改写为 Internal）", st.Code())
	}
	if st.Message() != status.Convert(original).Message() {
		t.Fatalf("Message 被改写: %q", st.Message())
	}
}

// TestStatus_UnknownErrorIsRedacted 未知错误回 Internal 且不外泄原文。
func TestStatus_UnknownErrorIsRedacted(t *testing.T) {
	err := Status(errors.New("connection reset by peer"))

	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Fatalf("Code = %v, want Internal", st.Code())
	}
	if st.Message() != defaultInternalMessage {
		t.Fatalf("Message = %q, 应脱敏", st.Message())
	}
}

// TestError_CodeAndError 验证机器可读码与 error 接口。
func TestError_CodeAndError(t *testing.T) {
	if got := errNotFound.Code(); got != "THING_NOT_FOUND" {
		t.Fatalf("Code() = %q", got)
	}
	if got := errNotFound.Error(); got != "thing not found" {
		t.Fatalf("Error() = %q", got)
	}
}
