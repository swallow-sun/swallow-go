package llm

// CacheHitTokens 返回缓存命中的输入 Token 数，并兼容两种常见响应格式。
func (u Usage) CacheHitTokens() int {
	if u.PromptCacheHitTokens != 0 {
		return u.PromptCacheHitTokens
	}
	return u.PromptTokensDetails.CachedTokens
}

// CacheMissTokens 返回缓存未命中的输入 Token 数。
// 上游未直接返回时，根据输入总量减去缓存命中量计算。
func (u Usage) CacheMissTokens() int {
	if u.PromptCacheHitTokens != 0 || u.PromptCacheMissTokens != 0 {
		return u.PromptCacheMissTokens
	}

	miss := u.PromptTokens - u.CacheHitTokens()
	if miss < 0 {
		return 0
	}
	return miss
}
