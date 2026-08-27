// ttsclone 用一段本地参考音频创建智谱私有音色，并生成清晰 WAV 试听文件。
// API Key 只从 ZHIPU_API_KEY 环境变量读取，避免密钥进入命令历史或仓库。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/provider/tts"
)

func main() {
	// 智谱要求 voice_name 在当前账户内唯一；时间戳避免重复运行时名称冲突。
	defaultVoiceName := "swallow_voice_" + time.Now().Format("20060102_150405")
	ref := flag.String("ref", "tts/data/voice_ref_wechat_2_635_clean.wav", "新微信视频第 2 秒起完整句降噪参考 WAV")
	refText := flag.String("ref-text", "听到这条录音的时候，我可能已经不在了，不要难过。", "参考音频原文")
	text := flag.String("text", "你好啊。", "创建音色时使用的试听文本")
	name := flag.String("name", defaultVoiceName, "智谱平台中的唯一音色名称")
	outDir := flag.String("out-dir", "tts/data/zhipu_common", "常用语 WAV 输出目录")
	longText := flag.String("long-text",
		"你好，很高兴再次见到你。今天我们可以慢慢聊天，别担心，我会认真听你说完。",
		"用于二次克隆的长参考台词")
	longOut := flag.String("long-out", "tts/data/voice_ref_zhipu_long.wav",
		"智谱生成的长参考 WAV 路径")
	flag.Parse()

	apiKey := os.Getenv("ZHIPU_API_KEY")
	if apiKey == "" {
		fatal("请先设置环境变量 ZHIPU_API_KEY")
	}
	if info, err := os.Stat(*ref); err != nil {
		fatal("参考音频不可用: %v", err)
	} else if info.Size() > 10*1024*1024 {
		fatal("参考音频超过智谱 10 MB 限制")
	}

	provider := tts.NewZhipu(tts.Config{APIKey: apiKey, OutputFormat: "wav", Speed: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	clone, err := provider.CloneVoice(ctx, *ref, *name, *refText, *text)
	if err != nil {
		fatal("创建音色失败: %v", err)
	}
	fmt.Printf("音色创建成功: %s\n", clone.Voice)

	// 先生成一条台词明确、音素覆盖更丰富的长音频。后续生成常用语时以它作为
	// 参考，能避免短素材导致的语气词漂移和咬字不稳定。
	longAudio, err := provider.Synthesize(ctx, tts.SynthesizeRequest{
		Text: *longText, Voice: clone.Voice,
	})
	if err != nil {
		fatal("生成长参考音频失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*longOut), 0o755); err != nil {
		fatal("创建长参考音频目录失败: %v", err)
	}
	if err := os.WriteFile(*longOut, longAudio.AudioData, 0o600); err != nil {
		fatal("保存长参考音频失败: %v", err)
	}
	fmt.Printf("长参考音频已生成: %s (%d bytes)\n", *longOut, len(longAudio.AudioData))
	fmt.Printf("长参考音频台词: %s\n", *longText)

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("创建输出目录失败: %v", err)
	}
	phrases := []struct {
		name string
		text string
	}{
		{"01_like", "我喜欢你。"},
		{"02_love", "我爱你。"},
		{"03_hello", "你好啊。"},
		{"04_shy_bendan", "笨蛋……"},
		{"05_yangyang_sleep", "阳阳，你怎么还没睡啊？"},
	}
	for _, phrase := range phrases {
		audio, err := provider.Synthesize(ctx, tts.SynthesizeRequest{Text: phrase.text, Voice: clone.Voice})
		if err != nil {
			fatal("生成 %q 失败: %v", phrase.text, err)
		}
		out := filepath.Join(*outDir, phrase.name+".wav")
		if err := os.WriteFile(out, audio.AudioData, 0o600); err != nil {
			fatal("保存 %q 失败: %v", phrase.text, err)
		}
		fmt.Printf("已生成 %-12s %s (%d bytes)\n", phrase.text, out, len(audio.AudioData))
	}
	fmt.Printf("服务端配置 voice = %q 后即可复用该音色。\n", strings.TrimSpace(clone.Voice))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
