package setting

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

var Chats = []map[string]string{
	//{
	//	"ChatGPT Next Web 官方示例": "https://app.nextchat.dev/#/?settings={\"key\":\"{key}\",\"url\":\"{address}\"}",
	//},
	{
		"Cherry Studio": "cherrystudio://providers/api-keys?v=1&data={cherryConfig}",
	},
	{
		"AionUI": "aionui://provider/add?v=1&data={aionuiConfig}",
	},
	{
		"Fluent Read": "fluentread",
	},
	{
		// CC Switch 官方深链协议 V1（原默认值 "ccswitch" 不是合法 scheme，
		// 会被浏览器当作相对路径。文档：https://ccswitch.co/docs/faq-deeplink.html）
		"CC Switch": "ccswitch://v1/import?resource=provider&app=claude&name=New%20API&endpoint={address}&apiKey={key}",
	},
	{
		"DeepChat": "deepchat://provider/install?v=1&data={deepchatConfig}",
	},
	{
		"Lobe Chat": "https://chat-preview.lobehub.com/?settings={\"keyVaults\":{\"openai\":{\"apiKey\":\"{key}\",\"baseURL\":\"{address}/v1\"}}}",
	},
	{
		"AI as Workspace": "https://aiaw.app/set-provider?provider={\"type\":\"openai\",\"settings\":{\"apiKey\":\"{key}\",\"baseURL\":\"{address}/v1\",\"compatibility\":\"strict\"}}",
	},
	{
		"AMA": "ama://set-api-key?server={address}&key={key}",
	},
	{
		"OpenCat": "opencat://team/join?domain={address}&token={key}",
	},
}

func UpdateChatsByJsonString(jsonString string) error {
	Chats = make([]map[string]string, 0)
	return json.Unmarshal([]byte(jsonString), &Chats)
}

func Chats2JsonString() string {
	jsonBytes, err := json.Marshal(Chats)
	if err != nil {
		common.SysLog("error marshalling chats: " + err.Error())
		return "[]"
	}
	return string(jsonBytes)
}
