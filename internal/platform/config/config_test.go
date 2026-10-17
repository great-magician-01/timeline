package config

import "testing"

func TestParseTrustedProxies(t *testing.T) {
	proxies, err := parseTrustedProxies("127.0.0.1, 10.0.0.0/8,::1")
	if err != nil {
		t.Fatal(err)
	}
	if len(proxies) != 3 || proxies[1] != "10.0.0.0/8" {
		t.Fatalf("代理解析结果错误: %v", proxies)
	}
	for _, invalid := range []string{"proxy.example.com", "127.0.0.1,,::1", "999.1.1.1", "0.0.0.0/0", "::/0"} {
		if _, err := parseTrustedProxies(invalid); err == nil {
			t.Errorf("非法代理配置应被拒绝: %q", invalid)
		}
	}
	if proxies, err := parseTrustedProxies(" "); err != nil || proxies != nil {
		t.Fatalf("空配置应表示不信任代理: %v %v", proxies, err)
	}
}
