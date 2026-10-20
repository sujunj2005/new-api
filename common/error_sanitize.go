package common

import (
	"encoding/json"
	"strings"
)

// OutboundErrorFallbackMessage 是返回给客户的通用英文兜底文案：
// 当对外错误信息含中文字符时整体替换为该文案，避免海外客户看到中文报错。
const OutboundErrorFallbackMessage = "Service error. Please try again later or contact support."

// ContainsCJK 判断字符串是否包含至少一个 CJK 统一表意文字。
// 覆盖区间：U+4E00-U+9FFF（基本区）、U+3400-U+4DBF（扩展A）、U+F900-U+FAFF（兼容表意）。
func ContainsCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) ||
			(r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0xF900 && r <= 0xFAFF) {
			return true
		}
	}
	return false
}

// SanitizeOutboundErrorMessage 对出站错误 message 做语言净化：
// 含中文时替换为 OutboundErrorFallbackMessage，否则原样返回。
func SanitizeOutboundErrorMessage(msg string) string {
	if ContainsCJK(msg) {
		return OutboundErrorFallbackMessage
	}
	return msg
}

// SanitizeErrorChunkData 净化 OpenAI 格式流式错误 chunk：
// 仅当 chunk 顶层 error 对象的 message 为含中文的字符串时，
// 将其改写为 OutboundErrorFallbackMessage 后重新序列化返回；
// 其余情况（含正常内容 chunk、英文错误、非字符串 message、嵌套 error 等）一律原样返回，字节级不变。
func SanitizeErrorChunkData(data string) string {
	// 预检：JSON 字符串值内的 "error" 字样必被转义为 \"error\"，与预检串不匹配，
	// 因此只有顶层存在 error 键的 chunk 才会进入解析。
	if !strings.Contains(data, `"error"`) {
		return data
	}

	var topLevel map[string]json.RawMessage
	if err := UnmarshalJsonStr(data, &topLevel); err != nil {
		return data
	}

	rawError, ok := topLevel["error"]
	if !ok || GetJsonType(rawError) != "object" {
		return data
	}

	var errorObject map[string]json.RawMessage
	if err := Unmarshal(rawError, &errorObject); err != nil {
		return data
	}

	rawMessage, ok := errorObject["message"]
	if !ok {
		return data
	}

	// 仅处理字符串形态的 message；数字、对象、数组、null 均视为不匹配而放行。
	var message string
	if err := Unmarshal(rawMessage, &message); err != nil {
		return data
	}
	if !ContainsCJK(message) {
		return data
	}

	fallback, err := Marshal(OutboundErrorFallbackMessage)
	if err != nil {
		return data
	}
	errorObject["message"] = json.RawMessage(fallback)

	newError, err := Marshal(errorObject)
	if err != nil {
		return data
	}
	topLevel["error"] = json.RawMessage(newError)

	newData, err := Marshal(topLevel)
	if err != nil {
		return data
	}
	return string(newData)
}
