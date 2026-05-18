package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-skeleton/pkg/errcode"
	applog "go-skeleton/pkg/log"
	"go-skeleton/pkg/validator"
)

func init() {
	applog.SetLogger(zap.NewNop())
	validator.InitValidator()
	gin.SetMode(gin.TestMode)
}

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	return c, w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) Response {
	t.Helper()
	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestWriteSuccessShape(t *testing.T) {
	c, w := newCtx()
	WriteSuccess(c, map[string]int{"n": 1})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decode(t, w)
	if resp.Code != 0 || resp.Message != "success" {
		t.Errorf("envelope = %+v, want code=0 msg=success", resp)
	}
}

func TestWriteErrorMapsKnownErrcode(t *testing.T) {
	c, w := newCtx()
	c.Set("trace_id", "trace-42")
	WriteError(c, errcode.Unauthorized)

	resp := decode(t, w)
	if resp.Code != errcode.Unauthorized.Code() {
		t.Errorf("code = %d, want %d", resp.Code, errcode.Unauthorized.Code())
	}
	if resp.Reason != errcode.Unauthorized.Reason() {
		t.Errorf("reason = %q, want %q", resp.Reason, errcode.Unauthorized.Reason())
	}
	if got, _ := resp.Metadata["trace_id"].(string); got != "trace-42" {
		t.Errorf("metadata.trace_id = %v, want trace-42", resp.Metadata)
	}
}

func TestWriteErrorFallsBackForUnknownError(t *testing.T) {
	c, w := newCtx()
	WriteError(c, errors.New("boom"))

	resp := decode(t, w)
	if resp.Code != errcode.InternalError.Code() {
		t.Errorf("expected InternalError fallback, got code=%d", resp.Code)
	}
	if resp.Reason != errcode.InternalError.Reason() {
		t.Errorf("expected reason %q, got %q", errcode.InternalError.Reason(), resp.Reason)
	}
}

func TestMessageForCoversEveryDeclaredReason(t *testing.T) {
	// Every reason exported from pkg/errcode/common.go should have a default
	// English message in MessageFor (avoid the "operation failed" tar pit).
	declared := []string{
		errcode.InvalidParams.Reason(),
		errcode.Unauthorized.Reason(),
		errcode.PermissionDenied.Reason(),
		errcode.TooManyRequests.Reason(),
		errcode.RequestTimeout.Reason(),
		errcode.DatabaseError.Reason(),
		errcode.QueueUnavailable.Reason(),
		errcode.QueueError.Reason(),
	}
	for _, r := range declared {
		if msg := MessageFor(r); msg == "" || msg == "operation failed" {
			t.Errorf("reason %q: MessageFor returned generic %q", r, msg)
		}
	}
}
