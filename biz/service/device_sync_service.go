// device_sync_service.go 放设备同步的业务编排层.
//
// 做的事情:
//  1. SyncBatch: 接收设备上报的一批 sync_outbox 条目, 逐条做幂等入库.
//     - "message" 类型: 把消息内容写入 dialogues 表 (用 message_id 做幂等).
//     - "event" 类型: 把事件写入 events 表 (用 event_id 做幂等, 通过 device_sync_log 去重).
//  2. 返回已确认的 item_id 列表, 设备收到后从 sync_outbox 删除.
//
// 设计要点:
//   - 消息幂等: 用 GetDialogueByTraceAndRole 检查是否已存在, 存在则跳过.
//   - 事件幂等: 通过 device_sync_log 表的唯一索引 (device_id, item_id) 去重.
//   - 批量处理: 逐条处理而非事务批量, 单条失败不影响其他条目.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// DeviceSyncService 编排设备同步数据的接收和持久化.
type DeviceSyncService struct {
	deps *Deps
}

// NewDeviceSyncService 创建一个 DeviceSyncService.
func NewDeviceSyncService(deps *Deps) *DeviceSyncService {
	return &DeviceSyncService{deps: deps}
}

// SyncBatchItem 是设备上报的一条同步条目.
// 对应 C++ 侧 SyncOutboxItem 的 JSON 序列化形式.
type SyncBatchItem struct {
	// ItemID 同步条目 ID (C++ 侧的 item_id, 即 event_id 或 message_id)
	ItemID string `json:"item_id"`
	// ItemType 条目类型: "message" 或 "event"
	ItemType string `json:"item_type"`
	// Payload JSON 字符串, 包含完整的消息或事件数据
	Payload string `json:"payload"`
}

// SyncBatchResult 是设备同步的返回结果.
// Acknowledged 是已成功处理的 item_id 列表, 设备收到后从 outbox 删除.
// Failed 是处理失败的 item_id 列表, 设备保留这些条目等待下次重试.
type SyncBatchResult struct {
	// Acknowledged 已确认接收的 item_id 列表 (含已存在跳过的)
	Acknowledged []string `json:"acknowledged"`
	// Failed 处理失败的 item_id 列表
	Failed []string `json:"failed,omitempty"`
}

// SyncBatch 接收设备上报的一批同步条目, 逐条做幂等入库.
// userID 和 deviceID 来自设备认证, 设备不能自行指定.
func (s *DeviceSyncService) SyncBatch(
	ctx context.Context,
	deviceID string,
	userID int64,
	items []SyncBatchItem,
) (SyncBatchResult, error) {
	result := SyncBatchResult{
		Acknowledged: make([]string, 0, len(items)),
	}

	for _, item := range items {
		// 先写 device_sync_log 做全局幂等.
		// 如果已存在, 说明之前收过了, 直接确认让设备删除.
		created, err := s.deps.repo.InsertDeviceSyncLog(ctx, data.DeviceSyncLog{
			DeviceID: deviceID,
			UserID:   userID,
			ItemID:   item.ItemID,
			ItemType: item.ItemType,
			Payload:  item.Payload,
		})
		if err != nil {
			logger.Error("sync: insert device_sync_log failed",
				zap.String("device_id", deviceID),
				zap.String("item_id", item.ItemID),
				zap.Error(err),
			)
			result.Failed = append(result.Failed, item.ItemID)
			continue
		}

		// created=false 表示之前已经收过了, 确认让设备删除.
		if !created {
			result.Acknowledged = append(result.Acknowledged, item.ItemID)
			continue
		}

		// 首次接收, 根据 item_type 做具体持久化.
		if err := s.processItem(ctx, deviceID, userID, item); err != nil {
			logger.Error("sync: process item failed",
				zap.String("device_id", deviceID),
				zap.String("item_id", item.ItemID),
				zap.String("item_type", item.ItemType),
				zap.Error(err),
			)
			// 具体业务数据没有落库时，撤销幂等占位，允许设备下次重试。
			// 只有真正处理成功的条目才能返回 acknowledged。
			if deleteErr := s.deps.repo.DeleteDeviceSyncLog(ctx, deviceID, item.ItemID); deleteErr != nil {
				return result, fmt.Errorf("rollback failed sync log for %s: %w", item.ItemID, deleteErr)
			}
			result.Failed = append(result.Failed, item.ItemID)
		} else {
			result.Acknowledged = append(result.Acknowledged, item.ItemID)
		}
	}

	return result, nil
}

