package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContainsCJK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"纯 ASCII", "no available channel for model gpt-4o", false},
		{"空串", "", false},
		{"中文", "没有可用服务商", true},
		{"扩展A区", "\u3400", true},
		{"兼容表意", "\uF900", true},
		{"中英混合", "bad response status 分组不存在", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ContainsCJK(c.in); got != c.want {
				t.Fatalf("ContainsCJK(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeOutboundErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"中文 message", "没有可用服务商", OutboundErrorFallbackMessage},
		{"英文 message", "Rate limit reached for gpt-4o", "Rate limit reached for gpt-4o"},
		{"中日韩混合", "服务不可用 / サービス / 서비스", OutboundErrorFallbackMessage},
		{"空串", "", ""},
		{"含扩展A区字符", "error \u3400 detail", OutboundErrorFallbackMessage},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeOutboundErrorMessage(c.in); got != c.want {
				t.Fatalf("SanitizeOutboundErrorMessage(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeErrorChunkDataReplacesChineseMessage 最小错误 chunk：仅 error.message 被改写。
func TestSanitizeErrorChunkDataReplacesChineseMessage(t *testing.T) {
	in := `{"error":{"message":"没有可用服务商"}}`
	want := `{"error":{"message":"Service error. Please try again later or contact support."}}`

	got := SanitizeErrorChunkData(in)
	if got != want {
		t.Fatalf("SanitizeErrorChunkData(%q) = %q, want %q", in, got, want)
	}
}

// TestSanitizeErrorChunkDataKeepsOtherFields 其余字段值与精度保持不变。
func TestSanitizeErrorChunkDataKeepsOtherFields(t *testing.T) {
	// total_tokens = 2^53+1，float64 无法精确表示，用于验证 json.RawMessage 未做数值重编码。
	in := `{"id":"chatcmpl-1","error":{"message":"上游返回错误","type":"upstream_error","code":500},"usage":{"total_tokens":9007199254740993}}`

	got := SanitizeErrorChunkData(in)
	if got == in {
		t.Fatalf("含中文 error.message 的 chunk 未被替换: %q", got)
	}
	if ContainsCJK(got) {
		t.Fatalf("净化后仍含中文字符: %q", got)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("净化结果不是合法 JSON: %v, got %q", err, got)
	}

	var parsedError map[string]json.RawMessage
	if err := json.Unmarshal(parsed["error"], &parsedError); err != nil {
		t.Fatalf("error 字段解析失败: %v", err)
	}

	var message string
	if err := json.Unmarshal(parsedError["message"], &message); err != nil {
		t.Fatalf("message 字段解析失败: %v", err)
	}
	if message != OutboundErrorFallbackMessage {
		t.Fatalf("message = %q, want %q", message, OutboundErrorFallbackMessage)
	}

	var errType string
	if err := json.Unmarshal(parsedError["type"], &errType); err != nil {
		t.Fatalf("type 字段解析失败: %v", err)
	}
	if errType != "upstream_error" {
		t.Fatalf("type = %q, want %q", errType, "upstream_error")
	}

	var id string
	if err := json.Unmarshal(parsed["id"], &id); err != nil {
		t.Fatalf("id 字段解析失败: %v", err)
	}
	if id != "chatcmpl-1" {
		t.Fatalf("id = %q, want %q", id, "chatcmpl-1")
	}

	if !strings.Contains(got, `"code":500`) {
		t.Fatalf("code 字段未按原值保留: %q", got)
	}
	if !strings.Contains(got, `"total_tokens":9007199254740993`) {
		t.Fatalf("大整数未按原精度保留: %q", got)
	}
}

// TestSanitizeErrorChunkDataKeepsChunksByteIdentical 不需要替换的 chunk 必须字节级原样返回。
func TestSanitizeErrorChunkDataKeepsChunksByteIdentical(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"英文错误 chunk", `{"error":{"message":"Upstream rate limit reached"}}`},
		{"不含 error 子串的正常中文内容 chunk", `{"choices":[{"delta":{"content":"你好，世界"}}]}`},
		{"含转义 error 字样的正常中文内容 chunk", `{"choices":[{"delta":{"content":"这里提到了 \"error\" 这个单词，请忽略"}}]}`},
		{"预检命中但无顶层 error 键", `{"result":{"error":{"message":"中文错误"}}}`},
		{"顶层 error 值为非对象", `{"error":"没有可用服务商"}`},
		{"error.message 为对象", `{"error":{"message":{"text":"中文"}}}`},
		{"error.message 为数字", `{"error":{"message":123}}`},
		{"error.message 为 null", `{"error":{"message":null}}`},
		{"error 对象无 message 字段", `{"error":{"type":"upstream_error"}}`},
		{"非法 JSON 但含 error 子串", `{"error":`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeErrorChunkData(c.in); got != c.in {
				t.Fatalf("SanitizeErrorChunkData(%q) = %q, want byte-identical", c.in, got)
			}
		})
	}
}

// TestSanitizeErrorChunkDataToleratesWhitespace 带空白的错误 chunk 同样被净化。
func TestSanitizeErrorChunkDataToleratesWhitespace(t *testing.T) {
	in := `{"error": {"message": "上游服务不可用"}}`
	want := `{"error":{"message":"Service error. Please try again later or contact support."}}`

	if got := SanitizeErrorChunkData(in); got != want {
		t.Fatalf("SanitizeErrorChunkData(%q) = %q, want %q", in, got, want)
	}
}
