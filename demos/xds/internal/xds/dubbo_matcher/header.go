package dubbo_matcher

import (
	"strconv"
	"strings"

	"github.com/balcony314/xds/internal/entity"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	typepb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

// GenerateHeaderMatcher 将header匹配selector（sc.Key为header名）转为dubbo路由的
// HeaderMatcher。dubbo语义下header指attachment（RPC隐式传参头，随请求传递的
// 键值对）；proto直接复用HTTP的route.HeaderMatcher类型。
// 支持操作符：exact/prefix/suffix/contains（忽略大小写）、regex（RE2，区分大小写）、
// range（值格式"min-max"，Envoy区间为半开[start,end)）、exists/not-exist（按是否存在匹配）
//
// 【已知缺陷】入参hm是切片的值拷贝（底层数组指针+len），末尾的hm=append只改本地
// 变量，不会回写调用方切片——createRouter传入的Match.Headers保持为空，
// 即dubbo路由的header匹配条件当前实际不生效，仅保留转换逻辑待修复
func GenerateHeaderMatcher(hm []*route.HeaderMatcher, sc entity.RequestSelector) {
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
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_RangeMatch{RangeMatch: &typepb.Int64Range{
			Start: int64(min),
			End:   int64(max),
		}}
	case entity.ExistsMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_PresentMatch{PresentMatch: true}
	case entity.NotExistMatch:
		headerMatcher.HeaderMatchSpecifier = &route.HeaderMatcher_PresentMatch{PresentMatch: false}
	}
	// 只追加到本地副本hm，调用方切片不受影响（见函数头"已知缺陷"）
	hm = append(hm, &headerMatcher)
	return
}
