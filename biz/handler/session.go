package handler

// session.go 放 POST /api/session 接口的 handler。
// 客户端调这个接口来开启一段新对话，拿到一个 session_id 后再去调 /api/chat。

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// CreateSession POST /api/session
// 这是 POST /api/session 接口的 handler，客户端调它来开启一段新对话。
// 流程：用户名 → 登录/创建用户 → 创建新会话 → 返回 session_id，四步走。
func (d *Deps) CreateSession(ctx context.Context, c *app.RequestContext) {
	// 声明一个空的结构体变量，准备接收客户端传来的 JSON
	var req createSessionReq

	// 把请求体里的 JSON 解析到 req 里，同时做参数校验。
	// &req 是取地址，因为要往里面写数据。
	// 如果 JSON 格式不对或字段类型不对，返回错误
	if err := c.BindAndValidate(&req); err != nil {
		// 解析失败，返回 HTTP 400 + 错误信息，return 结束函数，不往下走
		c.JSON(consts.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// 客户端没传用户名（或者传了空字符串），给一个默认值 "owner"。
	// 这是你的私人助手，默认就是主人本人
	if req.UserName == "" {
		req.UserName = "owner"
	}

	// 调身份管理器：拿用户名去数据库查，有就更新活跃时间返回，没有就新建一条返回。
	// user 是用户结构体（里面有 ID、Name 等），err 是可能的数据库错误
	user, err := d.idm.LoginOrCreateUser(ctx, req.UserName)

	// 出错了，打日志 + 返回错误
	if err != nil {
		// zap.String("user", req.UserName) 记录是哪个用户出了问题
		// zap.Error(err) 记录具体报错内容
		// 这行只往日志文件里写，客户端看不到
		logger.Error("用户初始化失败", zap.String("user", req.UserName), zap.Error(err))
		// 给客户端返回 HTTP 500 + 笼统错误信息。
		// 注意这里没把 err 的具体内容扔给客户端——内部错误不外泄
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "user init failed"})
		return
	}

	// 给这个用户创建一条新会话。
	// 底层往数据库 sessions 表插一条记录，返回一个 UUID 字符串作为会话 ID。
	// user.ID 是上一步 LoginOrCreateUser 拿到的用户 ID
	sessionID, err := d.idm.NewSession(ctx, user.ID)

	// 又是出错打日志。
	// zap.Int64 因为 user.ID 是 int64 类型，和上面的 zap.String 对应，不同类型用不同的 zap 方法
	if err != nil {
		logger.Error("创建会话失败", zap.Int64("user_id", user.ID), zap.Error(err))
		// 同样的套路，500 + 笼统信息，return
		c.JSON(consts.StatusInternalServerError, map[string]string{"error": "session create failed"})
		return
	}

	// 成功了也打一条日志。
	// 这是 Info 级别（不是 Error），方便你排查问题时看到"哦，这个会话是哪个用户、哪个 session_id"
	logger.Info("会话已创建",
		zap.String("user", user.Name),
		zap.String("session_id", sessionID),
	)

	// 返回 HTTP 200 + JSON 响应体。
	// createSessionResp 结构体会被序列化成 JSON 发给客户端。
	// 客户端拿到这三个字段，其中最重要的是 SessionID，后续调 /api/chat 要带上它
	c.JSON(consts.StatusOK, createSessionResp{
		SessionID: sessionID,
		UserName:  user.Name,
		UserID:    user.ID,
	})
}
