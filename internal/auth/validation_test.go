package auth

import "testing"

func TestNormalizeRegistration(t *testing.T) {
	in, details := normalizeRegistration(RegisterInput{
		Username: " Valid_User ",
		Email:    "TEST@example.com",
		Password: "password-123",
	})
	if details != nil {
		t.Fatalf("合法注册信息未通过: %v", details)
	}
	if in.Username != "Valid_User" || in.Email != "test@example.com" {
		t.Fatalf("规范化结果错误: %+v", in)
	}

	_, details = normalizeRegistration(RegisterInput{
		Username: "1x",
		Email:    "not-an-email",
		Password: "short",
	})
	for _, field := range []string{"username", "email", "password"} {
		if details[field] == "" {
			t.Errorf("缺少字段 %s 的校验错误", field)
		}
	}
}

func TestValidateProfile(t *testing.T) {
	nickname := "  新昵称  "
	bio := "  简介  "
	if details := validateProfile(&nickname, &bio); details != nil {
		t.Fatal(details)
	}
	if nickname != "新昵称" || bio != "简介" {
		t.Fatalf("资料未正确去除首尾空白: %q %q", nickname, bio)
	}
	empty := " "
	if details := validateProfile(&empty, nil); details["nickname"] == "" {
		t.Fatal("空昵称应被拒绝")
	}
}
