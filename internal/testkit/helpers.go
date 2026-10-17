package testkit

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// uniqueSuffix 生成只含字母数字的短唯一后缀（不依赖 time.Now，避免与架构时间守卫冲突）。
func uniqueSuffix() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 读取失败属于环境异常；退化为固定前缀由调用方测试失败暴露。
		return "0000000000000000"
	}
	return hex.EncodeToString(buf)
}

// IntegrationSuffix 生成用于隔离本次测试数据的唯一后缀。
func IntegrationSuffix() string { return uniqueSuffix() }

// IntegrationShortSuffix 返回较短的后缀，用于受 32 位长度限制的用户名（如 grant_target_xxx）。
func IntegrationShortSuffix() string {
	suffix := IntegrationSuffix()
	if len(suffix) > 12 {
		return suffix[len(suffix)-12:]
	}
	return suffix
}

// SubstituteRouteParams 把 Gin 路径参数替换为占位值 "1"，便于直接发起请求。
func SubstituteRouteParams(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			segments[index] = "1"
		}
	}
	return strings.Join(segments, "/")
}
