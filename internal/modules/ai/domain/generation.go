package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 生成参数约束。
const (
	MaxTokensLimit       = 100000
	MaxStopSequences     = 8
	MaxStopSequenceRunes = 64
)

// ParseGenerationParamsInput 严格解析 HTTP 输入的生成参数：拒绝未知键、非法类型与越界值。
func ParseGenerationParamsInput(raw json.RawMessage) (GenerationParams, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return GenerationParams{}, nil
	}
	var payload struct {
		MaxTokens   *int      `json:"max_tokens"`
		Temperature *float64  `json:"temperature"`
		TopP        *float64  `json:"top_p"`
		Stop        *[]string `json:"stop"`
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return GenerationParams{}, fmt.Errorf("生成参数只支持 max_tokens、temperature、top_p、stop")
	}
	params := GenerationParams{}
	if payload.MaxTokens != nil {
		if *payload.MaxTokens < 1 || *payload.MaxTokens > MaxTokensLimit {
			return GenerationParams{}, fmt.Errorf("max_tokens 必须是 1-%d 的整数", MaxTokensLimit)
		}
		params.MaxTokens = *payload.MaxTokens
	}
	if payload.Temperature != nil {
		if *payload.Temperature < 0 || *payload.Temperature > 2 {
			return GenerationParams{}, fmt.Errorf("temperature 必须在 0-2 之间")
		}
		params.Temperature = payload.Temperature
	}
	if payload.TopP != nil {
		if *payload.TopP < 0 || *payload.TopP > 1 {
			return GenerationParams{}, fmt.Errorf("top_p 必须在 0-1 之间")
		}
		params.TopP = payload.TopP
	}
	if payload.Stop != nil {
		if len(*payload.Stop) > MaxStopSequences {
			return GenerationParams{}, fmt.Errorf("stop 最多 %d 条", MaxStopSequences)
		}
		for _, sequence := range *payload.Stop {
			if strings.TrimSpace(sequence) == "" {
				return GenerationParams{}, fmt.Errorf("stop 序列不能为空")
			}
			if utf8.RuneCountInString(sequence) > MaxStopSequenceRunes {
				return GenerationParams{}, fmt.Errorf("单条 stop 序列不能超过 %d 个字符", MaxStopSequenceRunes)
			}
		}
		params.Stop = *payload.Stop
	}
	return params, nil
}

// GenerationParamsFromJSON 宽松解析数据库中的 JSONB：缺失键使用零值，非法类型返回错误，
// 未知键忽略以兼容后续扩展（与旧实现一致）。
func GenerationParamsFromJSON(raw []byte) (GenerationParams, error) {
	var params GenerationParams
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return params, nil
	}
	var payload struct {
		MaxTokens   *int      `json:"max_tokens"`
		Temperature *float64  `json:"temperature"`
		TopP        *float64  `json:"top_p"`
		Stop        *[]string `json:"stop"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return GenerationParams{}, fmt.Errorf("生成参数格式无效: %w", err)
	}
	if payload.MaxTokens != nil {
		params.MaxTokens = *payload.MaxTokens
	}
	if payload.Temperature != nil {
		params.Temperature = payload.Temperature
	}
	if payload.TopP != nil {
		params.TopP = payload.TopP
	}
	if payload.Stop != nil {
		params.Stop = *payload.Stop
	}
	return params, nil
}

// GenerationParamsJSON 把生成参数编码为落库用的 JSONB（未配置的键不落库）。
func GenerationParamsJSON(params GenerationParams) (json.RawMessage, error) {
	payload := map[string]any{}
	if params.MaxTokens > 0 {
		payload["max_tokens"] = params.MaxTokens
	}
	if params.Temperature != nil {
		payload["temperature"] = *params.Temperature
	}
	if params.TopP != nil {
		payload["top_p"] = *params.TopP
	}
	if len(params.Stop) > 0 {
		payload["stop"] = params.Stop
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化生成参数失败: %w", err)
	}
	return json.RawMessage(encoded), nil
}
