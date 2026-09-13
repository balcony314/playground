// Package api 实现 msched HTTP API（OpenAPI 契约见 api/openapi/msched.yaml）。
//
// 手写 gin handler 对齐契约（不使用代码生成）。三接口：创建任务 / list 任务 /
// 查看 group 进度。handler 依赖 scheduler.TaskStore 接口，与具体存储实现解耦。
package api

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/balcony314/msched/internal/model"
)

// --- 请求 DTO ---

// createTaskRequest 创建任务请求体（对应 OpenAPI CreateTaskRequest）。
type createTaskRequest struct {
	Args            string            `json:"args" binding:"required"`
	WorkerSelector  map[string]string `json:"worker_selector"`
	MaxExecDuration string            `json:"max_exec_duration"`
	Priority        int64             `json:"priority"`
}

// --- 响应 DTO ---

// taskDTO 任务投影（对应 OpenAPI Task）。不含 args/result 大字段。
type taskDTO struct {
	UnitID          string            `json:"unit_id"`
	GroupUID        string            `json:"group_uid"`
	State           string            `json:"state"`
	Priority        int64             `json:"priority"`
	WorkerSelector  map[string]string `json:"worker_selector"`
	SelectorHash    string            `json:"selector_hash"`
	MaxExecDuration string            `json:"max_exec_duration"`
	FailCount       int               `json:"fail_count"`
	DispatchCount   int               `json:"dispatch_count"`
	NextRetryTime   *time.Time        `json:"next_retry_time,omitempty"`
}

// taskListDTO 任务列表响应（对应 OpenAPI TaskList）。
type taskListDTO struct {
	Items []taskDTO `json:"items"`
}

// groupStatRowDTO group_stats 单行（对应 OpenAPI GroupStatRow）。
type groupStatRowDTO struct {
	SelectorHash string   `json:"selector_hash"`
	Selectors    []string `json:"selectors"`
	State        string   `json:"state"`
	Count        int64    `json:"count"`
}

// groupProgressDTO group 进度响应（对应 OpenAPI GroupProgress）。
type groupProgressDTO struct {
	GroupUID string            `json:"group_uid"`
	Stats    []groupStatRowDTO `json:"stats"`
}

// errorDTO 统一错误响应（对应 OpenAPI Error）。
type errorDTO struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// deleteTasksRequest 删除任务请求体。两种模式互斥：
//   - unit_ids 非空：删指定 unitID（仅删属于该 group 的）
//   - all=true：删整个 group 下全部任务
type deleteTasksRequest struct {
	UnitIDs []string `json:"unit_ids"`
	All     bool     `json:"all"`
}

// deleteResponse 删除结果。
type deleteResponse struct {
	Deleted int64 `json:"deleted"`
}

// --- 转换 ---

// reqToModel 把请求体转 model.Task。group_uid 来自路径参数。Args base64 解码；
// MaxExecDuration 字符串解析为 time.Duration。校验失败返回 error（handler 包 400）。
func (r createTaskRequest) toModel(groupUID string) (model.Task, error) {
	args, err := base64.StdEncoding.DecodeString(r.Args)
	if err != nil {
		return model.Task{}, fmt.Errorf("args base64 decode: %w", err)
	}
	t := model.Task{
		GroupUID:       groupUID,
		Args:           args,
		WorkerSelector: model.WorkerSelector(r.WorkerSelector),
		Priority:       r.Priority,
	}
	if r.MaxExecDuration != "" {
		d, err := time.ParseDuration(r.MaxExecDuration)
		if err != nil {
			return model.Task{}, fmt.Errorf("max_exec_duration parse: %w", err)
		}
		t.MaxExecDuration = d
	}
	return t, nil
}

// taskToDTO 把 model.Task 转 taskDTO。MaxExecDuration 转字符串；NextRetryTime 保留指针。
func taskToDTO(t model.Task) taskDTO {
	dto := taskDTO{
		UnitID:          t.UnitID,
		GroupUID:        t.GroupUID,
		State:           string(t.State),
		Priority:        t.Priority,
		WorkerSelector:  map[string]string(t.WorkerSelector),
		SelectorHash:    t.SelectorHash,
		MaxExecDuration: t.MaxExecDuration.String(),
		FailCount:       t.FailCount,
		DispatchCount:   t.DispatchCount,
		NextRetryTime:   t.NextRetryTime,
	}
	if dto.WorkerSelector == nil {
		dto.WorkerSelector = map[string]string{}
	}
	return dto
}

// groupStatsToDTO 把 model.GroupStats 转 groupStatRowDTO。
func groupStatsToDTO(g model.GroupStats) groupStatRowDTO {
	selectors := g.Selectors
	if selectors == nil {
		selectors = []string{}
	}
	return groupStatRowDTO{
		SelectorHash: g.SelectorHash,
		Selectors:    selectors,
		State:        string(g.State),
		Count:        g.Count,
	}
}
