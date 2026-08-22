package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/search"
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

	var enrichedHits []EnrichedHit
	for _, hit := range res.Hits {
		k, err := h.knowledgeSvc.GetKnowledgeByID(hit.KnowledgeID)
		if err == nil && k != nil {
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

	SuccessResponse(c, gin.H{
		"total":     res.Total,
		"page":      page,
		"page_size": pageSize,
		"hits":      enrichedHits,
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
