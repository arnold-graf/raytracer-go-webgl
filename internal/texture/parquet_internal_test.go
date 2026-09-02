package texture

import "testing"

func TestParquetHerringboneLPairs(t *testing.T) {
	w := parquetPlankWid
	n := parquetPlankLen / w // 4:1 planks

	// Axis-aligned SO layout (Blindman67): H at (0,0) covers [0,n)×[0,1);
	// V at (n,0) covers [n,n+1)×[0,n). The short end of H meets the long
	// side of V at 90°.
	colH0, rowH0, fuH0, _ := parquetHerringbone(0.5*w, 0.5*w, parquetPlankLen, w)
	colH1, rowH1, fuH1, _ := parquetHerringbone((n-0.5)*w, 0.5*w, parquetPlankLen, w)
	if colH0 != colH1 || rowH0 != rowH1 {
		t.Fatalf("H plank should be continuous along length, got (%v,%v) vs (%v,%v)", colH0, rowH0, colH1, rowH1)
	}
	if fuH1 <= fuH0 {
		t.Fatalf("fu should increase along the H plank, start=%v end=%v", fuH0, fuH1)
	}

	colV0, rowV0, fuV0, _ := parquetHerringbone((n+0.5)*w, 0.5*w, parquetPlankLen, w)
	colV1, rowV1, fuV1, _ := parquetHerringbone((n+0.5)*w, (n-0.5)*w, parquetPlankLen, w)
	if colH0 == colV0 && rowH0 == rowV0 {
		t.Fatalf("V plank at the H tip should be a different plank, both id (%v,%v)", colH0, rowH0)
	}
	if colV0 != colV1 || rowV0 != rowV1 {
		t.Fatalf("V plank should be continuous along length, got (%v,%v) vs (%v,%v)", colV0, rowV0, colV1, rowV1)
	}
	if fuV1 <= fuV0 {
		t.Fatalf("fu should increase along the V plank, start=%v end=%v", fuV0, fuV1)
	}

	colAcross, rowAcross, _, _ := parquetHerringbone(0.5*w, 1.5*w, parquetPlankLen, w)
	if colAcross == colH0 && rowAcross == rowH0 {
		t.Fatalf("moving across H width should leave the plank")
	}
}
