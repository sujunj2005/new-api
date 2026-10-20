package common

import "testing"

func TestIsImageGenerationModel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"glm-image", "glm-image", true},
		{"glm-image 变体（子串匹配）", "glm-image-flash", true},
		{"glm 聊天模型不受影响", "glm-4.6", false},
		{"dall-e-3", "dall-e-3", true},
		{"gpt-image-1", "gpt-image-1", true},
		{"大小写不敏感", "GLM-Image", true},
		{"imagen 走 prefix 规则", "imagen-4.0-generate-001", true},
		{"flux 子串匹配", "flux-1.1-pro", true},
		{"普通聊天模型", "gpt-4o", false},
		{"claude 模型不受影响", "claude-sonnet-4-5", false},
		{"空串", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsImageGenerationModel(c.in); got != c.want {
				t.Fatalf("IsImageGenerationModel(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
