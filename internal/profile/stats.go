// stats.go 放标签统计的格式化和用户画像的注入逻辑.
//
// 做的事情:
//  1. FormatStatsForLLM: 把 tag_statistics 列表格式化成 LLM 分析 prompt 的输入文本.
//  2. InjectProfile: 把用户画像 JSON 格式化成 system prompt 的 "[用户画像参考]" 文本块.
//  3. 降级模式: 查询或解析失败时返回空字符串, 不影响正常对话.
//
// 方案 16.12.6 节: 统计数据格式化后发给 LLM 归纳画像; 画像注入 system prompt.
package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// FormatStatsForLLM 把 tag_statistics 列表格式化成 LLM 分析 prompt 的输入文本.
// 输出按维度分组, 每个维度下列出所有标签值及其命中次数, 方便 LLM 统计归纳.
//
// 输出示例:
//
//	[标签统计数据]
//	## emotion
//	frustrated: 12 次
//	neutral: 10 次
//	happy: 8 次
//	## urgency
//	normal: 25 次
//	high: 5 次
//	## cooperation
//	cooperative: 20 次
//	resistant: 10 次
//
// 参数:
//   - stats: tag_statistics 列表
//
// 返回值:
//   - string: 格式化后的统计文本
func FormatStatsForLLM(stats []data.TagStatistic) string {
	if len(stats) == 0 {
		return ""
	}

	// 按维度分组: dim -> []{value, count}
	// 用 map[string][]tagCount 结构, 遍历完再按维度排序输出
	type tagCount struct {
		value string
		count int
	}
	byDim := make(map[string][]tagCount)
	for _, s := range stats {
		byDim[s.TagDim] = append(byDim[s.TagDim], tagCount{value: s.TagValue, count: s.HitCount})
	}

	// 收集所有维度名并排序, 保证输出顺序稳定
	dims := make([]string, 0, len(byDim))
	for dim := range byDim {
		dims = append(dims, dim)
	}
	sort.Strings(dims)

	var b strings.Builder
	b.WriteString("[标签统计数据]\n")

	for _, dim := range dims {
		b.WriteString("## ")
		b.WriteString(dim)
		b.WriteString("\n")

		// 每个维度内按 count 降序排列, 频次高的排前面
		counts := byDim[dim]
		sort.Slice(counts, func(i, j int) bool {
			return counts[i].count > counts[j].count
		})

		for _, tc := range counts {
			b.WriteString(tc.value)
			b.WriteString(": ")
			b.WriteString(fmt.Sprintf("%d 次\n", tc.count))
		}
	}

	return b.String()
}

// InjectProfile 把用户画像格式化成 system prompt 的 "[用户画像参考]" 文本块.
// 如果没有画像或解析失败, 返回空字符串 (降级模式, 不影响正常对话).
//
// 参数:
//   - ctx: 上下文
//   - userID: 哪个用户的画像
//   - store: 画像存储, 用来查用户画像
//
// 返回值:
//   - string: 格式化后的画像参考文本块, 没有就返回空字符串
//
// 输出示例:
//
//	[用户画像参考]
//	沟通风格: direct, concise
//	兴趣领域: cpp, go, system programming
//	专业水平: intermediate programmer
//	性格特点: patient, detail-oriented
//	偏好话题: coding, architecture
//	概括: 用户是一名正在学习 C++ 和 Go 的程序员, 偏好简洁直接的沟通风格.
func InjectProfile(ctx context.Context, userID int64, store *Store) string {
	profile, err := store.GetUserProfile(ctx, userID)
	if err != nil {
		// 查不到画像 (还没有分析过) 或查询失败, 降级返回空字符串
		// 不打日志, 因为首次对话时没有画像是正常情况
		return ""
	}

	// 解析 profile_json 为 ProfileData 结构体
	var pd ProfileData
	if err := json.Unmarshal([]byte(profile.ProfileJSON), &pd); err != nil {
		// JSON 格式不对, 打 Error 日志, 降级返回空字符串
		logger.Error("inject profile: parse profile json failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return ""
	}

	// 如果画像内容全空, 不注入
	if pd.Summary == "" && pd.CommunicationStyle == "" && len(pd.Interests) == 0 &&
		len(pd.PersonalityTraits) == 0 && len(pd.PreferredTopics) == 0 && pd.Expertise == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("[用户画像参考]\n")

	if pd.CommunicationStyle != "" {
		b.WriteString("沟通风格: ")
		b.WriteString(pd.CommunicationStyle)
		b.WriteString("\n")
	}
	if len(pd.Interests) > 0 {
		b.WriteString("兴趣领域: ")
		b.WriteString(strings.Join(pd.Interests, ", "))
		b.WriteString("\n")
	}
	if pd.Expertise != "" {
		b.WriteString("专业水平: ")
		b.WriteString(pd.Expertise)
		b.WriteString("\n")
	}
	if len(pd.PersonalityTraits) > 0 {
		b.WriteString("性格特点: ")
		b.WriteString(strings.Join(pd.PersonalityTraits, ", "))
		b.WriteString("\n")
	}
	if len(pd.PreferredTopics) > 0 {
		b.WriteString("偏好话题: ")
		b.WriteString(strings.Join(pd.PreferredTopics, ", "))
		b.WriteString("\n")
	}
	if pd.Summary != "" {
		b.WriteString("概括: ")
		b.WriteString(pd.Summary)
		b.WriteString("\n")
	}

	return b.String()
}
