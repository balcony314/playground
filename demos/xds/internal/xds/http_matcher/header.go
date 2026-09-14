package http_matcher

import (
	"strconv"
	"strings"

	"github.com/balcony314/xds/internal/entity"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	typepb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

// GenerateHttpHeaderMatcher 将header匹配selector转为HTTP路由的HeaderMatcher
// （sc.Key为header名，sc.Value为期望值）。
//
// 【业务→Envoy映射】操作符转为HeaderMatchSpecifier oneof的几种形态：
//   - exact/prefix/suffix/contains → HeaderMatcher_StringMatch（StringMatcher
//     对应形态，忽略大小写）
//   - regex → HeaderMatcher_StringMatch（SafeRegex，RE2，区分大小写）
//   - range → HeaderMatcher_RangeMatch（按数值区间匹配header值，Value格式
//     "min-max"，Envoy区间为半开[start,end)）
//   - exists/not-exist → HeaderMatcher_PresentMatch（只看header是否存在，与值无关）
//
// 多条header条件之间是AND关系（由调用方router.go追加到RouteMatch.Headers）
func GenerateHttpHeaderMatcher(sc entity.RequestSelector) *route.HeaderMatcher {
	var headerMatcher route.HeaderMatcher
	headerMatcher.Name = sc.Key
	switch sc.Operator {
	case entity.ExactMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Exact{
				Exact: sc.Value,
			},
		}}
	case entity.RegexMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
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
	case entity.PrefixMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Prefix{
				Prefix: sc.Value,
			},
		}}
	case entity.SuffixMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Suffix{
				Suffix: sc.Value,
			},
		}}
	case entity.ContainsMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_StringMatch{StringMatch: &matcher.StringMatcher{
			IgnoreCase: true,
			MatchPattern: &matcher.StringMatcher_Contains{
				Contains: sc.Value,
			},
		}}
	case entity.RangeMatch:
		// 解析"min-max"区间；格式非法时放弃本条匹配（静默忽略，不设置specifier）
		item := strings.Split(sc.Value, "-")
		if len(item) != 2 {
			break
		}
		min, err := strconv.Atoi(item[0])
		if err != nil {
			break
		}
		max, err := strconv.Atoi(item[1])
		if err != nil {
			break
		}
		// Envoy区间语义为半开区间[start,end)：值>=min且<max才命中
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_RangeMatch{RangeMatch: &typepb.Int64Range{
			Start: int64(min),
			End:   int64(max),
		}}
	case entity.ExistsMatch:
		// header存在即命中
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_PresentMatch{PresentMatch: true}
	case entity.NotExistMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_PresentMatch{PresentMatch: false}
	}
	return &headerMatcher
}
