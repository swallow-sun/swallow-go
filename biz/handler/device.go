// device.go 放设备注册和设备身份接口.
//
// 做的事情:
//  1. RegisterDevice:主人认证后注册设备,返回只出现一次的设备令牌.
//  2. GetCurrentDevice:使用设备令牌认证,返回当前设备公开信息.
//  3. CreateDeviceSession:设备认证后创建属于自己的对话会话.
//  4. DeviceChat:设备认证后复用共用云端聊天链路.
//  5. 解析 Device 认证头,不把设备令牌写入日志或响应错误.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/internal/apperror"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/device"
	"github.com/swallow-sun/swallow-go/internal/trace"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// RegisterDevice POST /api/v1/devices/register.
// 本接口必须使用主人 Bearer Token,不能使用尚未注册的设备凭据.
// 成功响应中的 token 只出现一次;后续查询设备信息不会再次返回 token 或 token_hash.
func (d *Deps) RegisterDevice(ctx context.Context, c *app.RequestContext) {
	if !d.authorizeOwner(c) {
		return
	}
	ctx, _ = trace.Ensure(ctx)
	var req registerDeviceReq
	if err := c.BindAndValidate(&req); err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(
			device.ErrorCodeInvalidRegistration, "invalid device registration body", ""))
		return
	}
	if req.Capabilities == nil {
		req.Capabilities = map[string]any{}
	}
	capabilities, err := json.Marshal(req.Capabilities)
	if err != nil {
		writeErrorFromCtx(ctx, c, apperror.BadRequest(
			device.ErrorCodeInvalidRegistration, "invalid device capabilities", ""))
		return
	}
	result, err := d.device.RegisterDevice(ctx, req.Name, req.Platform, string(capabilities))
	if err != nil {
		var domainErr *device.DomainError
		if errors.As(err, &domainErr) {
			status := consts.StatusBadRequest
			if domainErr.Code == device.ErrorCodeNameConflict {
				status = consts.StatusConflict
			}
			writeErrorFromCtx(ctx, c, apperror.New(
				domainErr.Code, "device registration rejected", status, false, ""))
			return
		}
		logger.Error("device registration failed", zap.Error(err))
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	logger.Info("device registration completed",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", result.Device.ID),
		zap.Int64("user_id", result.Device.UserID),
	)
	c.JSON(consts.StatusCreated, registerDeviceResp{
		Device: newDevicePublicResp(result.Device),
		Token:  result.Token,
	})
}

// GetCurrentDevice GET /api/v1/devices/me.
// 请求头格式:Authorization: Device <device_id>.<token>.
func (d *Deps) GetCurrentDevice(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.Ensure(ctx)
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"device": newDevicePublicResp(registered)})
}

// CreateDeviceSession POST /api/v1/device/session.
// 设备身份决定 userID,请求体不能指定或覆盖用户归属.
func (d *Deps) CreateDeviceSession(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.Ensure(ctx)
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	result, err := d.session.CreateSessionForUser(ctx, registered.UserID, registered.ID)
	if err != nil {
		logger.Error("device session create failed",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.String("device_id", registered.ID),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return
	}
	c.JSON(consts.StatusCreated, createSessionResp{
		SessionID: result.SessionID,
		UserName:  result.UserName,
		UserID:    result.UserID,
	})
}

// DeviceChat POST /api/v1/device/chat.
// 设备只负责提供 session_id、client_message_id 和 message,其余聊天业务复用现有服务端链路.
func (d *Deps) DeviceChat(ctx context.Context, c *app.RequestContext) {
	ctx, _ = trace.Ensure(ctx)
	registered, ok := d.authenticateDevice(ctx, c)
	if !ok {
		return
	}
	d.chatForUser(ctx, c, registered.UserID, registered.ID, "device_chat", "POST /api/v1/device/chat")
}

// authenticateDevice 解析请求头并调用设备业务服务认证.
// 请求头格式固定为 Device <device_id>.<token>;任何凭据错误统一返回 401,
// 日志只记录 trace_id,认证成功后才允许记录公开的 device_id,避免泄露认证材料.
func (d *Deps) authenticateDevice(ctx context.Context, c *app.RequestContext) (data.Device, bool) {
	header := strings.TrimSpace(string(c.GetHeader("Authorization")))
	prefix := device.AuthorizationScheme + " "
	if !strings.HasPrefix(header, prefix) {
		writeErrorFromCtx(ctx, c, apperror.Unauthorized(""))
		return data.Device{}, false
	}
	credential := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	separator := strings.LastIndexByte(credential, '.')
	if separator <= 0 || separator == len(credential)-1 {
		writeErrorFromCtx(ctx, c, apperror.Unauthorized(""))
		return data.Device{}, false
	}
	registered, err := d.device.AuthenticateDevice(ctx, credential[:separator], credential[separator+1:])
	if err != nil {
		var domainErr *device.DomainError
		if errors.As(err, &domainErr) && domainErr.Code == device.ErrorCodeInvalidCredentials {
			logger.Warn("device authentication failed", zap.String("trace_id", trace.FromContext(ctx)))
			writeErrorFromCtx(ctx, c, apperror.Unauthorized(""))
			return data.Device{}, false
		}
		logger.Error("device authentication unavailable",
			zap.String("trace_id", trace.FromContext(ctx)),
			zap.Error(err),
		)
		writeErrorFromCtx(ctx, c, apperror.Internal(""))
		return data.Device{}, false
	}
	logger.Debug("device authentication succeeded",
		zap.String("trace_id", trace.FromContext(ctx)),
		zap.String("device_id", registered.ID),
		zap.Int64("user_id", registered.UserID),
	)
	return registered, true
}

// newDevicePublicResp 把设备业务对象转换成不会泄露令牌摘要的响应对象.
// capabilities_json 即使因历史脏数据无法解析也降级为空对象,不影响认证后的基础信息响应.
func newDevicePublicResp(registered data.Device) devicePublicResp {
	capabilities := map[string]any{}
	_ = json.Unmarshal([]byte(registered.CapabilitiesJSON), &capabilities)
	response := devicePublicResp{
		ID:           registered.ID,
		Name:         registered.Name,
		Platform:     registered.Platform,
		Status:       registered.Status,
		Capabilities: capabilities,
		CreatedAt:    registered.CreatedAt.Format(time.RFC3339Nano),
	}
	if registered.LastSeenAt != nil {
		response.LastSeenAt = registered.LastSeenAt.Format(time.RFC3339Nano)
	}
	return response
}
