// wav_stream.go 提供流式 WAV 的边界解析和 PCM 分帧能力。
//
// 流式 HTTP 的 Read 边界只是网络分片，不等于 WAV 块边界，也不等于 PCM
// 采样边界。尤其是硅基流动返回的 WAV 会在 fmt 和 data 之间插入 LIST/AIGC
// 元数据，data 不一定从第 44 字节开始。这里统一按 RIFF 规范查找 data 块，
// 再按 blockAlign 重组音频帧，避免把元数据或半个采样送给播放器。
package tts

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const maxStreamingWAVHeaderBytes = 1 << 20 // 防止异常块长度导致头部无限缓存。

// WAVFormat 是 WAV fmt 块中播放器真正需要的格式信息。
type WAVFormat struct {
	AudioFormat   uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
}

// PCMFrameStats 记录固定分帧后的 PCM 数据量，供链路日志使用。
type PCMFrameStats struct {
	Bytes  int
	Chunks int
}

// BuildPCM16MonoWAVHeader 构造标准 44 字节 PCM WAV 头。
// 流式场景尚不知道最终长度时传 dataSize=0；客户端只用头部初始化播放格式。
func BuildPCM16MonoWAVHeader(sampleRate int, dataSize uint32) ([]byte, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate %d", sampleRate)
	}

	const (
		channels      = uint16(1)
		bitsPerSample = uint16(16)
		blockAlign    = channels * bitsPerSample / 8
	)
	byteRate := uint32(sampleRate) * uint32(blockAlign)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1) // WAVE_FORMAT_PCM
	binary.LittleEndian.PutUint16(header[22:24], channels)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	return header, nil
}

// ReadStreamingWAVHeader 从任意碎片化的 reader 中解析 RIFF/WAVE 头。
//
// 返回的 initialPCM 是解析 data 块时已经被同一次 Read 顺带读到的 PCM，调用方
// 必须先处理它，再继续读取 reader。函数会跳过 fmt 与 data 之间的 LIST、JUNK、
// AIGC 等任意合法 RIFF 块，不能用固定 44 字节偏移替代。
func ReadStreamingWAVHeader(reader io.Reader) (WAVFormat, []byte, error) {
	if reader == nil {
		return WAVFormat{}, nil, errors.New("wav reader is nil")
	}

	headerBuffer := make([]byte, 0, 512)
	readBuffer := make([]byte, 8192)
	for {
		format, dataOffset, complete, err := parseStreamingWAVHeader(headerBuffer)
		if err != nil {
			return WAVFormat{}, nil, err
		}
		if complete {
			initialPCM := append([]byte(nil), headerBuffer[dataOffset:]...)
			return format, initialPCM, nil
		}
		if len(headerBuffer) >= maxStreamingWAVHeaderBytes {
			return WAVFormat{}, nil, fmt.Errorf("wav header exceeds %d bytes", maxStreamingWAVHeaderBytes)
		}

		n, readErr := reader.Read(readBuffer)
		if n > 0 {
			remaining := maxStreamingWAVHeaderBytes - len(headerBuffer)
			if n > remaining {
				return WAVFormat{}, nil, fmt.Errorf("wav header exceeds %d bytes", maxStreamingWAVHeaderBytes)
			}
			headerBuffer = append(headerBuffer, readBuffer[:n]...)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				// io.Reader 允许在最后一次读取时同时返回数据和 EOF。刚读到的
				// 字节里可能已经包含完整 data 块头，必须先回到循环顶部解析，
				// 不能直接把一个合法的短 WAV 误报为头部截断。
				if n > 0 {
					continue
				}
				return WAVFormat{}, nil, io.ErrUnexpectedEOF
			}
			return WAVFormat{}, nil, fmt.Errorf("read wav header: %w", readErr)
		}
		if n == 0 {
			return WAVFormat{}, nil, io.ErrNoProgress
		}
	}
}

