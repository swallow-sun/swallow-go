// store.go 放 memory.Store:对话历史的存取.
//
// 做的事情:
//  1. SaveMessage:把一条用户或助手消息写入 dialogues 表,记录 trace ID 和 token 用量,同时打埋点.
//  2. LoadHistory:从数据库加载最近 N 条对话,转成 LLM 用的 ChatMessage 切片(跳过 system 角色).
//  3. SearchLongTerm:检索用户已经确认的 active 长期记忆.
//  4. CreateCandidates:对话成功后按确定性规则生成 pending 记忆候选.
//
// Phase 2:对话存 SQLite,每轮对话后写入,启动时从 DB 加载历史.
// 取代 Phase 1 的 Agent 内存 slice——数据存进数据库后重启不丢.
package memory

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/metrics"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/telemetry"
	"github.com/swallow-sun/swallow-go/internal/trace"
)

// New 创建一个 memory Store.
// safetyFilterEnabled 不传时默认开启;传入 false 时明确关闭敏感信息过滤.
func New(repo data.Repository, safetyFilterEnabled ...bool) *Store {
	// 构造一个 Store,把 repo 存进去,后面所有方法都用它操作数据库
	return &Store{repo: repo, safetyFilterEnabled: resolveSafetyFilterEnabled(safetyFilterEnabled)}
}

// SaveMessage 保存一条用户或助手消息,并记录 dialogue 埋点.
// ctx 里有 trace ID,sessionID 是会话 ID,userID 是用户 ID,
// role 是消息角色(user/assistant),content 是消息内容,
// usage 是 LLM 返回的 token 用量.
func (s *Store) SaveMessage(
	ctx context.Context,
	sessionID string,
	userID int64,
	role llm.Role,
	content string,
	usage llm.Usage,
) error {
	// 从 context 里拿到本轮对话的 trace ID.
	// trace.FromContext 是我们自己在 internal/trace 包写的函数,
	// 从 context 里取出 trace ID 字符串(如果没有就返回空串).
	traceID := trace.FromContext(ctx)

	// 把 LLM 返回的 usage 转成数据层的 data.TokenUsage 结构体.
	// LLM 那边用的是 llm.Usage,数据库这边用的是 data.TokenUsage,两个结构体字段不完全一样,
	// 需要手动映射一下.CacheHitTokens()/CacheMissTokens() 是方法,算出缓存命中和未命中的 token 数.
	tokenUsage := data.TokenUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheHitTokens:   usage.CacheHitTokens(),
		CacheMissTokens:  usage.CacheMissTokens(),
		ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      usage.TotalTokens,
	}

	// 把消息存进 dialogues 表.
	// string(role) 把角色枚举转成字符串,比如 llm.RoleUser → "user"
	// InsertDialogue 返回数据库自增 ID,这里用 _ 丢掉不需要
	_, err := s.repo.InsertDialogue(
		ctx,
		sessionID,
		userID,
		string(role),
		content,
		tokenUsage,
		traceID,
	)

	if err != nil {
		// 消息保存失败,记录一个失败埋点.
		// telemetry.Emit 是我们在 internal/telemetry 包写的函数,往 channel 里扔一条事件,
		// 后面异步消费的人会把它记进数据库或发到看板.
		// utf8.RuneCountInString(content) 是 Go 标准库 unicode/utf8 里的函数,
		// 数 content 里有多少个 Unicode 字符(rune),而不是字节数.
		// 比如中文 "你好" 是 2 个 rune,但 6 个字节(UTF-8 编码每个汉字 3 字节).
		// 用 rune 数更准确地反映消息长度.
		telemetry.Emit(ctx, telemetry.EventDialogue, map[string]any{
			"session_id":          sessionID,
			"user_id":             userID,
			"role":                string(role),
			"content_chars":       utf8.RuneCountInString(content),
			telemetry.FieldStatus: telemetry.StatusError,
			"error":               err.Error(),
		})

		// 把原始错误包一层往上抛
		return fmt.Errorf("save message: %w", err)
	}

	// 保存成功了,打一个成功埋点.
	// dialogue 事件只记录"保存了什么消息"——角色和内容长度.
	// token 明细由 dialogues 表和 llm.stream.complete 事件负责,这里不重复记.
	// content_bytes 也带上,方便对比字符数和字节数的差异
	telemetry.Emit(ctx, telemetry.EventDialogue, map[string]any{
		"session_id":          sessionID,
		"user_id":             userID,
		"role":                string(role),
		"content_chars":       utf8.RuneCountInString(content),
		"content_bytes":       len(content),
		telemetry.FieldStatus: telemetry.StatusOK,
	})

	return nil
}

