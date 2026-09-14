package thrift_matcher

import (
	"github.com/balcony314/xds/internal/entity"
	thirft_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/thrift_proxy/v3"
)

// GeneraThriftMethodMatch 将方法名匹配selector转为thrift路由的MethodName匹配
// 即按thrift请求调用的方法名精确匹配（sc.Value为方法名，无操作符语义，
// 不支持前缀/正则等形态，与dubbo侧的StringMatcher不同）
// 注意：当前无调用方，属预留转换函数
func GeneraThriftMethodMatch(sc entity.RequestSelector) *thirft_proxy.RouteMatch_MethodName {
	return &thirft_proxy.RouteMatch_MethodName{
		MethodName: sc.Value,
	}
}
