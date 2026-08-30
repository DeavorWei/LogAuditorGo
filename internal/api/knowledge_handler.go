package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

type KnowledgeHandler struct {
	knowledgeSvc *knowledge.Service
	indexer      *search.Indexer
}

func NewKnowledgeHandler(knowledgeSvc *knowledge.Service, indexer *search.Indexer) *KnowledgeHandler {
	return &KnowledgeHandler{
		knowledgeSvc: knowledgeSvc,
		indexer:      indexer,
	}
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

	filter := search.SearchFilter{
		Keyword:   keyword,
		Module:    module,
		Severity:  sevPtr,
		EntryType: entryType,
		Product:   product,
		Version:   version,
		Page:      page,
		PageSize:  pageSize,
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
// 传 `?async=false` 时同步执行（便于测试与脚本调用）。
func (h *KnowledgeHandler) RebuildIndex(c *gin.Context) {
	isAsync := c.Query("async") != "false" && c.PostForm("async") != "false"

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

// GetKnowledgeDetail 获取单条知识详情
func (h *KnowledgeHandler) GetKnowledgeDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid knowledge ID")
		return
	}

	k, err := h.knowledgeSvc.GetKnowledgeByID(uint(id))
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, "Knowledge not found: "+err.Error())
		return
	}

	SuccessResponse(c, k)
}