// LoadHistory 从数据库加载最近 N 条对话,
// 转成 LLM 用的 ChatMessage 切片.
// sessionID 是会话 ID,limit 是最多加载几条.
// 返回的切片里不包含 system 角色的消息(system prompt 由 Agent 单独加).
func (s *Store) LoadHistory(
	ctx context.Context,
	sessionID string,
	limit int,
) ([]llm.ChatMessage, error) {
	// 记录查询开始时间,后面算数据库查询耗时
	start := time.Now()

	// 调 repo 去数据库里按 session ID 拿最近 limit 条对话记录
	// 返回的是 data.Dialogue 结构体切片
	dialogues, err := s.repo.GetRecentDialogues(
		ctx,
		sessionID,
		limit,
	)

	// time.Since(start) 算出从 start 到现在过了多久,就是数据库查询耗时
	elapsed := time.Since(start)

	if err != nil {
		// 查询失败也要记埋点,方便看板统计错误率
		// FieldDurationMS 把耗时以毫秒为单位记进去
		telemetry.Emit(ctx, telemetry.EventMemoryQuery, map[string]any{
			"session_id":              sessionID,
			"limit":                   limit,
			telemetry.FieldStatus:     telemetry.StatusError,
			telemetry.FieldDurationMS: elapsed.Milliseconds(),
			"error":                   err.Error(),
		})

		// Prometheus 指标:记忆查询失败,计数器 +1,耗时直方图记录
		metrics.RecordMemoryQuery(metrics.StatusFailed, float64(elapsed.Milliseconds()))

		// 把原始错误包一层往上抛
		return nil, fmt.Errorf("load history: %w", err)
	}

	// 把数据库查出来的 data.Dialogue 转成 LLM 用的 llm.ChatMessage.
	// make([]llm.ChatMessage, 0, len(dialogues)) 预分配一个切片,
	//   长度 0(现在还没有元素),容量等于查出来的对话条数,
	//   后面 append 时不用频繁扩容,提前分好空间.
	// system prompt 由 Agent 单独加,这里不重复返回.
	messages := make(
		[]llm.ChatMessage,
		0,
		len(dialogues),
	)

	// 遍历查出来的每条对话
	for _, dialogue := range dialogues {
		// 如果是 system 角色的消息就跳过——system prompt 由 Agent 统一管理,不从历史里拿
		if dialogue.Role == string(llm.RoleSystem) {
			continue
		}

		// 把角色和内容塞进 ChatMessage,追加到 messages 切片
		// llm.Role(dialogue.Role) 把字符串转回 Role 枚举类型
		messages = append(messages, llm.ChatMessage{
			Role:    llm.Role(dialogue.Role),
			Content: dialogue.Content,
		})
	}

	// 查询成功了,记一个埋点:返回了几条,花了多久
	telemetry.Emit(ctx, telemetry.EventMemoryQuery, map[string]any{
		"session_id":              sessionID,
		"limit":                   limit,
		"rows_returned":           len(messages),
		telemetry.FieldStatus:     telemetry.StatusOK,
		telemetry.FieldDurationMS: elapsed.Milliseconds(),
	})

	// Prometheus 指标:记忆查询成功,计数器 +1,耗时直方图记录
	metrics.RecordMemoryQuery(metrics.StatusOK, float64(elapsed.Milliseconds()))

	// 返回转换好的消息切片
	return messages, nil
}

// SearchLongTerm 检索用户已经确认的正式长期记忆。
// 使用用户原问题做词元检索；无匹配时返回空集合，不注入无关记忆。
func (s *Store) SearchLongTerm(ctx context.Context, userID int64, query string, limit int) (SearchResult, error) {
	retriever := NewRetriever(s.repo)
	result, err := retriever.Search(ctx, userID, query, limit)
	if err != nil {
		return SearchResult{}, err
	}
	return result, nil
}

// CreateCandidates 在完整一轮对话成功保存后生成 pending 长期记忆候选。
// 候选必须由用户通过确认接口处理，本方法不会直接写入正式 memories 表。
func (s *Store) CreateCandidates(ctx context.Context, userID int64, sessionID, userMessage string) ([]data.MemoryCandidate, error) {
	service := NewCandidateService(s.repo, NewPolicy(), s.safetyFilterEnabled)
	return service.CreateCandidates(ctx, userID, sessionID, userMessage)
}
