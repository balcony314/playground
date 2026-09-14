package thrift_matcher

import (
	"strconv"
	"strings"

	"github.com/balcony314/xds/internal/entity"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	typepb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

// GenerateThriftHeaderMatcher 将header匹配selector转为thrift路由的HeaderMatcher
// （sc.Key为header名），匹配thrift请求的transport header；proto复用HTTP的
// route.HeaderMatcher类型（thrift_proxy的RouteMatch.Headers即此类型）。
// 支持操作符：exact/prefix/suffix/contains（忽略大小写）、regex（RE2，区分大小写）、
// range（值格式"min-max"，Envoy区间为半开[start,end)）、exists/not-exist（按是否存在匹配）
// 注意：当前无调用方（createRouter未消费MatchConfig），属预留转换函数
func GenerateThriftHeaderMatcher(sc entity.RequestSelector) *route.HeaderMatcher {
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
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_PresentMatch{PresentMatch: true}
	case entity.NotExistMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_PresentMatch{PresentMatch: false}
	}
	return &headerMatcher
}
