// usage.go 放 Usage 类型的辅助方法，处理不同 LLM 供应商返回的 Token 用量格式差异。
//
// 做的事情：
//  1. CacheHitTokens：返回缓存命中的输入 Token 数，兼容两种常见响应格式（PromptCacheHitTokens 或 PromptTokensDetails.CachedTokens）。
//  2. CacheMissTokens：返回缓存未命中的输入 Token 数，上游未直接返回时根据输入总量减去命中量计算。
//
// 什么是缓存命中：
//   有些 API 厂商支持 prompt 缓存，就是你重复发同样的对话前缀（比如系统提示词），
//   第二次开始会命中缓存，省 token 费用。缓存命中的 token 计费更便宜甚至免费。
package llm

// CacheHitTokens 返回缓存命中的输入 Token 数，并兼容两种常见响应格式。
//
// 有些 API（如 DeepSeek）直接在顶层 prompt_cache_hit_tokens 字段返回，
// 有些 API（如 OpenAI 官方）放在 prompt_tokens_details.cached_tokens 子字段里。
// 这里先看顶层字段，没有再看子字段，两种都能处理。
func (u Usage) CacheHitTokens() int {
	// 先看顶层字段 prompt_cache_hit_tokens，非零说明这个 API 用的是顶层格式
	if u.PromptCacheHitTokens != 0 {
		return u.PromptCacheHitTokens
	}
	// 顶层没有就看子字段 prompt_tokens_details.cached_tokens（OpenAI 风格）
	return u.PromptTokensDetails.CachedTokens
}

// CacheMissTokens 返回缓存未命中的输入 Token 数。
// 上游未直接返回时，根据输入总量减去缓存命中量计算。
//
// 缓存未命中 = 输入总量 - 缓存命中量
// 比如 100 个输入 token，其中 80 个命中缓存，那未命中就是 20 个（这 20 个要按原价付费）。
func (u Usage) CacheMissTokens() int {
	// 如果 API 直接返回了 cache_hit 或 cache_miss 字段，说明它支持缓存统计，
	// 直接返回 prompt_cache_miss_tokens 就行
	if u.PromptCacheHitTokens != 0 || u.PromptCacheMissTokens != 0 {
		return u.PromptCacheMissTokens
	}

	// API 没直接返回 cache_miss，自己算：输入总量 - 缓存命中量 = 未命中量
	miss := u.PromptTokens - u.CacheHitTokens()
	// 理论上 miss 不会是负数，但防御性处理一下，算出来是负数就返回 0
	if miss < 0 {
		return 0
	}
	return miss
}
