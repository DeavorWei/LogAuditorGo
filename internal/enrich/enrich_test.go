package enrich

import (
	"testing"
)

func TestEnrichParametersForComments(t *testing.T) {
	paramsJSON := `{"Slot":"1","DeviceModel":"CE6865E","Version":"V200R024C00SPC500B126"}`
	enriched := EnrichParameters(paramsJSON, nil)

	if len(enriched) != 3 {
		t.Fatalf("expected 3 enriched parameters, got %d", len(enriched))
	}

	foundSlot := false
	foundModel := false
	foundVer := false

	for _, p := range enriched {
		if p.Name == "Slot" {
			foundSlot = true
			if !p.Matched || p.Description == "" {
				t.Errorf("expected Slot to be matched with description, got matched=%v, desc=%s", p.Matched, p.Description)
			}
		}
		if p.Name == "DeviceModel" {
			foundModel = true
			if !p.Matched || p.Description == "" {
				t.Errorf("expected DeviceModel to be matched with description, got matched=%v, desc=%s", p.Matched, p.Description)
			}
		}
		if p.Name == "Version" {
			foundVer = true
			if !p.Matched || p.Description == "" {
				t.Errorf("expected Version to be matched with description, got matched=%v, desc=%s", p.Matched, p.Description)
			}
		}
	}

	if !foundSlot || !foundModel || !foundVer {
		t.Errorf("missing expected parameters: slot=%v, model=%v, ver=%v", foundSlot, foundModel, foundVer)
	}
}

func TestEnrichParametersDigest(t *testing.T) {
	paramsJSON := `{"DigestSeq":"0006756365","Digest":"3e0f5f595bfa263fff2638e6692bb42ce44af9c01af42a075add1073b287b917"}`
	enriched := EnrichParameters(paramsJSON, nil)

	if len(enriched) != 2 {
		t.Fatalf("expected 2 enriched parameters, got %d", len(enriched))
	}

	for _, p := range enriched {
		if !p.Matched || p.Description == "" {
			t.Errorf("expected %s to have matched description, got matched=%v, desc=%s", p.Name, p.Matched, p.Description)
		}
	}
}
