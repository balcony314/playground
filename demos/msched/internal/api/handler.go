package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/balcony314/msched/internal/model"
	"github.com/balcony314/msched/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// maxListLimit list 任务单次返回上限（OpenAPI 约束）。
const maxListLimit = 500

// handler 持有 TaskStore 引用，处理三接口请求。
type handler struct {
	store scheduler.TaskStore
}

// newHandler 构造 handler。
func newHandler(store scheduler.TaskStore) *handler {
	return &handler{store: store}
}

// register 把路由注册到 gin RouterGroup。
func (h *handler) register(rg *gin.RouterGroup) {
	rg.POST("/groups/:group_uid/tasks", h.createTask)
	rg.GET("/groups/:group_uid/tasks", h.listTasks)
	rg.DELETE("/groups/:group_uid/tasks", h.deleteTasks)
	rg.GET("/groups/:group_uid/progress", h.groupProgress)
}

// createTask POST /api/v1/groups/:group_uid/tasks
//
// 幂等：相同 group+args 产生相同 unit_id，重复创建返回已存在任务（store.Create 内处理，
// DESIGN §3）。校验失败 400，store 错误 500。
func (h *handler) createTask(c *gin.Context) {
	groupUID := c.Param("group_uid")
	if groupUID == "" {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "group_uid required"})
		return
	}
	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "invalid body: " + err.Error()})
		return
	}
	in, err := req.toModel(groupUID)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorDTO{Error: err.Error()})
		return
	}
	created, err := h.store.Create(c.Request.Context(), in)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorDTO{Error: "create task: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, taskToDTO(created))
}

// listTasks GET /api/v1/groups/:group_uid/tasks
//
// query: state（可重复）、limit（default 50, max 500）、offset（default 0）。
// state 为空表示不过滤。store 错误 500。
func (h *handler) listTasks(c *gin.Context) {
	groupUID := c.Param("group_uid")
	if groupUID == "" {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "group_uid required"})
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, errorDTO{Error: "invalid limit"})
			return
		}
		if n > maxListLimit {
			n = maxListLimit
		}
		limit = n
	}
	offset := 0
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, errorDTO{Error: "invalid offset"})
			return
		}
		offset = n
	}
	var states []model.State
	for _, s := range c.QueryArray("state") {
		st := model.State(strings.ToUpper(s))
		if !validState(st) {
			c.JSON(http.StatusBadRequest, errorDTO{Error: "invalid state: " + s})
			return
		}
		states = append(states, st)
	}
	tasks, err := h.store.ListByGroup(c.Request.Context(), groupUID, states, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorDTO{Error: "list tasks: " + err.Error()})
		return
	}
	items := make([]taskDTO, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, taskToDTO(t))
	}
	c.JSON(http.StatusOK, taskListDTO{Items: items})
}

// groupProgress GET /api/v1/groups/:group_uid/progress
//
// 返回 group 下各 (selector_hash, state) 聚合计数（DESIGN §3 group_stats）。
func (h *handler) groupProgress(c *gin.Context) {
	groupUID := c.Param("group_uid")
	if groupUID == "" {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "group_uid required"})
		return
	}
	stats, err := h.store.ListGroupStats(c.Request.Context(), groupUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorDTO{Error: "list group stats: " + err.Error()})
		return
	}
	rows := make([]groupStatRowDTO, 0, len(stats))
	for _, g := range stats {
		rows = append(rows, groupStatsToDTO(g))
	}
	c.JSON(http.StatusOK, groupProgressDTO{GroupUID: groupUID, Stats: rows})
}

// deleteTasks DELETE /api/v1/groups/:group_uid/tasks
//
// 两种模式互斥：unit_ids 非空删指定（仅删属于该 group 的），all=true 删整个 group。
// 二者都给则 400。store 错误 500。
func (h *handler) deleteTasks(c *gin.Context) {
	groupUID := c.Param("group_uid")
	if groupUID == "" {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "group_uid required"})
		return
	}
	var req deleteTasksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "invalid body: " + err.Error()})
		return
	}
	if req.All && len(req.UnitIDs) > 0 {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "unit_ids 与 all 互斥"})
		return
	}
	if !req.All && len(req.UnitIDs) == 0 {
		c.JSON(http.StatusBadRequest, errorDTO{Error: "需指定 unit_ids 或 all=true"})
		return
	}
	var unitIDs []string
	if !req.All {
		unitIDs = req.UnitIDs
	}
	deleted, err := h.store.Delete(c.Request.Context(), groupUID, unitIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorDTO{Error: "delete tasks: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, deleteResponse{Deleted: deleted})
}

// validState 校验状态枚举值（DESIGN §8.1）。
func validState(s model.State) bool {
	switch s {
	case model.StatePending, model.StateBackoff, model.StateScheduled,
		model.StateRunning, model.StateCompleted, model.StateDiscarded:
		return true
	}
	return false
}