// processItem 根据 item_type 把 payload 持久化到对应表.
// "message" -> dialogues 表 (会话历史), "event" -> events 表 (埋点事件).
func (s *DeviceSyncService) processItem(
	ctx context.Context,
	deviceID string,
	userID int64,
	item SyncBatchItem,
) error {
	switch item.ItemType {
	case "message":
		return s.processMessage(ctx, deviceID, userID, item)
	case "event":
		return s.processEvent(ctx, deviceID, userID, item)
	default:
		logger.Warn("sync: unknown item type",
			zap.String("device_id", deviceID),
			zap.String("item_id", item.ItemID),
			zap.String("item_type", item.ItemType),
		)
		return fmt.Errorf("unknown sync item type %q", item.ItemType)
	}
}

// deviceSyncMessage 是 C++ 侧 buildMessageJson 生成的 JSON 结构.
type deviceSyncMessage struct {
	MessageID string `json:"message_id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	TraceID   string `json:"trace_id"`
	Timestamp int64  `json:"timestamp"`
}

// processMessage 把设备上报的消息写入 dialogues 表.
// 用 message_id (即 trace_id + "-" + role) 检查是否已存在, 做幂等.
func (s *DeviceSyncService) processMessage(
	ctx context.Context,
	deviceID string,
	userID int64,
	item SyncBatchItem,
) error {
	var msg deviceSyncMessage
	if err := json.Unmarshal([]byte(item.Payload), &msg); err != nil {
		return err
	}

	// 用 trace_id + role 查是否已存在.
	// C++ 侧 message_id 格式是 "trace_id-role", 所以从 message_id 反解.
	// 但更稳妥的做法是直接用 message_id 当 client_message_id,
	// 通过 GetDialogueByTraceAndRole 来查. 这里用 payload 里的 trace_id + role.
	existing, err := s.deps.repo.GetDialogueByTraceAndRole(ctx, msg.TraceID, msg.Role)
	if err == nil && existing.ID > 0 {
		// 已经存过了, 跳过.
		return nil
	}

	// 插入 dialogues 表.
	// 设备上报的消息没有 token 用量信息 (那是在 Go 侧调 LLM 时产生的),
	// 所以 TokenUsage 全部为 0.
	// 设备侧的 session_id 可能和 Go 侧创建的 session_id 一致 (设备通过 ensureSession 创建过).
	_, err = s.deps.repo.InsertDialogue(ctx, msg.SessionID, userID, msg.Role, msg.Content, data.TokenUsage{}, msg.TraceID)
	return err
}

// deviceSyncEvent 是 C++ 侧 buildEventJson 生成的 JSON 结构.
type deviceSyncEvent struct {
	EventID   string `json:"event_id"`
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	TraceID   string `json:"trace_id"`
	Timestamp int64  `json:"timestamp"`
	Payload   string `json:"payload"`
}

// processEvent 把设备上报的事件写入 events 表.
func (s *DeviceSyncService) processEvent(
	ctx context.Context,
	deviceID string,
	userID int64,
	item SyncBatchItem,
) error {
	var evt deviceSyncEvent
	if err := json.Unmarshal([]byte(item.Payload), &evt); err != nil {
		return err
	}

	// 构造事件类型: "device_" + type, 如 "device_record", "device_asr" 等.
	// 加前缀避免和 Go 侧自己的事件类型冲突.
	eventType := "device_" + evt.Type
	success := evt.Status == "ok"

	// 把事件 payload 包装成 event_data JSON.
	eventData := item.Payload

	// 写入 events 表.
	// durationMs 为 0 (设备侧事件没有耗时信息).
	return s.deps.repo.InsertEvent(ctx, eventType, &userID, eventData, 0, success, evt.TraceID)
}

// 保留 time 包引用, 后续如果需要解析 timestamp 可以用到.
var _ = time.Now
