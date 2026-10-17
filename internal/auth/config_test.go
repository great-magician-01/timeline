package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// nilPoolProvider 模拟数据库尚未就绪的启动早期状态。
type nilPoolProvider struct{}

func (nilPoolProvider) Pool() *pgxpool.Pool { return nil }

// TestConfigRequiresReadyDatabase 验证公开认证配置接口在数据库未就绪时返回 503，
// 而不是 panic 或返回误导性的默认值。
func TestConfigRequiresReadyDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &Service{pools: nilPoolProvider{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)

	service.Config(c)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", recorder.Code)
	}
	if body := recorder.Body.String(); body == "" {
		t.Fatal("应返回统一 JSON 错误响应")
	}
}
