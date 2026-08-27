// sfclone 上传去除提示音的参考 WAV 到硅基流动，并生成一条自定义音色试听。
// API Key 只从 SILICONFLOW_API_KEY 环境变量读取，避免写入源码和命令参数。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/provider/asr"
	"github.com/swallow-sun/swallow-go/internal/provider/tts"
)

func main() {
	defaultName := "swallow_yangyang_" + time.Now().Format("20060102_150405")
	ref := flag.String("ref", "tts/data/voice_ref_mp4_intro.wav", "无伴奏且内容完整的参考 WAV")
	refText := flag.String("ref-text", "我听见了，我听见你我相隔。", "参考音频准确台词")
	text := flag.String("text", "你终于回家了。", "要完整合成的文本")
	name := flag.String("name", defaultName, "账户内唯一的自定义音色名")
	out := flag.String("out", "tts/data/siliconflow/finally_home_verified.wav", "通过 ASR 完整性校验后的输出 WAV")
	attempts := flag.Int("attempts", 3, "内容不完整时的最大生成次数")
	flag.Parse()

	apiKey := os.Getenv("SILICONFLOW_API_KEY")
	if apiKey == "" {
		fatal("请先设置环境变量 SILICONFLOW_API_KEY")
	}
	provider := tts.NewSiliconFlow(tts.Config{
		BaseURL: "https://api.siliconflow.cn/v1", APIKey: apiKey,
		Model: tts.SFDModel, OutputFormat: "wav", SampleRate: 16000, Speed: 0.92,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	voice, err := provider.UploadVoice(ctx, *ref, *name, *refText)
	if err != nil {
		fatal("上传自定义音色失败: %v", err)
	}
	fmt.Printf("自定义音色创建成功: %s\n", voice)

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fatal("创建输出目录失败: %v", err)
	}
	checker := asr.NewOpenAICompat(asr.Config{
		BaseURL: "https://api.siliconflow.cn/v1", APIKey: apiKey,
		Model: "FunAudioLLM/SenseVoiceSmall",
	})
	for attempt := 1; attempt <= *attempts; attempt++ {
		audio, err := provider.Synthesize(ctx, tts.SynthesizeRequest{Text: *text, Voice: voice})
		if err != nil {
			fmt.Fprintf(os.Stderr, "第 %d 次生成失败: %v\n", attempt, err)
			continue
		}
		candidate := candidatePath(*out, attempt)
		if err := os.WriteFile(candidate, audio.AudioData, 0o600); err != nil {
			fatal("保存第 %d 次候选失败: %v", attempt, err)
		}
		transcription, err := checker.Transcribe(ctx, asr.TranscribeRequest{
			AudioData: audio.AudioData, AudioFormat: "wav", Language: "zh",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "第 %d 次 ASR 校验失败: %v\n", attempt, err)
			continue
		}
		fmt.Printf("第 %d 次识别结果: %s\n", attempt, transcription.Text)
		if containsNormalized(transcription.Text, *text) {
			if err := os.WriteFile(*out, audio.AudioData, 0o600); err != nil {
				fatal("保存最终语音失败: %v", err)
			}
			fmt.Printf("完整性校验通过，已保存: %s (%d bytes)\n", *out, len(audio.AudioData))
			return
		}
	}
	fatal("连续 %d 次均未识别出完整文本 %q；候选文件已保留，请勿作为正式音频使用", *attempts, *text)
}

// candidatePath 为每次生成保留独立文件，便于比较模型实际输出。
func candidatePath(output string, attempt int) string {
	ext := filepath.Ext(output)
	return strings.TrimSuffix(output, ext) + fmt.Sprintf("_attempt_%d", attempt) + ext
}

// containsNormalized 忽略常见中英文标点和空白后检查目标句是否完整出现。
func containsNormalized(actual, expected string) bool {
	replacer := strings.NewReplacer(
		"，", "", "。", "", "？", "", "！", "", "、", "",
		",", "", ".", "", "?", "", "!", "", " ", "", "\n", "", "\r", "", "\t", "",
	)
	return strings.Contains(replacer.Replace(actual), replacer.Replace(expected))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
