package http_matcher

import (
	"github.com/balcony314/xds/internal/entity"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
)

// GenerateHttpParamsMatcher 将URL query参数匹配selector转为HTTP路由的
// QueryParameterMatcher（sc.Key为参数名，如 ?id=123 中的 id）。
//
// 【业务→Envoy映射】操作符转为QueryParameterMatchSpecifier oneof的几种形态：
//   - exact/prefix/suffix/contains → StringMatch（对应形态，忽略大小写）
//   - regex → StringMatch（SafeRegex，RE2，区分大小写）
//   - exists/not-exist → PresentMatch（只看参数是否存在，与值无关）
//
// 注意：与header不同，query参数不支持range区间匹配——Envoy的
// QueryParameterMatcher本身没有RangeMatch形态
func GenerateHttpParamsMatcher(sc entity.RequestSelector) *route.QueryParameterMatcher {
	var ParameterMatcher route.QueryParameterMatcher
	// Name为query参数名；多个参数条件之间是AND关系（调用方追加到RouteMatch.QueryParameters）
	ParameterMatcher.Name = sc.Key
	switch sc.Operator {
	case entity.RegexMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: false,
			MatchPattern: &matcher.StringMatcher_SafeRegex{
				SafeRegex: &matcher.RegexMatcher{
					EngineType: &matcher.RegexMatcher_GoogleRe2{
						GoogleRe2: &matcher.RegexMatcher_GoogleRE2{},
					},
					Regex: sc.Value,
				},
			},
		}}
	case entity.ExactMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase:   true,
			MatchPattern: &matcher.StringMatcher_Exact{Exact: sc.Value},
		}}
	case entity.PrefixMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Prefix{
				Prefix: sc.Value,
			},
		}}
	case entity.SuffixMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Suffix{
				Suffix: sc.Value,
			},
		}}
	case entity.ContainsMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Contains{
				Contains: sc.Value,
			},
		}}
	case entity.ExistsMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_PresentMatch{PresentMatch: true}
	case entity.NotExistMatch:
		ParameterMatcher.QueryParameterMatchSpecifier = &route.QueryParameterMatcher_PresentMatch{PresentMatch: false}
	}
	return &ParameterMatcher
}
