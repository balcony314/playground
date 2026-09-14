package dubbo_matcher

import (
	"strconv"
	"strings"

	"github.com/balcony314/xds/internal/entity"
	dubbo_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/dubbo_proxy/v3"
	v32 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

// GenerateParamMatcher 将dubbo方法参数匹配selector填充到MethodMatch.ParamsMatch
// （按参数位置匹配请求参数值，HTTP路由没有对应能力）。
// sc.Key为参数下标（从0开始，如"0"表示第一个参数），业务配置 → proto映射：
//   - exact → ParameterMatchSpecifier_ExactMatch（参数值精确相等）
//   - range → ParameterMatchSpecifier_RangeMatch（数值区间，值格式"min-max"，
//     Envoy区间为半开[start,end)）
//
// Key非数字或其他操作符时不生成条件（静默忽略）；同一下标多次配置时后者覆盖前者
func GenerateParamMatcher(mm *dubbo_proxy.MethodMatch, sc entity.RequestSelector) {
	// ParamsMatch为"参数下标→匹配条件"的map，首次调用时初始化
	if mm.ParamsMatch == nil {
		mm.ParamsMatch = make(map[uint32]*dubbo_proxy.MethodMatch_ParameterMatchSpecifier)
	}
	// Key必须是十进制数字（参数位置），否则放弃本条匹配
	index, err := strconv.ParseUint(sc.Key, 10, 32)
	if err != nil {
		return
	}

	switch sc.Operator {
	case entity.RangeMatch:
		// 解析"min-max"区间；格式非法时放弃本条匹配（静默忽略）
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
		mm.ParamsMatch[uint32(index)] = &dubbo_proxy.MethodMatch_ParameterMatchSpecifier{
			ParameterMatchSpecifier: &dubbo_proxy.MethodMatch_ParameterMatchSpecifier_RangeMatch{RangeMatch: &v32.Int64Range{
				Start: int64(min),
				End:   int64(max),
			}},
		}
	case entity.ExactMatch:
		mm.ParamsMatch[uint32(index)] = &dubbo_proxy.MethodMatch_ParameterMatchSpecifier{
			ParameterMatchSpecifier: &dubbo_proxy.MethodMatch_ParameterMatchSpecifier_ExactMatch{ExactMatch: sc.Value},
		}
	}
}
