package ai

import "testing"

func TestProviderDisableConflict(t *testing.T) {
	cases := []struct {
		name         string
		before, next bool
		aiCount      int
		wantBlocked  bool
		wantCount    int
	}{
		{"启用改停用且有 AI", true, false, 2, true, 2},
		{"启用改停用但无 AI", true, false, 0, false, 0},
		{"停用改启用", false, true, 3, false, 0},
		{"保持启用", true, true, 3, false, 0},
		{"保持停用", false, false, 3, false, 0},
	}
	for _, tc := range cases {
		count, blocked := providerDisableConflict(tc.before, tc.next, tc.aiCount)
		if blocked != tc.wantBlocked || count != tc.wantCount {
			t.Errorf("%s: got (%d, %v), want (%d, %v)",
				tc.name, count, blocked, tc.wantCount, tc.wantBlocked)
		}
	}
}
