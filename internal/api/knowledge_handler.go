package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/search"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

type KnowledgeHandler struct {
	knowledgeSvc *knowledge.Service
	indexer      *search.Indexer
	taskSvc      *task.Service
}

func NewKnowledgeHandler(knowledgeSvc *knowledge.Service, indexer *search.Indexer, taskSvc ...*task.Service) *KnowledgeHandler {
	h := &KnowledgeHandler{
		knowledgeSvc: knowledgeSvc,
		indexer:      indexer,
	}
	if len(taskSvc) > 0 {
		h.taskSvc = taskSvc[0]
	}
	return h
}

// SearchKnowledge 全文与多维组合检索知识库
func (h *KnowledgeHandler) SearchKnowledge(c *gin.Context) {
	keyword := c.Query("keyword")
	module := c.Query("module")
	entryType := c.Query("entry_type")
	product := c.Query("product")
	version := c.Query("version")

	var sevPtr *int
	if sevStr := c.Query("severity"); sevStr != "" {
		if s, err := strconv.Atoi(sevStr); err == nil {
			sevPtr = &s
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	} else if pageSize > 200 {
		pageSize = 200
	}

	filter := search.SearchFilter{
		Keyword:   keyword,
		Module:    module,
		Severity:  sevPtr,
		EntryType: entryType,
		Product:   product,
		Version:   version,
		Page:      page,
		PageSize:  pageSize,
		Sort:      c.Query("sort"),
	}

	res, err := h.indexer.Search(filter)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Search failed: "+err.Error())
		return
	}

	// 丰富知识详情
	type EnrichedHit struct {
		search.SearchHit
		Module      string `json:"module"`
		Brief       string `json:"brief"`
		Severity    int    `json:"severity"`
		EntryType   string `json:"entry_type"`
		Message     string `json:"message"`
		Description string `json:"description"`
		Cause       string `json:"cause"`
		Action      string `json:"action"`
		Source      string `json:"source"`
		Reference   string `json:"reference_url,omitempty"`
	}

	enrichedHits := make([]EnrichedHit, 0)
	if len(res.Hits) > 0 {
		uniqueIDs := make([]uint, 0, len(res.Hits))
		idSet := make(map[uint]struct{}, len(res.Hits))
		for _, hit := range res.Hits {
			if hit.KnowledgeID > 0 {
				if _, exists := idSet[hit.KnowledgeID]; !exists {
					idSet[hit.KnowledgeID] = struct{}{}
					uniqueIDs = append(uniqueIDs, hit.KnowledgeID)
				}
			}
		}

		knowledgeMap, _ := h.knowledgeSvc.GetKnowledgeMapByIDs(uniqueIDs)
		for _, hit := range res.Hits {
			if knowledgeMap != nil {
				if k, ok := knowledgeMap[hit.KnowledgeID]; ok && k != nil {
					eh := EnrichedHit{
						SearchHit:   hit,
						Module:      k.Module,
						Brief:       k.Brief,
						Severity:    k.Severity,
						EntryType:   string(k.EntryType),
						Message:     k.Message,
						Description: k.Description,
						Cause:       k.Cause,
						Action:      k.Action,
						Source:      k.Source,
						Reference:   k.ReferenceURL,
					}
					enrichedHits = append(enrichedHits, eh)
				}
			}
		}
	}

	SuccessResponse(c, gin.H{
		"total":     res.Total,
		"page":      page,
		"page_size": pageSize,
		"hits":      enrichedHits,
	})
}

// RebuildIndex 全量重建 Bleve 全文检索索引 (KB-01)。
//
// 默认异步执行并返回 job_id，前端可复用进度弹窗展示重建进度；
// 传 `?async=false` 或 JSON `{"async":false}` 时同步执行（便于测试与脚本调用）。
func (h *KnowledgeHandler) RebuildIndex(c *gin.Context) {
	if h.knowledgeSvc.IsReindexing() {
		ErrorResponse(c, http.StatusConflict, -1, "Another reindex job or document import is already running")
		return
	}

	isAsync := c.Query("async") != "false" && c.PostForm("async") != "false"
	var body struct {
		Async *bool `json:"async"`
	}
	if err := c.ShouldBindBodyWith(&body, binding.JSON); err == nil && body.Async != nil {
		isAsync = *body.Async
	}

	tracker := progress.GetHub().NewJob("reindex", "", knowledge.ReindexStages)
	tracker.AddLog("info", "收到重建索引请求 (mode: %s)", map[bool]string{true: "async", false: "sync"}[isAsync])

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in rebuild index: %v", r))
				}
			}()
			if err := h.knowledgeSvc.RebuildIndex(tracker); err != nil {
				logger.Log.Errorf("[API Knowledge] Rebuild index failed: %v", err)
			}
		}()

		SuccessResponse(c, gin.H{
			"job_id":   tracker.JobID(),
			"is_async": true,
		}, "索引重建任务已在后台启动")
		return
	}

	if err := h.knowledgeSvc.RebuildIndex(tracker); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Rebuild index failed: "+err.Error())
		return
	}
	SuccessResponse(c, nil, "索引重建完成")
}

// GetIndexStatus 返回索引健康状态，供前端判断是否提示"需要重建索引"
func (h *KnowledgeHandler) GetIndexStatus(c *gin.Context) {
	dirty := false
	if h.indexer != nil {
		dirty = h.indexer.IsMappingOutdated()
	}
	var docCount uint64
	if h.indexer != nil {
		docCount, _ = h.indexer.DocCount()
	}
	SuccessResponse(c, gin.H{
		"mapping_version":   search.IndexMappingVersion,
		"mapping_outdated":  dirty,
		"indexed_documents": docCount,
	})
}

// GetFacets 返回知识库分面（产品/版本/严重度/类型）。
func (h *KnowledgeHandler) GetFacets(c *gin.Context) {
	facets, err := h.knowledgeSvc.GetKnowledgeFacets(c.Query("module"), c.Query("entry_type"))
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, facets)
}

// SetFavorite 收藏知识。
func (h *KnowledgeHandler) SetFavorite(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if err := h.knowledgeSvc.SetFavorite(id, true); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, nil, "已收藏")
}

// RemoveFavorite 取消收藏。
func (h *KnowledgeHandler) RemoveFavorite(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if err := h.knowledgeSvc.SetFavorite(id, false); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, nil, "已取消收藏")
}

// ListFavorites 返回收藏列表。
func (h *KnowledgeHandler) ListFavorites(c *gin.Context) {
	items, err := h.knowledgeSvc.ListFavorites()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, items)
}

// ListRecent 返回最近浏览。
func (h *KnowledgeHandler) ListRecent(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	items, err := h.knowledgeSvc.ListRecent(limit)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, items)
}

// GetKnowledgeDetail 获取单条知识详情
func (h *KnowledgeHandler) GetKnowledgeDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid knowledge ID")
		return
	}

	k, err := h.knowledgeSvc.GetKnowledgeByID(uint(id))
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, "Knowledge not found: "+err.Error())
		return
	}
	// v1.3: 记录最近浏览（本地工具，GET 侧写入可接受；最多保留 50 条）
	h.knowledgeSvc.RecordRecent(uint(id))

	SuccessResponse(c, k)
}
