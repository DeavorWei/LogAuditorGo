package enrich

import (
	"testing"

	"logauditorgo/internal/model"
)

// TestExtractMatchedDocName_ByDocumentID 验证文档归属按"匹配当时胜出的文档 ID"精确解析，
// 并在历史行（docID == 0）与文档名缺失时平滑回退，杜绝张冠李戴。
func TestExtractMatchedDocName_ByDocumentID(t *testing.T) {
	kb := &model.Knowledge{
		ID: 11534,
		Versions: []model.KnowledgeVersionMapping{
			{ID: 1, DocumentID: 3823, ProductType: "CloudEngine 16800", DocName: "CloudEngine 16800 产品文档"},
			{ID: 2, DocumentID: 11534, ProductType: "WLAN", DocName: "WLAN 产品文档"},
		},
	}

	// 1. 精确命中落库的文档 ID：必须返回该文档，而不是 Versions 中的第一条
	if got := extractMatchedDocName(kb, 11534); got != "WLAN 产品文档" {
		t.Fatalf("matched document name = %q, want %q", got, "WLAN 产品文档")
	}
	if got := extractMatchedDocName(kb, 3823); got != "CloudEngine 16800 产品文档" {
		t.Fatalf("matched document name = %q, want %q", got, "CloudEngine 16800 产品文档")
	}

	// 2. 历史行（docID == 0）回退为任一非空 DocName，行为与改造前一致
	if got := extractMatchedDocName(kb, 0); got != "CloudEngine 16800 产品文档" {
		t.Fatalf("legacy fallback = %q, want first non-empty doc name", got)
	}

	// 3. 落库 ID 在知识条目内找不到（知识库热更新/文档被删）：回退到非空 DocName
	if got := extractMatchedDocName(kb, 999999); got != "CloudEngine 16800 产品文档" {
		t.Fatalf("unknown doc id fallback = %q, want first non-empty doc name", got)
	}

	// 4. DocName 全缺失时回退 ProductType
	noName := &model.Knowledge{
		ID:       1,
		Versions: []model.KnowledgeVersionMapping{{ID: 1, DocumentID: 7, ProductType: "S12700, S12700E"}},
	}
	if got := extractMatchedDocName(noName, 7); got != "S12700, S12700E" {
		t.Fatalf("product type fallback = %q, want %q", got, "S12700, S12700E")
	}

	// 5. 空知识 / 无版本映射
	if got := extractMatchedDocName(nil, 7); got != "" {
		t.Fatalf("nil knowledge must return empty, got %q", got)
	}
	if got := extractMatchedDocName(&model.Knowledge{ID: 2}, 7); got != "" {
		t.Fatalf("knowledge without versions must return empty, got %q", got)
	}
}
