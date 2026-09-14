package http_matcher

import (
	"github.com/balcony314/xds/internal/entity"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
)

// GenerateHttpPathMatcher 将路径匹配selector写入HTTP路由匹配（RouteMatch）的
// PathSpecifier，匹配请求URL的path部分（如 /api/v1/user）。
//
// 【业务→Envoy映射】match.Value为路径，操作符转为PathSpecifier oneof的三种形态：
//   - exact → RouteMatch_Path       ：path与Value完全相等才命中
//   - prefix → RouteMatch_Prefix    ：path以Value开头即命中
//   - regex → RouteMatch_SafeRegex  ：RE2正则匹配（"safe"指Envoy限定使用可保证
//     执行时延的正则引擎，区别于已废弃的Path字段正则形态）
//
// 仅匹配path，不含query string（?a=b部分由parameter.go的QueryParameterMatcher负责）。
// 未识别的操作符不设置specifier（保持nil），由调用方router.go兜底补默认前缀"/"匹配
func GenerateHttpPathMatcher(r *route.RouteMatch, match entity.RequestSelector) {
	switch match.Operator {
	case entity.ExactMatch:
		r.PathSpecifier = &route.RouteMatch_Path{Path: match.Value}
	case entity.RegexMatch:
		// 正则匹配：显式指定Google RE2引擎；RE2不支持回溯，
		// 配置带回溯语义的正则（如贪婪变体）时无法按预期命中
		r.PathSpecifier = &route.RouteMatch_SafeRegex{SafeRegex: &matcher.RegexMatcher{
			EngineType: &matcher.RegexMatcher_GoogleRe2{
				GoogleRe2: &matcher.RegexMatcher_GoogleRE2{},
			},
			Regex: match.Value,
		}}
	case entity.PrefixMatch:
		r.PathSpecifier = &route.RouteMatch_Prefix{Prefix: match.Value}
	}
}
