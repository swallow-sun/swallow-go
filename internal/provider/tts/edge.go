// edge.go 实现 edge-tts (微软免费 TTS) 的 WebSocket 协议.
//
// 做的事情:
//  1. NewEdge: 创建 Provider 实例, 配置语音和输出格式.
//  2. Synthesize: 建 WebSocket 连接 -> 发 config + SSML 消息 -> 接收音频 -> 关闭连接.
//
// edge-tts 协议:
//   - WebSocket 连接微软 Azure 语音服务的公开端点 (不需要 API key).
//   - 连接后发两条文本消息: synthesis (输出格式) 和 ssml (合成请求).
//   - 服务端返回文本消息 (Path: audio.metadata) 和二进制消息 (音频数据).
//   - 二进制消息前 2 字节是大端序 uint16, 表示后面跟着的头部字符串的长度.
//   - 头部里 Path:audio 表示这是音频数据, 截取头部后面的字节就是 WAV 音频.
//   - 收到 Path:Turn.off 表示合成结束, 关闭连接.
package tts

import (
	// binary 读大端序 uint16
	"encoding/binary"
	// context 传给 WebSocket 拨号, 控制超时取消
	"context"
	// fmt 格式化 error
	"fmt"
	// strings 拼接 SSML
	"strings"
	// time 超时控制
	"time"

	// gorilla/websocket 是 Go 生态最常用的 WebSocket 库
	"github.com/gorilla/websocket"
)

// NewEdge 用 Config 创建一个 edge-tts Provider 实例.
func NewEdge(cfg Config) *EdgeTTS {
	// 如果配置没传语音或格式, 用默认值
	if cfg.Voice == "" {
		cfg.Voice = DefaultVoice
	}
	if cfg.OutputFormat == "" {
		cfg.OutputFormat = DefaultOutputFormat
	}
	if cfg.Rate == "" {
		cfg.Rate = DefaultRate
	}
	if cfg.Volume == "" {
		cfg.Volume = DefaultVolume
	}
	if cfg.Pitch == "" {
		cfg.Pitch = DefaultPitch
	}
	return &EdgeTTS{config: cfg}
}

// Synthesize 把文字转成 MP3 音频.
// 流程: 拨号 WebSocket -> 发 config 消息 -> 发 SSML 消息 -> 循环读消息收集音频 -> 返回.
func (p *EdgeTTS) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	// 选择语音: 请求指定了就用请求的, 没指定用配置里的默认值
	voice := req.Voice
	if voice == "" {
		voice = p.config.Voice
	}

	// 第一步: 拨号 WebSocket.
	// websocket.DefaultDialer.DialContext 连接 WebSocket 端点, 带 30 秒超时.
	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}
	// 连接微软 edge-tts 端点, 带 Origin 头模拟浏览器 (微软要求 Origin)
	conn, _, err := dialer.DialContext(ctx, EdgeTTSWsURL, nil)
	if err != nil {
		return SynthesizeResponse{}, fmt.Errorf("websocket dial: %w", err)
	}
	// defer 在函数返回时关闭 WebSocket 连接
	defer conn.Close()

	// 第二步: 发送 config 消息 (输出格式).
	// edge-tts 协议要求先发一条 config 消息, 告诉服务端输出什么格式的音频.
	// 消息格式: X-Timestamp...\r\nPath: synthesis\r\nContent-Type: application/json\r\n\r\n{...}
	// edge-tts 协议要求发一条 synthesis 配置消息, 告诉服务端输出什么格式.
	// Path 头必须是英文 "synthesis", 不能是中文.
	configMsg := "X-Timestamp:SwallowTTS\r\n" +
		"Path:synthesis\r\n" +
		"Content-Type:application/json\r\n\r\n" +
		`{"context":{"synthesis":{"audio":{"metadata":{"timestamp":{"@type":"CanvasTime","Resolution":"MilliSeconds"}}}}}}`
	// 用 WriteMessage 发送文本消息 (TextMessage = 1)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(configMsg)); err != nil {
		return SynthesizeResponse{}, fmt.Errorf("send config: %w", err)
	}

	// 第三步: 发送 SSML 消息 (合成请求).
	// SSML = Speech Synthesis Markup Language, 语音合成标记语言.
	// 里面指定语音名称、语速、音量、音调和要合成的文字.
	ssml := buildSSML(voice, p.config.Rate, p.config.Volume, p.config.Pitch, req.Text)
	// 生成 request ID (格式 111111111111111111111111111111111{随机})
	// 这里用时间戳拼接, 只要唯一就行
	requestID := fmt.Sprintf("111111111111111111111111111111111%d", time.Now().UnixNano())
	ssmlMsg := "X-RequestId:" + requestID + "\r\n" +
		"Path:ssml\r\n" +
		"Content-Type:application/ssml+xml\r\n\r\n" +
		ssml
	if err := conn.WriteMessage(websocket.TextMessage, []byte(ssmlMsg)); err != nil {
		return SynthesizeResponse{}, fmt.Errorf("send ssml: %w", err)
	}

	// 第四步: 循环读取 WebSocket 消息, 收集音频数据.
	var audioData []byte
	// 读消息循环: 一直读直到收到 Turn.off 或出错
	for {
		// 检查 context 是否已取消 (超时或调用方取消)
		select {
		case <-ctx.Done():
			return SynthesizeResponse{}, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		// ReadMessage 返回消息类型 (1=文本, 2=二进制) 和消息内容
		_, message, err := conn.ReadMessage()
		if err != nil {
			return SynthesizeResponse{}, fmt.Errorf("read message: %w", err)
		}

		// 二进制消息 (BinaryMessage = 2) 才包含音频数据
		if len(message) < 2 {
			continue // 太短, 跳过
		}

		// 前两字节是大端序 uint16, 表示头部长度
		headerLen := int(binary.BigEndian.Uint16(message[:2]))
		// 头部从第 3 字节开始, 头部后面是音频数据
		headerEnd := 2 + headerLen
		if headerEnd > len(message) {
			continue // 数据不完整, 跳过
		}

		// 解析头部字符串, 找 Path 字段
		header := string(message[2:headerEnd])
		// Path:audio 表示这帧有音频数据
		if strings.Contains(header, "Path:audio") {
			// 头部后面的字节就是音频数据 (MP3 片段)
			audioData = append(audioData, message[headerEnd:]...)
		}
		// Path:Turn.off 表示合成结束
		if strings.Contains(header, "Path:Turn.off") {
			break
		}
	}

	// 如果没收到任何音频数据, 返回错误
	if len(audioData) == 0 {
		return SynthesizeResponse{}, fmt.Errorf("no audio data received")
	}

	// edge-tts 输出的是裸 PCM 数据 (riff-16khz-16bit-mono-pcm 格式), 不带 WAV 文件头.
	// 需要手动加上 44 字节的 WAV 头, C++ 侧 waveOut API 才能正确解析和播放.
	wavData := wrapWAVHeader(audioData, 16000, 16, 1)

	// 返回音频数据 (WAV 格式, C++ 侧用 waveOut API 直接播放, 不需要 MP3 解码器)
	return SynthesizeResponse{
		AudioData:   wavData,
		AudioFormat: "wav",
	}, nil
}

