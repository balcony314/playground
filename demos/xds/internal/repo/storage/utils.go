package storage

import (
	"encoding/json"
	"fmt"

	"github.com/nacos-group/nacos-sdk-go/v2/model"

	"github.com/balcony314/xds/internal/entity"
)

// boolPtr 返回布尔值指针（替代 golang/protobuf 的 proto.Bool；
// entity 包的泛型 ptr 为私有，此处本地实现）
func boolPtr(b bool) *bool { return &b }

// copyMap 拷贝字符串映射，nil 安全（原 pkg/utils.CopyStringMap 未随迁移，此处就地实现）
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// ToInstance 将Nacos实例模型转换为内部Instance实体（原名ToMatrixInstance，
// "Matrix"为业务旧词，开源化后更名）：
// Nacos原生只有metadata一个扩展字段，region/zone/source等在注册时被写入标签，
// 这里反向从标签还原为结构化字段，并去掉nacos sdk对实例做的过滤修饰
func ToInstance(instances []model.Instance) entity.InstanceList {
	var result []entity.Instance
	for i := range instances {
		obj := instances[i]
		item := entity.Instance{
			IP:      obj.Ip,
			Port:    int(obj.Port),
			Region:  obj.Metadata["region"],
			Zone:    obj.Metadata["zone"],
			Healthy: boolPtr(obj.Healthy),
			Isolate: boolPtr(!obj.Enable),
			Source:  obj.Metadata["source"],
			Labels:  copyMap(obj.Metadata),
		}
		item.RemoveDefaultLabels()
		result = append(result, item)
	}
	return result
}

// PageItemsToServices 将配置中心分页查询结果反序列化为服务元数据列表
func PageItemsToServices(pageItems []model.ConfigItem) ([]entity.MicroService, error) {
	result := make([]entity.MicroService, 0)
	for _, item := range pageItems {
		var svc entity.MicroService
		if err := json.Unmarshal([]byte(item.Content), &svc); err != nil {
			return nil, fmt.Errorf("%w: %s", err, item.Content)
		}
		result = append(result, svc)
	}
	return result, nil
}
