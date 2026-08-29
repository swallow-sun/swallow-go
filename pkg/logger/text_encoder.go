package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

var textBufferPool = buffer.NewPool()

// textEncoder 先借助 Zap 的 JSON encoder 完整保留字段类型，再输出纯文本 key=value。
// 这样 development 不会出现“文本消息 + JSON 字段”的混合格式。
type textEncoder struct {
	zapcore.Encoder
	timeKey    string
	levelKey   string
	messageKey string
}

func newTextEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &textEncoder{
		Encoder:    zapcore.NewJSONEncoder(cfg),
		timeKey:    cfg.TimeKey,
		levelKey:   cfg.LevelKey,
		messageKey: cfg.MessageKey,
	}
}

func (e *textEncoder) Clone() zapcore.Encoder {
	return &textEncoder{
		Encoder:    e.Encoder.Clone(),
		timeKey:    e.timeKey,
		levelKey:   e.levelKey,
		messageKey: e.messageKey,
	}
}

func (e *textEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	encoded, err := e.Encoder.EncodeEntry(entry, fields)
	if err != nil {
		return nil, err
	}
	defer encoded.Free()

	decoder := json.NewDecoder(bytes.NewReader(encoded.Bytes()))
	decoder.UseNumber()
	values := make(map[string]any)
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode development log entry: %w", err)
	}

	out := textBufferPool.Get()
	appendColumn(out, "🕒 "+prettyTimestamp(fmt.Sprint(values[e.timeKey])))
	appendColumn(out, fmt.Sprint(values[e.levelKey]))
	appendColumn(out, "💬 "+fmt.Sprint(values[e.messageKey]))

	delete(values, e.timeKey)
	delete(values, e.levelKey)
	delete(values, e.messageKey)

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if _, ok := values["startup_id"]; ok {
		keys = append([]string{"startup_id"}, removeKey(keys, "startup_id")...)
	}
	for index, key := range keys {
		if index == 0 {
			out.AppendString("  │  🏷️  ")
		} else {
			out.AppendString("  ·  ")
		}
		out.AppendString(key)
		out.AppendByte('=')
		out.AppendString(textValue(values[key]))
	}
	out.AppendByte('\n')
	return out, nil
}

func removeKey(keys []string, target string) []string {
	result := make([]string, 0, len(keys)-1)
	for _, key := range keys {
		if key != target {
			result = append(result, key)
		}
	}
	return result
}

func appendColumn(out *buffer.Buffer, value string) {
	if out.Len() > 0 {
		out.AppendString("  │  ")
	}
	out.AppendString(value)
}

func prettyTimestamp(value string) string {
	// ISO8601 的 T 适合机器，开发日志换成空格更接近日常日期时间。
	return strings.Replace(value, "T", " ", 1)
}

func textValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		if v != "" && !strings.ContainsAny(v, " \t\r\n=\"") {
			return v
		}
		return strconv.Quote(v)
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return strconv.Quote(fmt.Sprint(v))
		}
		return string(encoded)
	}
}
