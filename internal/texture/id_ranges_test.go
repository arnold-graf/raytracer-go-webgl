package texture

import "testing"

func TestCaptureAndDocumentIDRangesDisjoint(t *testing.T) {
	for _, id := range CaptureIDs {
		if IsDocument(id) {
			t.Fatalf("capture id %d overlaps document range", id)
		}
	}
	for id := DocumentBase; id < DocumentBase+DocumentCount; id++ {
		if IsCapture(id) {
			t.Fatalf("document id %d overlaps capture range", id)
		}
	}
	if CaptureBackward >= DocumentBase {
		t.Fatalf("CaptureBackward=%d must be below DocumentBase=%d", CaptureBackward, DocumentBase)
	}
}
