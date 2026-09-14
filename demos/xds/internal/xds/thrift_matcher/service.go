package thrift_matcher

import (
	"github.com/balcony314/xds/internal/entity"
	thrift_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/thrift_proxy/v3"
)

// GeneraThriftServiceMatch 将服务名匹配selector转为thrift路由的ServiceName匹配
// 即按thrift请求的服务名精确匹配（sc.Value为服务名）。thrift RPC以
// "服务名:方法名"两级定位调用目标，ServiceName对应第一级
// 注意：当前无调用方，属预留转换函数
func GeneraThriftServiceMatch(sc entity.RequestSelector) *thrift_proxy.RouteMatch_ServiceName {
	return &thrift_proxy.RouteMatch_ServiceName{
		ServiceName: sc.Value,
	}

}