// parseStreamingWAVHeader 在当前缓存里尝试查找 fmt 和 data 块。
// complete=false 表示数据还不够，调用方应继续读取；dataOffset 指向 PCM 第一个字节。
func parseStreamingWAVHeader(buffer []byte) (format WAVFormat, dataOffset int, complete bool, err error) {
	if len(buffer) < 12 {
		return WAVFormat{}, 0, false, nil
	}
	if !bytes.Equal(buffer[0:4], []byte("RIFF")) || !bytes.Equal(buffer[8:12], []byte("WAVE")) {
		return WAVFormat{}, 0, false, errors.New("audio stream is not RIFF/WAVE")
	}

	position := 12
	fmtFound := false
	for {
		if len(buffer) < position+8 {
			return WAVFormat{}, 0, false, nil
		}

		chunkID := string(buffer[position : position+4])
		chunkSize := uint64(binary.LittleEndian.Uint32(buffer[position+4 : position+8]))
		payloadOffset := position + 8

		// data 的长度在流式 WAV 中经常是 0 或占位大数；找到块头即可开始读 PCM，
		// 不能等待声明长度的全部内容到齐。
		if chunkID == "data" {
			if !fmtFound {
				return WAVFormat{}, 0, false, errors.New("wav data chunk appears before fmt chunk")
			}
			return format, payloadOffset, true, nil
		}

		paddedSize := chunkSize + chunkSize%2
		chunkEnd := uint64(payloadOffset) + paddedSize
		if chunkEnd > maxStreamingWAVHeaderBytes {
			return WAVFormat{}, 0, false, fmt.Errorf("wav %q chunk is too large: %d", chunkID, chunkSize)
		}
		if uint64(len(buffer)) < chunkEnd {
			return WAVFormat{}, 0, false, nil
		}

		if chunkID == "fmt " && !fmtFound {
			if chunkSize < 16 {
				return WAVFormat{}, 0, false, fmt.Errorf("wav fmt chunk is too short: %d", chunkSize)
			}
			payload := buffer[payloadOffset : payloadOffset+16]
			format = WAVFormat{
				AudioFormat:   binary.LittleEndian.Uint16(payload[0:2]),
				Channels:      binary.LittleEndian.Uint16(payload[2:4]),
				SampleRate:    binary.LittleEndian.Uint32(payload[4:8]),
				ByteRate:      binary.LittleEndian.Uint32(payload[8:12]),
				BlockAlign:    binary.LittleEndian.Uint16(payload[12:14]),
				BitsPerSample: binary.LittleEndian.Uint16(payload[14:16]),
			}
			if format.SampleRate == 0 || format.Channels == 0 || format.BlockAlign == 0 || format.BitsPerSample == 0 {
				return WAVFormat{}, 0, false, fmt.Errorf("wav fmt chunk has invalid values: %+v", format)
			}
			fmtFound = true
		}

		position = int(chunkEnd)
	}
}

// StreamAlignedPCM 把任意网络碎片重组为固定大小且 blockAlign 对齐的 PCM 帧。
// 除最后一帧外，每帧都是 frameBytes；若流最终停在半个采样上，返回错误而不是
// 丢一个字节后继续播放，避免后续样本整体错位产生吱吱声。
func StreamAlignedPCM(
	reader io.Reader,
	initialPCM []byte,
	blockAlign int,
	frameBytes int,
	emit func([]byte) error,
) (PCMFrameStats, error) {
	if reader == nil {
		return PCMFrameStats{}, errors.New("pcm reader is nil")
	}
	if emit == nil {
		return PCMFrameStats{}, errors.New("pcm emit callback is nil")
	}
	if blockAlign <= 0 {
		return PCMFrameStats{}, fmt.Errorf("invalid pcm block align %d", blockAlign)
	}
	frameBytes -= frameBytes % blockAlign
	if frameBytes <= 0 {
		return PCMFrameStats{}, fmt.Errorf("pcm frame size is smaller than block align: %d", blockAlign)
	}

	combined := io.MultiReader(bytes.NewReader(initialPCM), reader)
	frame := make([]byte, frameBytes)
	stats := PCMFrameStats{}
	for {
		n, readErr := io.ReadFull(combined, frame)
		if n > 0 {
			if n%blockAlign != 0 {
				return stats, fmt.Errorf("pcm stream ended with %d unaligned bytes (block_align=%d)", n, blockAlign)
			}
			// emit 可能异步保存切片，复制一份避免下一轮 ReadFull 覆盖已提交数据。
			chunk := append([]byte(nil), frame[:n]...)
			if err := emit(chunk); err != nil {
				return stats, err
			}
			stats.Bytes += n
			stats.Chunks++
		}

		switch {
		case readErr == nil:
			continue
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			return stats, nil
		default:
			return stats, fmt.Errorf("read pcm stream: %w", readErr)
		}
	}
}
