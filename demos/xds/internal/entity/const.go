package entity

// 标签/请求头匹配类型常量，用于路由、可见性等规则的匹配条件表达
const (
	RegexMatch    = "regex_match" // 正则匹配
	ExactMatch    = "exact_match" // 精确匹配
	PrefixMatch   = "prefix"      // 前缀匹配
	SuffixMatch   = "suffix"      // 后缀匹配
	ContainsMatch = "contain"     // 包含匹配
	RangeMatch    = "range"       // 区间匹配
	ExistsMatch   = "exist"       // 键存在
	NotExistMatch = "not_exist"   // 键不存在
)
