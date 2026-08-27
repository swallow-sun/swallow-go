// promptgen 批量生成设备常用语，并用 ASR 检查台词是否完整。
// 只有校验通过的 WAV 才会写入 approved_prompts，供 C++ 客户端打包。
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode"

	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/provider/asr"
	"github.com/swallow-sun/swallow-go/internal/provider/tts"
)

type phrase struct {
	file string
	text string
}

func main() {
	outDir := flag.String("out-dir", "tts/data/approved_prompts", "校验通过的输出目录")
	attempts := flag.Int("attempts", 4, "每条台词最多生成次数")
	force := flag.Bool("force", false, "重新生成已经校验通过的常用语")
	voice := flag.String("voice", "", "临时覆盖 [tts.aliyun].voice；默认使用配置文件音色")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fatal("加载配置失败: %v", err)
	}
	if cfg.TTS.Provider != "aliyun" {
		fatal("常用语必须使用阿里云生成，请先把 [tts].provider 设为 aliyun")
	}
	ttsConfig := cfg.TTS.SelectedTTSProviderConfig()
	selectedVoice := ttsConfig.Voice
	if *voice != "" {
		selectedVoice = *voice
	}
	if ttsConfig.APIKey == "" {
		fatal("请先填写 [tts.aliyun].api_key")
	}

	asrConfig := cfg.ASR.SelectedASRProviderConfig()
	checker, err := asr.NewProvider(cfg.ASR.Provider, asr.Config{
		BaseURL: asrConfig.BaseURL, APIKey: asrConfig.APIKey, Model: asrConfig.Model,
		Language: asrConfig.Language, EnableITN: asrConfig.EnableITN,
	})
	if err != nil {
		fatal("创建 ASR 完整性校验器失败: %v", err)
	}
	if checker == nil {
		fatal("ASR 已禁用，无法校验常用语是否完整")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	provider := tts.NewAliyun(tts.Config{
		BaseURL: ttsConfig.BaseURL, APIKey: ttsConfig.APIKey,
		WorkspaceID: ttsConfig.WorkspaceID, Model: ttsConfig.Model,
		Voice: selectedVoice, SampleRate: ttsConfig.SampleRate, Speed: ttsConfig.Speed,
	})
	fmt.Printf("使用阿里云模型: %s，官方音色: %s\n", ttsConfig.Model, selectedVoice)
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("创建输出目录失败: %v", err)
	}

	phrases := []phrase{
		{"startup_first.wav", "你来啦。今天想先做点什么？我陪着你。"},
		{"startup_return.wav", "你回来啦。要继续刚才的事，还是聊点别的？"},
		{"thinking_warmup.wav", "嗯，让我想一下。"},
		{"listening_ready.wav", "我在听，你慢慢说。"},
		{"didnt_hear.wav", "刚才没听清，你再说一遍好吗？"},
		{"network_retry.wav", "网络有点慢，我再试一下。"},
		{"task_done.wav", "好啦，已经弄好了。"},
		{"comfort.wav", "别急，我在呢。我们慢慢来。"},
		{"goodbye.wav", "好吧，那你早点回来。"},
		{"goodnight.wav", "晚安。好好休息，明天见。"},
	}
	failed := make([]string, 0)
	for _, item := range phrases {
		path := filepath.Join(*outDir, item.file)
		if !*force && fileExists(path) {
			fmt.Printf("已存在校验素材，跳过: %s\n", path)
			continue
		}
		if *force {
			// 强制生成时先移除旧供应商留下的素材；新版本校验失败后不能误用旧音频。
			_ = os.Remove(path)
		}
		if !generateVerified(ctx, provider, checker, selectedVoice, *outDir, item, *attempts) {
			failed = append(failed, item.file)
		}
	}
	if len(failed) > 0 {
		fatal("以下常用语未通过完整性校验: %v", failed)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func generateVerified(ctx context.Context, provider tts.Provider, checker asr.Provider,
	voice, outDir string, item phrase, attempts int) bool {
	for attempt := 1; attempt <= attempts; attempt++ {
		audio, err := provider.Synthesize(ctx, tts.SynthesizeRequest{
			Text: item.text, Voice: voice,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s 第 %d 次生成失败: %v\n", item.file, attempt, err)
			continue
		}
		result, err := checker.Transcribe(ctx, asr.TranscribeRequest{
			AudioData: audio.AudioData, AudioFormat: "wav", Language: "zh",
		})
		if err != nil || !sameWords(result.Text, item.text) {
			fmt.Fprintf(os.Stderr, "%s 第 %d 次完整性未通过，识别=%q\n",
				item.file, attempt, result.Text)
			continue
		}
		path := filepath.Join(outDir, item.file)
		// 给最后一个字保留播放缓冲，避免 waveOut 在音频数据结束处产生截断听感。
		finalAudio, err := appendTrailingSilence(audio.AudioData, 300*time.Millisecond)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s 无法追加尾部静音: %v\n", item.file, err)
			continue
		}
		if err := os.WriteFile(path, finalAudio, 0o600); err != nil {
			fatal("保存 %s 失败: %v", path, err)
		}
		fmt.Printf("校验通过: %s，台词=%s\n", path, item.text)
		return true
	}
	fmt.Fprintf(os.Stderr, "%s 连续 %d 次未通过完整性校验，不会进入客户端\n", item.file, attempts)
	return false
}

// appendTrailingSilence 在 PCM WAV 的 data 块末尾插入零采样，
// 同时更新 data 和 RIFF 长度；它不会改变已经合成的人声内容。
func appendTrailingSilence(wav []byte, duration time.Duration) ([]byte, error) {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, fmt.Errorf("不是有效的 RIFF/WAVE")
	}
	var byteRate uint32
	dataHeader := -1
	dataStart := -1
	dataSize := 0
	for position := 12; position+8 <= len(wav); {
		size := int(binary.LittleEndian.Uint32(wav[position+4 : position+8]))
		start := position + 8
		available := len(wav) - start
		actualSize := size
		if actualSize > available {
			actualSize = available
		}
		switch string(wav[position : position+4]) {
		case "fmt ":
			if actualSize >= 12 {
				byteRate = binary.LittleEndian.Uint32(wav[start+8 : start+12])
			}
		case "data":
			dataHeader, dataStart, dataSize = position, start, actualSize
		}
		step := 8 + size + size%2
		if step <= 8 || position+step > len(wav) {
			break
		}
		position += step
	}
	if byteRate == 0 || dataHeader < 0 || dataStart < 0 {
		return nil, fmt.Errorf("缺少 fmt 或 data 块")
	}
	silenceBytes := int(float64(byteRate) * duration.Seconds())
	if silenceBytes%2 != 0 {
		silenceBytes++
	}
	dataEnd := dataStart + dataSize
	result := make([]byte, 0, len(wav)+silenceBytes)
	result = append(result, wav[:dataEnd]...)
	result = append(result, bytes.Repeat([]byte{0}, silenceBytes)...)
	result = append(result, wav[dataEnd:]...)
	binary.LittleEndian.PutUint32(result[dataHeader+4:dataHeader+8], uint32(dataSize+silenceBytes))
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(result)-8))
	return result, nil
}

// sameWords 检查 ASR 是否覆盖了完整台词。
// ASR 对“啦/了”、儿化音和少量语气词并不稳定，因此允许极少量编辑差异；
// 同时要求句尾完整，避免真正被截断的音频混入正式素材。
func sameWords(actual, expected string) bool {
	a := []rune(spokenCharacters(actual))
	e := []rune(spokenCharacters(expected))
	if len(a) == 0 || len(e) == 0 || len(a)*100 < len(e)*85 {
		return false
	}
	// 最后四个字必须一致，这是识别音频是否被截断的关键保护。
	tailLength := 4
	if len(e) < tailLength {
		tailLength = len(e)
	}
	if len(a) < tailLength || string(a[len(a)-tailLength:]) != string(e[len(e)-tailLength:]) {
		return false
	}
	allowed := len(e) / 10
	if allowed < 2 {
		allowed = 2
	}
	return editDistance(a, e) <= allowed
}

func spokenCharacters(text string) string {
	result := make([]rune, 0, len([]rune(text)))
	for _, char := range text {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			result = append(result, char)
		}
	}
	return string(result)
}

func editDistance(a, b []rune) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
