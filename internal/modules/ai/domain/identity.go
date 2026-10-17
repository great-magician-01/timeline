package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 用户名规则与人类账号完全一致（docs/DATA_MODEL.md 6.1）。
var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,31}$`)

// 身份字段长度上限；system_prompt 允许更长。
const (
	MaxNicknameRunes      = 50
	MaxBioRunes           = 500
	MaxIdentityFieldRunes = 1000
	MaxSystemPromptRunes  = 8000
	MaxLanguageTagRunes   = 20
	MaxModelOverrideRunes = 200
)

// CreateInput 是创建 AI 用户所需的资料（字段名与旧请求体一致，JSON tag 在 http/inputs.go）。
type CreateInput struct {
	Username       string
	Nickname       string
	Bio            string
	Background     string
	Personality    string
	CoreValues     string
	LanguageStyle  string
	Topics         string
	TargetLanguage string
	Taboos         string
	SystemPrompt   string
}

// UpdateInput 是更新 AI 资料与身份的补丁（nil 表示不变更）。
type UpdateInput struct {
	Username       *string
	Nickname       *string
	Bio            *string
	Background     *string
	Personality    *string
	CoreValues     *string
	LanguageStyle  *string
	Topics         *string
	TargetLanguage *string
	Taboos         *string
	SystemPrompt   *string
}

// TrimCreateInput 归一化创建输入（昵称缺省为用户名，目标语言缺省 zh-CN）。
func TrimCreateInput(in CreateInput) CreateInput {
	in.Username = strings.TrimSpace(in.Username)
	in.Nickname = strings.TrimSpace(in.Nickname)
	if in.Nickname == "" {
		in.Nickname = in.Username
	}
	in.Bio = strings.TrimSpace(in.Bio)
	in.TargetLanguage = strings.TrimSpace(in.TargetLanguage)
	if in.TargetLanguage == "" {
		in.TargetLanguage = "zh-CN"
	}
	return in
}

// ValidateCreate 校验创建 AI 的资料与身份字段，返回字段名到错误文案的映射。
func ValidateCreate(in CreateInput) map[string]string {
	details := map[string]string{}
	username := strings.TrimSpace(in.Username)
	nickname := strings.TrimSpace(in.Nickname)
	if nickname == "" {
		nickname = username
	}
	if !usernamePattern.MatchString(username) {
		details["username"] = "用户名需为 3-32 位，以字母开头，只能包含字母、数字和下划线"
	}
	if runes := utf8.RuneCountInString(nickname); runes < 1 || runes > MaxNicknameRunes {
		details["nickname"] = "昵称需为 1-50 个字符"
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Bio)) > MaxBioRunes {
		details["bio"] = "简介不能超过 500 个字符"
	}
	addIdentityDetails(details, in.Background, in.Personality, in.CoreValues,
		in.LanguageStyle, in.Topics, in.Taboos)
	if utf8.RuneCountInString(in.SystemPrompt) > MaxSystemPromptRunes {
		details["system_prompt"] = fmt.Sprintf("自定义提示词不能超过 %d 个字符", MaxSystemPromptRunes)
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.TargetLanguage)) > MaxLanguageTagRunes {
		details["target_language"] = "目标语言标识不能超过 20 个字符"
	}
	return details
}

// ValidateUpdate 校验资料更新补丁。
func ValidateUpdate(in UpdateInput) map[string]string {
	details := map[string]string{}
	if in.Username != nil {
		if !usernamePattern.MatchString(strings.TrimSpace(*in.Username)) {
			details["username"] = "用户名需为 3-32 位，以字母开头，只能包含字母、数字和下划线"
		}
	}
	if in.Nickname != nil {
		runes := utf8.RuneCountInString(strings.TrimSpace(*in.Nickname))
		if runes < 1 || runes > MaxNicknameRunes {
			details["nickname"] = "昵称需为 1-50 个字符"
		}
	}
	if in.Bio != nil && utf8.RuneCountInString(strings.TrimSpace(*in.Bio)) > MaxBioRunes {
		details["bio"] = "简介不能超过 500 个字符"
	}
	values := make(map[string]*string, 6)
	if in.Background != nil {
		values["background"] = in.Background
	}
	if in.Personality != nil {
		values["personality"] = in.Personality
	}
	if in.CoreValues != nil {
		values["core_values"] = in.CoreValues
	}
	if in.LanguageStyle != nil {
		values["language_style"] = in.LanguageStyle
	}
	if in.Topics != nil {
		values["topics"] = in.Topics
	}
	if in.Taboos != nil {
		values["taboos"] = in.Taboos
	}
	for field, value := range values {
		if value != nil && utf8.RuneCountInString(*value) > MaxIdentityFieldRunes {
			details[field] = fmt.Sprintf("该字段不能超过 %d 个字符", MaxIdentityFieldRunes)
		}
	}
	if in.SystemPrompt != nil && utf8.RuneCountInString(*in.SystemPrompt) > MaxSystemPromptRunes {
		details["system_prompt"] = fmt.Sprintf("自定义提示词不能超过 %d 个字符", MaxSystemPromptRunes)
	}
	if in.TargetLanguage != nil && utf8.RuneCountInString(strings.TrimSpace(*in.TargetLanguage)) > MaxLanguageTagRunes {
		details["target_language"] = "目标语言标识不能超过 20 个字符"
	}
	return details
}

// ValidateModelOverride 校验模型覆盖名长度。
func ValidateModelOverride(value string) error {
	if utf8.RuneCountInString(strings.TrimSpace(value)) > MaxModelOverrideRunes {
		return fmt.Errorf("模型覆盖名不能超过 200 个字符")
	}
	return nil
}

// addIdentityDetails 校验身份长文本字段。
func addIdentityDetails(details map[string]string, background, personality, coreValues, languageStyle, topics, taboos string) {
	fields := map[string]string{
		"background":     background,
		"personality":    personality,
		"core_values":    coreValues,
		"language_style": languageStyle,
		"topics":         topics,
		"taboos":         taboos,
	}
	for field, value := range fields {
		if utf8.RuneCountInString(value) > MaxIdentityFieldRunes {
			details[field] = fmt.Sprintf("该字段不能超过 %d 个字符", MaxIdentityFieldRunes)
		}
	}
}