// wrapWAVHeader 给裸 PCM 数据加上标准的 WAV 文件头 (44 字节).
// 参数: sampleRate=采样率 (如 16000), bitsPerSample=位深 (如 16), channels=声道数 (1=单声道).
// WAV 文件格式: "RIFF" + 文件大小 + "WAVE" + "fmt " + fmt 块大小 + PCM 格式参数 + "data" + 数据大小 + PCM 数据.
func wrapWAVHeader(pcm []byte, sampleRate, bitsPerSample, channels int) []byte {
	// 计算各种大小
	numChannels := channels
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	dataSize := len(pcm)
	chunkSize := 36 + dataSize // RIFF chunk 大小 = 文件总大小 - 8

	// 分配 WAV 头 + PCM 数据的空间
	result := make([]byte, 44+len(pcm))

	// RIFF 头
	copy(result[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(result[4:8], uint32(chunkSize))
	copy(result[8:12], []byte("WAVE"))

	// fmt 子块
	copy(result[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(result[16:20], 16)       // fmt 块大小固定 16
	binary.LittleEndian.PutUint16(result[20:22], 1)        // PCM 格式 = 1
	binary.LittleEndian.PutUint16(result[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(result[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(result[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(result[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(result[34:36], uint16(bitsPerSample))

	// data 子块
	copy(result[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	copy(result[44:], pcm)

	return result
}

// buildSSML 构造 SSML 请求消息.
// SSML 格式:
//
//	<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'>
//	  <voice name='zh-CN-XiaoxiaoNeural'>
//	    <prosody rate='+0%' volume='+0%' pitch='+0Hz'>
//	      要合成的文字
//	    </prosody>
//	  </voice>
//	</speak>
func buildSSML(voice, rate, volume, pitch, text string) string {
	return fmt.Sprintf(
		"<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'>"+
			"<voice name='%s'>"+
			"<prosody rate='%s' volume='%s' pitch='%s'>"+
			"%s"+
			"</prosody></voice></speak>",
		voice, rate, volume, pitch, escapeXML(text),
	)
}

// escapeXML 转义 XML 特殊字符, 防止文本破坏 SSML 结构.
func escapeXML(s string) string {
	// 先替换 & (其他替换可能引入新的 &), 再替换其他特殊字符
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
