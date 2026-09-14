package dubbo_matcher

import (
	"github.com/balcony314/xds/internal/entity"
	dubbo_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/dubbo_proxy/v3"
	v31 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
)

// GenerateMethodMatcher 将方法名匹配selector填充到dubbo的MethodMatch.Name，
// 即按dubbo请求调用的方法名匹配（service:method中的method部分）。
// 业务操作符 → StringMatcher形态：exact/prefix/suffix/contains/regex，
// 统一忽略大小写（含regex，与HTTP侧regex区分大小写不同）。
// mm.Name为整体赋值，同一规则配多个method-name selector时后者覆盖前者
func GenerateMethodMatcher(mm *dubbo_proxy.MethodMatch, sc entity.RequestSelector) {
	var methodMatcher v31.StringMatcher
	// 方法名匹配统一忽略大小写（对regex同样生效）
	methodMatcher.IgnoreCase = true

	switch sc.Operator {
	case entity.ExactMatch:
		methodMatcher.MatchPattern = &v31.StringMatcher_Exact{Exact: sc.Value}
	case entity.PrefixMatch:
		methodMatcher.MatchPattern = &v31.StringMatcher_Prefix{Prefix: sc.Value}
	case entity.SuffixMatch:
		methodMatcher.MatchPattern = &v31.StringMatcher_Suffix{Suffix: sc.Value}
	case entity.RegexMatch:
		methodMatcher.MatchPattern = &v31.StringMatcher_SafeRegex{SafeRegex: &v31.RegexMatcher{
			EngineType: &v31.RegexMatcher_GoogleRe2{
				GoogleRe2: &v31.RegexMatcher_GoogleRE2{},
			},
			Regex: sc.Value,
		}}
	case entity.ContainsMatch:
		methodMatcher.MatchPattern = &v31.StringMatcher_Contains{Contains: sc.Value}
	}
	mm.Name = &methodMatcher
}
